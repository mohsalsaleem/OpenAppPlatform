package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sshhost"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// docker runs the local docker CLI; tests replace it.
func (a *app) dockerCmd(ctx context.Context, args ...string) *exec.Cmd {
	if a.docker != nil {
		return a.docker(ctx, args...)
	}
	return exec.CommandContext(ctx, "docker", args...)
}

func newInstallID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// localImage returns a local image's id and architecture.
func (a *app) localImage(ctx context.Context, image string) (id, arch string, err error) {
	out, err := a.dockerCmd(ctx, "image", "inspect", "-f", "{{.Id}} {{.Architecture}}", image).Output()
	if err != nil {
		return "", "", fmt.Errorf("image %s isn't on this machine", image)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return "", "", fmt.Errorf("unexpected docker output for %s", image)
	}
	return f[0], f[1], nil
}

// shipImage copies a local image to the server unless the server already
// has the same one. No registry is involved: docker save streams into
// docker load over SSH, and the image id is checked on arrival.
func (a *app) shipImage(ctx context.Context, t sshhost.Target, image string) error {
	id, arch, err := a.localImage(ctx, image)
	if err != nil {
		return err
	}
	remote, err := a.ssh.Run(ctx, t, fmt.Sprintf("docker image inspect -f '{{.Id}}' '%s' 2>/dev/null || true; dpkg --print-architecture 2>/dev/null || uname -m", image))
	if err != nil {
		return err
	}
	lines := strings.Fields(remote)
	serverArch := ""
	if len(lines) > 0 {
		serverArch = lines[len(lines)-1]
	}
	if serverArch == "x86_64" {
		serverArch = "amd64"
	} else if serverArch == "aarch64" {
		serverArch = "arm64"
	}
	if serverArch != "" && serverArch != arch {
		return fmt.Errorf("image %s is %s but the server is %s; build it for linux/%s", image, arch, serverArch, serverArch)
	}
	if len(lines) == 2 && lines[0] == id {
		a.u().Note(fmt.Sprintf("Image %s is already on the server.", image))
		return nil
	}
	return a.u().Run("Sending image "+image, func(progress func(string)) error {
		save := a.dockerCmd(ctx, "save", image)
		var errb bytes.Buffer
		save.Stderr = &errb
		pipe, err := save.StdoutPipe()
		if err != nil {
			return err
		}
		if err := save.Start(); err != nil {
			return err
		}
		_, rerr := a.ssh.Remote(ctx, t, "docker load", pipe, progress)
		if err := save.Wait(); err != nil {
			return fmt.Errorf("docker save: %v %s", err, errb.String())
		}
		if rerr != nil {
			return rerr
		}
		got, err := a.ssh.Run(ctx, t, fmt.Sprintf("docker image inspect -f '{{.Id}}' '%s'", image))
		if err != nil || strings.TrimSpace(got) != id {
			return fmt.Errorf("image %s arrived as %q, want %s", image, strings.TrimSpace(got), id)
		}
		return nil
	})
}

func (a *app) hostInstall(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("host install", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	image := fs.String("image", os.Getenv("OAP_OAPD_IMAGE"), "the oapd image to install, on this machine")
	noTLS := fs.Bool("no-tls", false, "serve routed hosts over plain HTTP")
	asJSON := fs.Bool("json", false, "")
	addr, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if *image == "" {
		return a.fail(fmt.Errorf("name the oapd image with --image (build one with make oapd-image)"))
	}
	s, err := a.server(addr)
	if err != nil {
		return a.fail(err)
	}
	if s.Stage != host.StageReady && s.Stage != host.StageInstalled && s.Stage != host.StageOwned && s.Stage != host.StageHandedOff {
		return a.fail(fmt.Errorf("%s isn't ready for install (it is %s); run %s first", s.Address, s.Stage, a.cli))
	}
	t, err := a.target(s.Address)
	if err != nil {
		return a.fail(err)
	}
	id, _ := a.ssh.Run(ctx, t, "cat /var/lib/oap/install-id 2>/dev/null || true")
	in := host.Install{ID: strings.TrimSpace(id), Oapd: *image, Caddy: host.CaddyImage, TLS: !*noTLS}
	if in.ID == "" {
		in.ID = s.Install
	}
	if in.ID == "" {
		in.ID = newInstallID()
	}
	script, err := in.Script()
	if err != nil {
		return a.fail(err)
	}
	if err := a.shipImage(ctx, t, *image); err != nil {
		return a.fail(err)
	}
	var out string
	err = a.u().Run("Installing oapd and Caddy", func(progress func(string)) error {
		var err error
		out, err = a.ssh.RunStream(ctx, t, script, progress)
		return err
	})
	if err != nil {
		return a.fail(fmt.Errorf("install failed: %v", err))
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		a.u().Note("  " + l)
	}
	if _, err := a.ssh.Run(ctx, t, host.RecordScript("install-oap", "apply", a.now())); err != nil {
		return a.fail(fmt.Errorf("installed, but the host manifest couldn't record it: %v", err))
	}
	stage := s.Stage
	if stage == host.StageReady {
		stage = host.StageInstalled
	}
	next := *s
	next.Stage, next.Install = stage, in.ID
	if err := a.update(next); err != nil {
		return a.fail(err)
	}
	st := host.Plan(&next, a.cli)
	if *asJSON || !a.terminal() {
		a.writeJSON(st)
	} else {
		a.u().Success(fmt.Sprintf("OAP is installed on %s (installation %s).", s.Address, in.ID))
		a.u().Status(st)
	}
	return host.ExitOK
}
