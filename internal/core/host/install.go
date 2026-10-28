package host

import (
	"fmt"
	"regexp"
	"strings"
)

// CaddyImage is the edge OAP installs, pinned by digest.
const CaddyImage = "caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d"

// OapdSocket is where oapd's agent API listens on the server. Only root can
// reach it; the owner does so over SSH.
const OapdSocket = "/run/oapd/oapd.sock"

var (
	installID = regexp.MustCompile(`^[a-z0-9]{6,16}$`)
	imageRef  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$`)
	apiHost   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// Install describes what `oap host install` puts on a server.
type Install struct {
	// ID names the installation; every resource carries it as a label.
	ID string
	// Oapd and Caddy are image references present on the server.
	Oapd, Caddy string
	// TLS gives routed hosts automatic HTTPS certificates.
	TLS bool
	// APIHost is the public host name of the agent API; empty keeps the
	// API on oapd's local socket only.
	APIHost string
}

// Script returns the install script: idempotent, so running it again
// changes nothing, and running it with a newer oapd image upgrades oapd
// in place, keeping its data. It creates only resources labelled with the
// installation id, plus /var/lib/oap and /run/oapd.
func (in Install) Script() (string, error) {
	if !installID.MatchString(in.ID) {
		return "", fmt.Errorf("host: installation id %q must be 6 to 16 lowercase letters and digits", in.ID)
	}
	if in.APIHost != "" && !apiHost.MatchString(in.APIHost) {
		return "", fmt.Errorf("host: %q isn't a host name", in.APIHost)
	}
	for _, img := range []string{in.Oapd, in.Caddy} {
		if !imageRef.MatchString(img) {
			return "", fmt.Errorf("host: %q isn't an image reference", img)
		}
	}
	tls := "false"
	if in.TLS {
		tls = "true"
	}
	s := fmt.Sprintf(`set -eu
ID=%s
OAPD=%s
CADDY=%s
TLS=%s
API=%s
L="--label io.openappplatform.install=$ID"
mkdir -p /var/lib/oap /run/oapd
chmod 700 /var/lib/oap /run/oapd
printf '%%s\n' "$ID" > /var/lib/oap/install-id
cat > /var/lib/oap/caddy.json <<'CADDYJSON'
{"admin": {"listen": "unix//run/oap-caddy/admin.sock"}, "apps": {"http": {"grace_period": "30s", "servers": {}}}}
CADDYJSON
docker network inspect "oap-$ID" >/dev/null 2>&1 || docker network create $L --label io.openappplatform.role=network "oap-$ID" >/dev/null
for v in data caddy-data caddy-config caddy-admin; do
  docker volume inspect "oap-$ID-$v" >/dev/null 2>&1 || docker volume create $L --label io.openappplatform.role=volume "oap-$ID-$v" >/dev/null
done
docker image inspect "$CADDY" >/dev/null 2>&1 || { docker pull -q "$CADDY" >/dev/null; printf '%%s\n' "$CADDY" >> /var/lib/oap/images; }
docker image inspect "$OAPD" >/dev/null 2>&1 || { docker pull -q "$OAPD" >/dev/null || { echo "oap: image $OAPD isn't on the server and couldn't be pulled" >&2; exit 3; }; printf '%%s\n' "$OAPD" >> /var/lib/oap/images; }

# current NAME IMAGE: true when NAME runs IMAGE with this configuration.
current() {
  [ "$(docker inspect -f '{{.Image}} {{.State.Running}} {{index .Config.Labels "io.openappplatform.config"}}' "$1" 2>/dev/null)" = "$(docker image inspect -f '{{.Id}}' "$2") true $3" ]
}

CADDYCONF="caddy-1"
if current "oap-$ID-caddy" "$CADDY" "$CADDYCONF"; then
  echo "caddy unchanged"
else
  docker rm -f "oap-$ID-caddy" >/dev/null 2>&1 || true
  docker run -d --name "oap-$ID-caddy" --restart unless-stopped $L \
    --label io.openappplatform.role=system --label io.openappplatform.component=caddy --label "io.openappplatform.config=$CADDYCONF" \
    --network "oap-$ID" -p 80:80 -p 443:443 -p 443:443/udp --memory 160m \
    -v "oap-$ID-caddy-data:/data" -v "oap-$ID-caddy-config:/config" -v "oap-$ID-caddy-admin:/run/oap-caddy" \
    -v /var/lib/oap/caddy.json:/etc/oap/caddy.json:ro \
    "$CADDY" caddy run --resume --config /etc/oap/caddy.json >/dev/null
  echo "caddy started"
fi

OAPDCONF="oapd-2-tls-$TLS-api-$API"
if current "oap-$ID-oapd" "$OAPD" "$OAPDCONF"; then
  echo "oapd unchanged"
else
  docker rm -f "oap-$ID-oapd" >/dev/null 2>&1 || true
  docker run -d --name "oap-$ID-oapd" --restart unless-stopped $L \
    --label io.openappplatform.role=system --label io.openappplatform.component=oapd --label "io.openappplatform.config=$OAPDCONF" \
    --network "oap-$ID" --memory 160m \
    -v /var/run/docker.sock:/var/run/docker.sock -v "oap-$ID-data:/var/lib/oapd" \
    -v "oap-$ID-caddy-admin:/run/oap-caddy" -v /run/oapd:/run/oapd \
    -e "OAPD_INSTALL=$ID" -e "OAPD_TLS=$TLS" -e "OAPD_API_HOST=$API" \
    "$OAPD" >/dev/null
  echo "oapd started"
fi

i=0
until [ -S %s ] && curl -sf --unix-socket %s http://oapd/healthz >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 60 ]; then
    echo "oap: oapd didn't become ready; its log:" >&2
    docker logs --tail 30 "oap-$ID-oapd" >&2
    exit 1
  fi
  sleep 1
done
echo "oapd ready"
`, in.ID, in.Oapd, in.Caddy, tls, in.APIHost, OapdSocket, OapdSocket)
	return s, nil
}

// Uninstall returns the script that removes an installation's containers,
// network and volumes, and its files. It removes nothing without the
// installation's label.
func (in Install) Uninstall() (string, error) {
	if !installID.MatchString(in.ID) {
		return "", fmt.Errorf("host: installation id %q isn't valid", in.ID)
	}
	return strings.ReplaceAll(`set -eu
ID=@ID@
F="--filter label=io.openappplatform.install=$ID"
docker ps -aq $F | xargs -r docker rm -f >/dev/null
docker network ls -q $F | xargs -r docker network rm >/dev/null
docker volume ls -q $F | xargs -r docker volume rm >/dev/null
# Images OAP's builder made carry the installation label too.
docker images -q $F | sort -u | xargs -r docker image rm -f >/dev/null
# The images OAP brought, unless something else now uses them.
if [ -f /var/lib/oap/images ]; then
  while read -r img; do
    [ -n "$img" ] || continue
    if [ -z "$(docker ps -aq --filter "ancestor=$img")" ]; then docker image rm "$img" >/dev/null 2>&1 || true; fi
  done < /var/lib/oap/images
fi
rm -f /var/lib/oap/install-id /var/lib/oap/caddy.json /var/lib/oap/images
rm -rf /run/oapd
echo "removed installation $ID"
`, "@ID@", in.ID), nil
}
