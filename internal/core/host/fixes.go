package host

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ManifestPath is where the host manifest lives on the server: one JSON
// line per fix applied or undone, so uninstall can reverse exactly what
// onboarding did (docs/architecture/onboarding.md, principle 4).
const ManifestPath = "/var/lib/oap/host-manifest.jsonl"

// FixScript is how OAP applies a fix, and undoes it when it can. Scripts
// are POSIX sh, run as root, and fail rather than overwrite something OAP
// didn't create.
type FixScript struct {
	Apply string
	Undo  string // empty when the fix can't be undone
}

const swapLine = "/swapfile none swap sw 0 0 # added by oap (swap-file)"

// Scripts holds a script for every fix Check can propose.
var Scripts = map[string]FixScript{
	"swap-file": {
		Apply: `set -eu
if [ -e /swapfile ]; then echo "oap: /swapfile already exists and OAP didn't create it" >&2; exit 3; fi
fallocate -l 1G /swapfile 2>/dev/null || dd if=/dev/zero of=/swapfile bs=1M count=1024 status=none
chmod 600 /swapfile
mkswap /swapfile >/dev/null
swapon /swapfile
grep -qxF '` + swapLine + `' /etc/fstab || printf '%s\n' '` + swapLine + `' >> /etc/fstab`,
		Undo: `set -eu
if ! grep -qxF '` + swapLine + `' /etc/fstab; then echo "oap: /etc/fstab has no swap entry from OAP" >&2; exit 3; fi
swapoff /swapfile 2>/dev/null || true
grep -vxF '` + swapLine + `' /etc/fstab > /etc/fstab.oap-tmp || true
cat /etc/fstab.oap-tmp > /etc/fstab
rm -f /etc/fstab.oap-tmp /swapfile`,
	},
	"open-web-ports": {
		Apply: `set -eu
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp`,
		Undo: `set -eu
ufw delete allow 80/tcp
ufw delete allow 443/tcp
ufw delete allow 443/udp`,
	},
	"time-sync": {
		Apply: `set -eu
timedatectl set-ntp true`,
		Undo: `set -eu
timedatectl set-ntp false`,
	},
	"upgrade-docker": {
		Apply: `set -eu
export DEBIAN_FRONTEND=noninteractive
if dpkg -s docker-ce >/dev/null 2>&1; then pkgs="docker-ce docker-ce-cli containerd.io"
elif dpkg -s docker.io >/dev/null 2>&1; then pkgs="docker.io"
else echo "oap: Docker isn't from docker-ce or docker.io; OAP doesn't upgrade it" >&2; exit 3; fi
apt-get update -q
apt-get install -y -q --only-upgrade $pkgs
v="$(docker version --format '{{.Server.Version}}')"
echo "$v"
[ "${v%%.*}" -ge 24 ] || { echo "oap: Docker is still $v after upgrading; the package source has nothing newer" >&2; exit 3; }`,
	},
	"install-docker": {
		Apply: `set -eu
export DEBIAN_FRONTEND=noninteractive
if pidof dockerd >/dev/null 2>&1; then echo "oap: a Docker daemon is already running; OAP doesn't replace it" >&2; exit 3; fi
. /etc/os-release
case "$ID" in ubuntu|debian) ;; *) echo "oap: no Docker install for $ID" >&2; exit 3 ;; esac
apt-get update -q
apt-get install -y -q ca-certificates curl
install -m 0755 -d /etc/apt/keyrings
curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
  "$(dpkg --print-architecture)" "$ID" "$VERSION_CODENAME" > /etc/apt/sources.list.d/docker.list
apt-get update -q
apt-get install -y -q docker-ce docker-ce-cli containerd.io
systemctl enable --now docker
docker version --format '{{.Server.Version}}'`,
	},
}

// Batch is a set of fixes the owner approves with one answer. Routine
// fixes share a batch; every sensitive or destructive fix is its own.
type Batch struct {
	Tier  Tier          `json:"tier"`
	Fixes []ProposedFix `json:"fixes"`
}

// ProposedFix is a fix with the exact commands it will run.
type ProposedFix struct {
	Fix
	Why      string `json:"why"`
	Observed string `json:"observed"`
	Script   string `json:"script"`
	CanUndo  bool   `json:"can_undo"`
}

// Propose turns findings into batches for the owner to approve, routine
// first. A fix with no script is reported in missing.
func Propose(fs []Finding) (batches []Batch, missing []string) {
	var routine []ProposedFix
	var single []Batch
	seen := map[string]bool{}
	for _, f := range fs {
		if f.Fix == nil || seen[f.Fix.ID] {
			continue
		}
		seen[f.Fix.ID] = true
		sc, ok := Scripts[f.Fix.ID]
		if !ok {
			missing = append(missing, f.Fix.ID)
			continue
		}
		p := ProposedFix{Fix: *f.Fix, Why: f.Why, Observed: f.Observed, Script: sc.Apply, CanUndo: sc.Undo != ""}
		if f.Fix.Tier == Routine {
			routine = append(routine, p)
		} else {
			single = append(single, Batch{Tier: f.Fix.Tier, Fixes: []ProposedFix{p}})
		}
	}
	if len(routine) > 0 {
		batches = append(batches, Batch{Tier: Routine, Fixes: routine})
	}
	return append(batches, single...), missing
}

// ManifestEntry is one line of the host manifest.
type ManifestEntry struct {
	Fix    string `json:"fix"`
	Action string `json:"action"` // "apply" or "undo"
	At     string `json:"at"`
	Until  string `json:"until,omitempty"` // when time-limited access ends
}

// RecordScript appends an entry to the host manifest.
func RecordScript(fix, action string, at time.Time) string {
	return RecordUntil(fix, action, at, time.Time{})
}

// RecordUntil appends an entry for time-limited access to the manifest.
func RecordUntil(fix, action string, at, until time.Time) string {
	e := ManifestEntry{Fix: fix, Action: action, At: at.UTC().Format(time.RFC3339)}
	if !until.IsZero() {
		e.Until = until.UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(e)
	return fmt.Sprintf("set -eu\nmkdir -p /var/lib/oap\nchmod 700 /var/lib/oap\nprintf '%%s\\n' '%s' >> %s\n", b, ManifestPath)
}

// Applied returns the fixes the manifest shows as applied and not undone,
// in the order they were applied.
func Applied(manifest string) []string {
	var order []string
	on := map[string]bool{}
	for _, line := range strings.Split(manifest, "\n") {
		var e ManifestEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.Fix == "" {
			continue
		}
		switch e.Action {
		case "apply":
			if !on[e.Fix] {
				order = append(order, e.Fix)
			}
			on[e.Fix] = true
		case "undo":
			on[e.Fix] = false
		}
	}
	var out []string
	for _, f := range order {
		if on[f] {
			out = append(out, f)
		}
	}
	return out
}
