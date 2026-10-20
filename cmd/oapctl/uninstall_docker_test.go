package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// TestInstallUninstallRestoresHost is R3.14's acceptance: on a fresh host,
// connect's key, the real oapd and Caddy, and an application deployed
// through oapd's API are installed; uninstall (with its key removal) then
// leaves only what was there before, compared by a host snapshot of the
// filesystem and Docker's containers, images, volumes and networks. The
// host is a Docker-in-Docker container. Gated on OAP_TEST_DOCKER=1.
func TestInstallUninstallRestoresHost(t *testing.T) {
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("needs Docker; set OAP_TEST_DOCKER=1")
	}
	run := func(stdin []byte, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	// oapd's image for this machine's architecture.
	ctxDir := t.TempDir()
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(ctxDir, "oapd-linux-"+runtime.GOARCH), "../oapd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build oapd: %v\n%s", err, out)
	}
	df, _ := os.ReadFile("../oapd/Dockerfile")
	os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), df, 0o644)
	const image = "oapd:r314-test"
	run(nil, "docker", "build", "-q", "-t", image, ctxDir)
	t.Cleanup(func() { exec.Command("docker", "image", "rm", image).Run() })

	// A fresh host with Docker, curl and an owner's own SSH key.
	name := fmt.Sprintf("oap-host-test-%d", time.Now().UnixNano())
	// The image is public; an empty Docker config keeps a desktop credential
	// helper, which can hang waiting for the keychain, out of the pull.
	run(nil, "env", "DOCKER_CONFIG="+t.TempDir(), "docker", "pull", "-q", "docker:27.5.1-dind")
	run(nil, "docker", "run", "-d", "--privileged", "--name", name, "docker:27.5.1-dind")
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", "-v", name).Run() })
	sh := func(script string) string {
		t.Helper()
		return run([]byte(script), "docker", "exec", "-i", name, "sh", "-s")
	}
	sh(`i=0; until docker info >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 60 ] && exit 1; sleep 1; done
apk add -q --no-cache curl
mkdir -p /root/.ssh && chmod 700 /root/.ssh
echo 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOwnersownkey owner@laptop' > /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys`)
	snapshot := func() string {
		t.Helper()
		return sh(`set -eu
P='( -path /proc -o -path /sys -o -path /dev -o -path /run -o -path /tmp -o -path /var/lib/docker -o -path /var/run -o -path /etc/hostname -o -path /etc/hosts -o -path /etc/resolv.conf )'
find / -xdev $P -prune -o -type d -print | sort
find / -xdev $P -prune -o -type l -print | sort
find / -xdev $P -prune -o -type f -print0 | sort -z | xargs -0 sha256sum
echo '--- docker'
docker ps -a --format '{{.Names}} {{.Image}}' | sort
docker images --format '{{.Repository}}:{{.Tag}} {{.ID}}' | sort
docker volume ls -q | sort
docker network ls --format '{{.Name}}' | sort`)
	}
	before := snapshot()

	// Connect: OAP's key. Install: oapd's image sent over, then the script.
	const bootstrap = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOAPbootstrapkeyfortests oap-bootstrap 2026-10-20 laptop"
	sh(fmt.Sprintf("printf '%%s\\n' '%s' >> /root/.ssh/authorized_keys", bootstrap))
	load, err := host.LoadImageCommand(image)
	if err != nil {
		t.Fatal(err)
	}
	saved := run(nil, "docker", "save", image)
	run([]byte(saved), "docker", "exec", "-i", name, "sh", "-c", load)
	in := host.Install{ID: "r314test", Oapd: image, Caddy: host.CaddyImage}
	script, err := in.Script()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(sh(script))
	sh(host.RecordScript("install-oap", "apply", time.Now()))

	// An application, deployed through oapd's API on its socket.
	const site = `{"intent": {"application": "demo", "spec_patch": {"merge_patch": {"environment": "production",
  "components": {"web": {"kind": "web", "image": "` + host.CaddyImage + `", "port": 80, "volumes": {"data": "/data"}}},
  "ingress": {"domain": "demo.test", "routes": {"/": {"component": "web"}}}}}}}`
	out := sh(`set -eu
api() { curl -sf --unix-socket /run/oapd/oapd.sock -H 'Content-Type: application/json' -d "$2" "http://oapd/oap.v1.AgentService/$1"; }
p=$(api Propose '` + strings.ReplaceAll(site, "\n", " ") + `' | sed -n 's/.*"proposal":{"id":"\([^"]*\)".*/\1/p')
[ -n "$p" ] || { echo "no proposal" >&2; exit 1; }
api Apply "{\"proposal_id\": \"$p\"}" >/dev/null
i=0; until [ -n "$(docker ps -q --filter label=io.openappplatform.install=r314test --filter label=io.openappplatform.application=demo)" ]; do
  i=$((i+1)); [ $i -gt 60 ] && { echo "the application never started" >&2; exit 1; }; sleep 1; done
docker ps --format '{{.Names}}' --filter label=io.openappplatform.install=r314test`)
	t.Log(out)
	if strings.Count(out, "\n") < 3 {
		t.Fatalf("want oapd, Caddy and the application running:\n%s", out)
	}
	if during := snapshot(); during == before {
		t.Fatal("the install changed nothing; the comparison would prove nothing")
	}

	// Uninstall as the CLI does: the manifest's steps, then forget, then the key.
	steps, err := host.PlanUninstall(in.ID, sh("cat "+host.ManifestPath), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Kept != "" || st.Script == "" {
			t.Fatalf("unexpected step %+v", st)
		}
		t.Log(sh(st.Script))
	}
	sh(host.ForgetScript)
	rm, err := host.RemoveKeyOnlyScript(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.TrimSpace(sh(rm)); n != "1" {
		t.Fatalf("removed %s key lines, want 1", n)
	}

	after := snapshot()
	if after != before {
		t.Fatalf("the host differs after uninstall:\n%s", diffLines(before, after))
	}
}

// diffLines lists lines only in a or only in b.
func diffLines(a, b string) string {
	in := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	var out []string
	for _, l := range strings.Split(a, "\n") {
		if !mb[l] {
			out = append(out, "- "+l)
		}
	}
	for _, l := range strings.Split(b, "\n") {
		if !ma[l] {
			out = append(out, "+ "+l)
		}
	}
	return strings.Join(out, "\n")
}
