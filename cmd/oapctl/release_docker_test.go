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

// TestReleaseInstallsPinnedImage is R5.6's acceptance, with a local
// registry standing in for GHCR: scripts/release.sh builds a release and
// pushes its oapd image for amd64 and arm64; the released oapctl pins that
// image by digest; and a fresh host, given only the pinned reference,
// pulls exactly that image and runs it. Every file matches SHA256SUMS.
// Gated on OAP_TEST_DOCKER=1.
func TestReleaseInstallsPinnedImage(t *testing.T) {
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("needs Docker; set OAP_TEST_DOCKER=1")
	}
	n := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	net, reg, builder, hostName := "oaprel-"+n, "oapreg"+n, "oaprelb"+n, "oaprelhost"+n
	cfg := t.TempDir() // a Docker config without the desktop credential helper
	os.WriteFile(filepath.Join(cfg, "config.json"), []byte("{}"), 0o600)
	home, _ := os.UserHomeDir()
	os.Symlink(filepath.Join(home, ".docker", "cli-plugins"), filepath.Join(cfg, "cli-plugins"))
	env := append(os.Environ(), "DOCKER_CONFIG="+cfg, "BUILDX_CONFIG="+filepath.Join(home, ".docker", "buildx"))
	run := func(stdin []byte, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Env = env
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	quiet := func(name string, args ...string) { cmd := exec.Command(name, args...); cmd.Env = env; cmd.Run() }

	run(nil, "docker", "network", "create", net)
	t.Cleanup(func() { quiet("docker", "network", "rm", net) })
	run(nil, "docker", "run", "-d", "--network", net, "--name", reg, "registry:2")
	t.Cleanup(func() { quiet("docker", "rm", "-f", "-v", reg) })
	bk := filepath.Join(t.TempDir(), "buildkitd.toml")
	os.WriteFile(bk, []byte(fmt.Sprintf("[registry.%q]\n  http = true\n  insecure = true\n", reg+":5000")), 0o644)
	run(nil, "docker", "buildx", "create", "--name", builder, "--driver", "docker-container", "--driver-opt", "network="+net, "--buildkitd-config", bk)
	t.Cleanup(func() { quiet("docker", "buildx", "rm", "-f", builder) })

	const version = "v9.9.9-rc.1"
	rel := exec.Command("../../scripts/release.sh", version)
	rel.Env = append(env, "LOCAL_REGISTRY=1", "SKIP_CHECK=1", "BUILDER="+builder, "IMAGE_REPO="+reg+":5000/oapd")
	if out, err := rel.CombinedOutput(); err != nil {
		t.Fatalf("release: %v\n%s", err, out)
	}
	dist := filepath.Join("..", "..", "dist", version)
	t.Cleanup(func() { os.RemoveAll(dist) })
	run(nil, "sh", "-c", "cd "+dist+" && shasum -a 256 -c SHA256SUMS")
	b, _ := os.ReadFile(filepath.Join(dist, "oapd-image.txt"))
	pin := strings.TrimSpace(string(b))
	if !strings.HasPrefix(pin, reg+":5000/oapd:"+version+"@sha256:") {
		t.Fatalf("pinned image %q", pin)
	}
	if out := run(nil, filepath.Join(dist, "oapctl-"+runtime.GOOS+"-"+runtime.GOARCH), "version"); !strings.Contains(out, "oapctl "+version) || !strings.Contains(out, pin) {
		t.Fatalf("oapctl version:\n%s", out)
	}

	// A fresh host on the registry's network, given only the pinned reference.
	run(nil, "docker", "pull", "-q", "docker:27.5.1-dind")
	run(nil, "docker", "run", "-d", "--privileged", "--network", net, "--name", hostName, "docker:27.5.1-dind", "--insecure-registry", reg+":5000")
	t.Cleanup(func() { quiet("docker", "rm", "-f", "-v", hostName) })
	sh := func(script string) string {
		t.Helper()
		return run([]byte(script), "docker", "exec", "-i", hostName, "sh", "-s")
	}
	sh(`i=0; until docker info >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 60 ] && exit 1; sleep 1; done; apk add -q --no-cache curl`)
	script, err := host.Install{ID: "reltest1", Oapd: pin, Caddy: host.CaddyImage}.Script()
	if err != nil {
		t.Fatal(err)
	}
	if out := sh(script); !strings.Contains(out, "oapd ready") {
		t.Fatalf("install: %s", out)
	}
	digest := pin[strings.Index(pin, "@")+1:]
	if got := sh(`docker image inspect -f '{{join .RepoDigests " "}}' ` + pin); !strings.Contains(got, digest) {
		t.Fatalf("the host runs %s, not the pinned %s", got, digest)
	}
	if got := sh(`cat /var/lib/oap/images`); !strings.Contains(got, pin) {
		t.Fatalf("the pulled image isn't recorded for uninstall: %s", got)
	}
	if v := sh(`docker exec oap-reltest1-oapd oapd -version`); !strings.Contains(v, version) {
		t.Fatalf("oapd -version: %s", v)
	}
}
