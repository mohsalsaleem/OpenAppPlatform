package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	const image = "oapd:r314-test"
	buildOapdImage(t, image)

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

// TestHandoffAgainstSSHD runs `oap host handoff` with the system ssh client
// against a real sshd in a container: afterwards the server refuses OAP's
// key, the owner's own key line is untouched, and the manifest records the
// removal. Gated on OAP_TEST_DOCKER=1.
func TestHandoffAgainstSSHD(t *testing.T) {
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
	const img = "ubuntu:26.04"
	run(nil, "env", "DOCKER_CONFIG="+t.TempDir(), "docker", "pull", "-q", img)
	name := fmt.Sprintf("oap-sshd-test-%d", time.Now().UnixNano())
	run(nil, "docker", "run", "-d", "--name", name, "-p", "127.0.0.1::22", img, "sh", "-c",
		"apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server >/dev/null && mkdir -p /run/sshd && exec /usr/sbin/sshd -D -e")
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	port := strings.TrimSpace(run(nil, "docker", "port", name, "22"))
	port = port[strings.LastIndex(port, ":")+1:]
	addr := "root@127.0.0.1:" + port

	local, public := twoListeners(t)
	h := newHarness(t, false, "", "")
	h.a.now = time.Now
	h.a.ssh.Command = nil // the system ssh, ssh-keyscan and ssh-keygen
	dir := filepath.Join(h.a.dir(), "hosts", "127.0.0.1_"+port)
	os.MkdirAll(dir, 0o700)
	run(nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "oap-bootstrap test", "-f", filepath.Join(dir, "bootstrap_ed25519"))
	pub, _ := os.ReadFile(filepath.Join(dir, "bootstrap_ed25519.pub"))
	const ownerLine = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOwnersownkey owner@laptop"
	var keys string
	for i := 0; i < 120; i++ { // sshd is up once it answers a key scan
		out, err := exec.Command("ssh-keyscan", "-p", port, "127.0.0.1").Output()
		if err == nil && len(out) > 0 {
			keys = string(out)
			break
		}
		time.Sleep(time.Second)
	}
	if keys == "" {
		t.Fatal("sshd never came up")
	}
	os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(keys), 0o600)
	run([]byte(ownerLine+"\n"+string(pub)), "docker", "exec", "-i", name, "sh", "-c",
		"mkdir -p /root/.ssh && chmod 700 /root/.ssh && cat > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys")

	tgt, err := h.a.target(addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.ssh.Run(context.Background(), tgt, "true"); err != nil {
		t.Fatalf("OAP's key doesn't work before hand-off: %v", err)
	}
	if err := h.a.update(host.Server{Address: addr, Stage: host.StageOwned, Install: "r314test", API: public}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAP_AGENT", "")
	t.Setenv("OAP_AGENT_URL", local)
	if code := h.run("owner", "enroll", "--label", "test"); code != 0 {
		t.Fatalf("enroll: %s", h.out.String())
	}
	t.Setenv("OAP_AGENT_URL", "")
	h.out.Reset()
	if code := h.run("host", "handoff", addr); code != 0 {
		t.Fatalf("hand-off: %s", h.out.String())
	}
	if _, err := h.a.ssh.Run(context.Background(), tgt, "true"); !keyRefused(err) {
		t.Fatalf("OAP's key after hand-off: %v", err)
	}
	ak := run(nil, "docker", "exec", name, "cat", "/root/.ssh/authorized_keys")
	if strings.TrimSpace(ak) != ownerLine {
		t.Fatalf("authorized_keys after hand-off:\n%s", ak)
	}
	if m := run(nil, "docker", "exec", name, "cat", host.ManifestPath); !strings.Contains(m, `"fix":"bootstrap-key","action":"undo"`) {
		t.Fatalf("manifest: %s", m)
	}
	if s, _ := h.a.server(addr); s.Stage != host.StageHandedOff {
		t.Fatalf("stage %s", s.Stage)
	}
}
