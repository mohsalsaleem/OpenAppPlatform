package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// TestProbeIsReadOnly runs the probe on supported systems with a read-only
// root filesystem and no network, and again writable, where docker diff
// must show no change.
// Gated on OAP_TEST_DOCKER=1 because it needs Docker.
func TestProbeIsReadOnly(t *testing.T) {
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("needs Docker; set OAP_TEST_DOCKER=1")
	}
	for _, image := range []string{"ubuntu:26.04", "ubuntu:24.04", "ubuntu:22.04", "debian:13", "debian:12"} {
		t.Run(image, func(t *testing.T) {
			var out, errb bytes.Buffer
			cmd := exec.Command("docker", "run", "--rm", "-i", "--read-only", "--network", "none", image, "sh", "-s")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(host.Probe), &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("probe failed on a read-only %s: %v\n%s", image, err, errb.String())
			}
			if strings.Contains(errb.String(), "Read-only file system") {
				t.Fatalf("probe tried to write:\n%s", errb.String())
			}
			f, err := host.ParseProbe(out.String())
			if err != nil {
				t.Fatal(err)
			}
			// A read-only root can hide a write whose error goes to
			// /dev/null, so also run it writable and require no changes.
			name := "oap-probe-test-" + strings.NewReplacer(":", "-", ".", "-").Replace(image)
			exec.Command("docker", "rm", "-f", name).Run()
			defer exec.Command("docker", "rm", "-f", name).Run()
			create := exec.Command("docker", "create", "-i", "--name", name, "--network", "none", image, "sh", "-s")
			if b, err := create.CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, b)
			}
			start := exec.Command("docker", "start", "-a", "-i", name)
			start.Stdin = strings.NewReader(host.Probe)
			if b, err := start.CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, b)
			}
			diff, err := exec.Command("docker", "diff", name).Output()
			if err != nil || len(bytes.TrimSpace(diff)) != 0 {
				t.Fatalf("the probe changed the filesystem: %v\n%s", err, diff)
			}
			for _, fd := range host.Check(f) {
				if fd.Why == "host.os.unsupported" {
					t.Fatalf("%s judged unsupported: %s", image, fd.Observed)
				}
			}
		})
	}
}
