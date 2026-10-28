package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sshhost"
)

// TestReleasedImageIsPulledByTheServer: an image pinned by digest in a
// registry (the oapd a release names) is always pulled by the server, which
// picks its own architecture from the image index, even when this machine
// has a copy: that copy is this machine's architecture. Found in R5.3: a
// Mac that had pulled the released oapd refused to install it on an amd64
// server.
func TestReleasedImageIsPulledByTheServer(t *testing.T) {
	const released = "ghcr.io/mohsalsaleem/oapd:v0.2.0-rc.1@sha256:3f8b3b2d0ce794bf07a1b03b3620195addc1d474b4eaace444a557492221d8dd"
	for _, c := range []struct {
		image string
		ship  bool
	}{
		{released, false},
		{"oapd:dev", true}, // built here: sent over SSH
	} {
		h := newHarness(t, false, "", "")
		var docker []string
		h.a.docker = func(ctx context.Context, args ...string) *exec.Cmd {
			docker = append(docker, strings.Join(args, " "))
			if args[0] == "image" {
				return exec.Command("echo", `["sha256:aa"]["/oapd"]null arm64`)
			}
			return exec.Command("false")
		}
		err := h.a.shipImage(context.Background(), sshhost.Target{User: "root", Host: "203.0.113.5", Port: 22, Dir: t.TempDir()}, c.image)
		shipped := strings.Contains(strings.Join(docker, "\n"), "save")
		if shipped != c.ship {
			t.Errorf("%s: shipped from this machine = %v, want %v (docker: %q, err %v)", c.image, shipped, c.ship, docker, err)
		}
		if !c.ship && (err != nil || !strings.Contains(h.out.String(), "The server pulls")) {
			t.Errorf("%s: %v\n%s", c.image, err, h.out.String())
		}
	}
}
