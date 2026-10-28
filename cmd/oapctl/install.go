package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"net"
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

// contentFormat identifies an image by its content: its layers' digests
// and how it starts. Image ids differ between Docker's classic and
// containerd image stores for the same image; these don't.
const contentFormat = "{{json .RootFS.Layers}}{{json .Config.Entrypoint}}{{json .Config.Cmd}}"

// localImage returns a local image's content identity and architecture.
func (a *app) localImage(ctx context.Context, image string) (id, arch string, err error) {
	out, err := a.dockerCmd(ctx, "image", "inspect", "-f", contentFormat+" {{.Architecture}}", image).Output()
	if err != nil {
		return "", "", fmt.Errorf("image %s isn't on this machine", image)
	}
	line := strings.TrimSpace(string(out))
	i := strings.LastIndex(line, " ")
	if i < 0 {
		return "", "", fmt.Errorf("unexpected docker output for %s", image)
	}
	return line[:i], line[i+1:], nil
}

func (a *app) remoteImage(ctx context.Context, t sshhost.Target, image string) (string, error) {
	out, err := a.ssh.Run(ctx, t, fmt.Sprintf("docker image inspect -f '%s' '%s' 2>/dev/null || true", contentFormat, image))
	return strings.TrimSpace(out), err
}

// shipImage copies a local image to the server unless the server already
// has the same one. No registry is involved: docker save streams into
// docker load over SSH, and the image id is checked on arrival.
func (a *app) shipImage(ctx context.Context, t sshhost.Target, image string) error {
	// A registry reference pinned by digest (a released oapd): the server
	// pulls it and picks its own architecture, even if this machine has a
	// copy, which is this machine's architecture.
	registry := false
	if first, _, ok := strings.Cut(image, "/"); ok && strings.ContainsAny(first, ".:") {
		registry = true
	}
	if registry && strings.Contains(image, "@sha256:") {
		a.u().Note(fmt.Sprintf("The server pulls %s from its registry.", image))
		return nil
	}
	id, arch, err := a.localImage(ctx, image)
	if err != nil {
		// Not built here: fine if the server already has it (built there).
		if cur, rerr := a.remoteImage(ctx, t, image); rerr == nil && cur != "" {
			a.u().Note(fmt.Sprintf("Image %s is on the server (not on this machine); using it.", image))
			return nil
		}
		// Any other registry reference: the server pulls it.
		if registry {
			a.u().Note(fmt.Sprintf("The server pulls %s from its registry.", image))
			return nil
		}
		return err
	}
	archOut, err := a.ssh.Run(ctx, t, "dpkg --print-architecture 2>/dev/null || uname -m")
	if err != nil {
		return err
	}
	serverArch := strings.TrimSpace(archOut)
	if serverArch == "x86_64" {
		serverArch = "amd64"
	} else if serverArch == "aarch64" {
		serverArch = "arm64"
	}
	if serverArch != "" && serverArch != arch {
		return fmt.Errorf("image %s is %s but the server is %s; build it for linux/%s", image, arch, serverArch, serverArch)
	}
	if cur, err := a.remoteImage(ctx, t, image); err != nil {
		return err
	} else if cur == id {
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
		load, err := host.LoadImageCommand(image)
		if err != nil {
			return err
		}
		_, rerr := a.ssh.Remote(ctx, t, load, pipe, progress)
		if err := save.Wait(); err != nil {
			return fmt.Errorf("docker save: %v %s", err, errb.String())
		}
		if rerr != nil {
			return rerr
		}
		got, err := a.remoteImage(ctx, t, image)
		if err != nil || got != id {
			return fmt.Errorf("image %s arrived with different content: %q, want %q", image, got, id)
		}
		return nil
	})
}

func (a *app) hostInstall(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("host install", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	defImage := os.Getenv("OAP_OAPD_IMAGE")
	if defImage == "" {
		defImage = OapdImage
	}
	image := fs.String("image", defImage, "the oapd image to install: built on this machine, or pulled by the server from its registry")
	noTLS := fs.Bool("no-tls", false, "serve routed hosts over plain HTTP")
	apiHost := fs.String("api-host", "", "public host name for the agent API (default oap.<ip>.sslip.io; \"none\" keeps it private)")
	asJSON := fs.Bool("json", false, "")
	addr, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if *image == "" {
		return a.fail(fmt.Errorf("this oapctl is a development build and pins no oapd image; name one with --image (build one with make oapd-image)"))
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
	in := host.Install{ID: strings.TrimSpace(id), Oapd: *image, Caddy: host.CaddyImage, TLS: !*noTLS, APIHost: *apiHost}
	switch {
	case in.APIHost == "none":
		in.APIHost = ""
	case in.APIHost == "" && net.ParseIP(t.Host).To4() != nil:
		in.APIHost = "oap." + strings.ReplaceAll(t.Host, ".", "-") + ".sslip.io"
	}
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
	next.Stage, next.Install, next.API = stage, in.ID, ""
	if in.APIHost != "" {
		next.API = "http://" + in.APIHost
		if in.TLS {
			next.API = "https://" + in.APIHost
		}
	}
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
