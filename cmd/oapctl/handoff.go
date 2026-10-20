package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sshhost"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// Hand-off and uninstall (R3.14, docs/architecture/onboarding.md).

// bootstrapPub reads the public half of OAP's SSH key for a server.
func bootstrapPub(t sshhost.Target) (string, error) {
	b, err := os.ReadFile(t.Key() + ".pub")
	if err != nil {
		return "", fmt.Errorf("OAP's key for %s isn't on this machine: %v", t.Address(), err)
	}
	return strings.TrimSpace(string(b)), nil
}

// keyRefused reports whether an SSH error says the key was refused, as
// opposed to the server being unreachable.
func keyRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Permission denied")
}

// hostHandoff: host handoff [ADDR]. Removes OAP's SSH key from the server
// and proves it no longer works. It refuses unless the owner can still
// reach oapd with an owner key from this machine.
func (a *app) hostHandoff(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("host handoff", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	asJSON := fs.Bool("json", false, "")
	addr, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	s, err := a.server(addr)
	if err != nil {
		return a.fail(err)
	}
	if s.Stage == host.StageHandedOff {
		a.u().Note(fmt.Sprintf("%s is already handed off; OAP has no SSH access to it.", s.Address))
		return host.ExitOK
	}
	if s.Stage != host.StageOwned && s.Stage != host.StageInstalled {
		return a.fail(fmt.Errorf("%s isn't installed yet; hand-off comes after %s host install and %s owner enroll", s.Address, a.cli, a.cli))
	}
	// Without a working owner key, removing SSH would lock the owner out.
	if !a.hasOwnerKey(s) {
		return a.fail(fmt.Errorf("hand-off would leave you no way to reach %s: enroll an owner key first with %s owner enroll", s.Address, a.cli))
	}
	if _, err := a.ownerSession(ctx, s); err != nil {
		return a.fail(fmt.Errorf("hand-off would leave you no way to reach %s: %v", s.Address, err))
	}
	t, err := a.target(s.Address)
	if err != nil {
		return a.fail(err)
	}
	pub, err := bootstrapPub(t)
	if err != nil {
		return a.fail(err)
	}
	script, err := host.RemoveKeyScript(pub, a.now())
	if err != nil {
		return a.fail(err)
	}
	out, err := a.ssh.Run(ctx, t, script)
	if err != nil {
		return a.fail(fmt.Errorf("removing OAP's key failed: %v", err))
	}
	removed := strings.TrimSpace(out)
	// Prove it: the key must now be refused.
	_, err = a.ssh.Run(ctx, t, "true")
	switch {
	case err == nil:
		return a.fail(fmt.Errorf("OAP's key still works on %s after removing %s line(s) from authorized_keys; it may be authorized elsewhere (another user, or AuthorizedKeysFile)", s.Address, removed))
	case !keyRefused(err):
		return a.fail(fmt.Errorf("removed OAP's key, but couldn't confirm it's refused: %v; check with %s host handoff again", err, a.cli))
	}
	next := *s
	next.Stage = host.StageHandedOff
	if err := a.update(next); err != nil {
		return a.fail(err)
	}
	passwordLogin := false
	for _, f := range s.Findings {
		passwordLogin = passwordLogin || f.Why == "host.ssh.password_login"
	}
	if *asJSON || !a.terminal() {
		a.writeJSON(map[string]any{"handed_off": s.Address, "removed_lines": removed, "key_refused": true, "password_login_on": passwordLogin})
		return host.ExitOK
	}
	a.u().Success(fmt.Sprintf("Handed off %s: OAP's SSH key is removed and the server refuses it. You and your agents now work only through OAP; %s host connect %s --break-glass gets temporary access back.",
		s.Address, a.cli, s.Address))
	if passwordLogin {
		a.u().Note("The server still accepts SSH password logins. Once your own SSH key works, consider turning them off (PasswordAuthentication no, PermitRootLogin prohibit-password in /etc/ssh/sshd_config). OAP doesn't change this for you.")
	}
	return host.ExitOK
}

// hostUninstall: host uninstall [ADDR] [--keep-access]. Reverses what the
// host manifest shows OAP did, newest first, each step approved by the
// owner on a terminal; then removes OAP's directory and its SSH key.
func (a *app) hostUninstall(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("host uninstall", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	keep := fs.Bool("keep-access", false, "leave OAP's SSH key on the server")
	asJSON := fs.Bool("json", false, "")
	addr, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	s, err := a.server(addr)
	if err != nil {
		return a.fail(err)
	}
	t, err := a.target(s.Address)
	if err != nil {
		return a.fail(err)
	}
	manifest, err := a.ssh.Run(ctx, t, "cat "+host.ManifestPath+" 2>/dev/null || true")
	if err != nil {
		if keyRefused(err) {
			return a.fail(fmt.Errorf("OAP has no SSH access to %s (handed off); get it back with %s host connect %s --break-glass", s.Address, a.cli, s.Address))
		}
		return a.fail(err)
	}
	steps, err := host.PlanUninstall(s.Install, manifest, a.now())
	if err != nil {
		return a.fail(err)
	}
	if *asJSON || !a.terminal() {
		// Only the owner, on a terminal, approves an uninstall.
		a.writeJSON(map[string]any{"server": s.Address, "steps": steps, "remove_key": !*keep,
			"ask_owner": fmt.Sprintf("Uninstalling deletes data. Please run `%s host uninstall %s` in a terminal and approve each step.", a.cli, s.Address)})
		return host.ExitOwner
	}
	if len(steps) == 0 {
		a.u().Note(fmt.Sprintf("The host manifest shows nothing OAP did on %s that's still in place.", s.Address))
	}
	kept := 0
	for _, st := range steps {
		if st.Kept != "" {
			a.u().Note(fmt.Sprintf("Keeping %s: %s.", st.Fix, st.Kept))
			kept++
			continue
		}
		a.u().Change(st.Title, "the host manifest shows OAP did it", st.Script)
		if st.DataLoss {
			typed := a.u().Ask(fmt.Sprintf("This deletes every application on %s and its data. Type the server address to confirm", s.Address), "")
			if typed != s.Address {
				a.u().Note("Not confirmed; nothing was removed.")
				return host.ExitOwner
			}
		} else if !a.u().Confirm("Do it?") {
			a.u().Note("Skipped; the uninstall stops here so the manifest stays accurate.")
			return host.ExitOwner
		}
		err := a.u().Run(st.Title, func(progress func(string)) error {
			_, err := a.ssh.RunStream(ctx, t, st.Script, progress)
			return err
		})
		if err != nil {
			return a.fail(fmt.Errorf("%s failed: %v", st.Fix, err))
		}
	}
	if kept == 0 {
		if _, err := a.ssh.Run(ctx, t, host.ForgetScript); err != nil {
			return a.fail(err)
		}
	}
	next := *s
	next.Stage, next.Install, next.API = host.StageConnected, "", ""
	if !*keep {
		pub, err := bootstrapPub(t)
		if err != nil {
			return a.fail(err)
		}
		script, err := host.RemoveKeyOnlyScript(pub)
		if err != nil {
			return a.fail(err)
		}
		if _, err := a.ssh.Run(ctx, t, script); err != nil {
			return a.fail(fmt.Errorf("removing OAP's key failed: %v", err))
		}
		if _, err := a.ssh.Run(ctx, t, "true"); !keyRefused(err) {
			return a.fail(fmt.Errorf("OAP's key may still work on %s: %v", s.Address, err))
		}
		next.Stage = host.StageNew
	}
	if err := a.update(next); err != nil {
		return a.fail(err)
	}
	if err := os.RemoveAll(a.ownerDir(s)); err != nil {
		return a.fail(err)
	}
	msg := fmt.Sprintf("Uninstalled OAP from %s.", s.Address)
	if kept > 0 {
		msg += fmt.Sprintf(" %d change(s) without an undo stay, listed above, with the host manifest in /var/lib/oap.", kept)
	}
	if *keep {
		msg += " OAP's SSH key stays, as asked."
	} else {
		msg += " OAP's SSH key is removed."
	}
	a.u().Success(msg)
	return host.ExitOK
}
