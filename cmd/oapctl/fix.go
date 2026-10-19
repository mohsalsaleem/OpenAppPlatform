package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sshhost"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// fixProposal is what `host fix --json` reports for an agent to relay.
type fixProposal struct {
	Server   string       `json:"server"`
	Batches  []host.Batch `json:"batches"`
	Missing  []string     `json:"missing,omitempty"`
	AskOwner string       `json:"ask_owner"`
}

// server finds a server in the journey by address, or the first one.
func (a *app) server(addr string) (*host.Server, error) {
	j, err := a.loadJourney()
	if err != nil {
		return nil, err
	}
	for i := range j.Servers {
		if addr == "" || j.Servers[i].Address == addr {
			return &j.Servers[i], nil
		}
	}
	if addr == "" {
		return nil, fmt.Errorf("no server connected yet; run %s first", a.cli)
	}
	return nil, fmt.Errorf("%s isn't connected; run %s host connect %s first", addr, a.cli, addr)
}

func (a *app) hostFix(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("host fix", flag.ContinueOnError)
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
	if s.Stage == host.StageNew || s.Stage == host.StageConnected {
		return a.fail(fmt.Errorf("check %s first: run %s host check %s", s.Address, a.cli, s.Address))
	}
	return a.fix(ctx, s, *asJSON || !a.terminal())
}

// fix proposes the check's fixes and applies those the owner approves on
// the terminal, then checks again. Without a terminal it only reports the
// proposal: an agent can propose fixes but never approve them.
func (a *app) fix(ctx context.Context, s *host.Server, proposeOnly bool) int {
	batches, missing := host.Propose(s.Findings)
	if proposeOnly {
		a.writeJSON(fixProposal{Server: s.Address, Batches: batches, Missing: missing,
			AskOwner: fmt.Sprintf("Please run `%s host fix` in a terminal on this machine and approve the fixes it shows. Tell me when it's done.", a.cli)})
		if len(batches) == 0 {
			return host.ExitOK
		}
		return host.ExitOwner
	}
	if len(batches) == 0 {
		fmt.Fprintln(a.out, "No fixes to apply.")
		return a.check(ctx, s.Address, false)
	}
	t, err := a.target(s.Address)
	if err != nil {
		return a.fail(err)
	}
	n := countFixes(batches)
	changes := "1 change"
	if n != 1 {
		changes = fmt.Sprintf("%d changes", n)
	}
	fmt.Fprintf(a.out, "%s needs %s. Each runs only if you approve it.\n", s.Address, changes)
	for _, b := range batches {
		fmt.Fprintln(a.out)
		for _, f := range b.Fixes {
			undo := "can't be undone"
			if f.CanUndo {
				undo = fmt.Sprintf("undo with `%s host undo %s`", a.cli, f.ID)
			}
			fmt.Fprintf(a.out, "%s (%s; %s)\n  Because: %s.\n  Commands:\n%s\n", f.Summary, f.Tier, undo, f.Observed, indent(f.Script, "    "))
		}
		prompt := "Apply this? [y/N] "
		if len(b.Fixes) > 1 {
			prompt = fmt.Sprintf("Apply these %d routine fixes? [y/N] ", len(b.Fixes))
		}
		if ans := a.ask(prompt, "n"); !strings.EqualFold(ans, "y") && !strings.EqualFold(ans, "yes") {
			fmt.Fprintln(a.out, "Not applied.")
			continue
		}
		for _, f := range b.Fixes {
			fmt.Fprintf(a.out, "Applying %s…\n", f.ID)
			if _, err := a.ssh.Run(ctx, t, f.Script); err != nil {
				return a.fail(fmt.Errorf("fix %s failed: %v", f.ID, err))
			}
			if _, err := a.ssh.Run(ctx, t, host.RecordScript(f.ID, "apply", a.now())); err != nil {
				return a.fail(fmt.Errorf("fix %s was applied but couldn't be recorded in the host manifest: %v", f.ID, err))
			}
			fmt.Fprintf(a.out, "✓ %s applied and recorded in %s\n", f.ID, host.ManifestPath)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(a.out, "No automatic fix yet for: %s\n", strings.Join(missing, ", "))
	}
	fmt.Fprintln(a.out)
	return a.check(ctx, s.Address, false)
}

func (a *app) hostUndo(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.fail(errors.New("name the fix to undo, for example: host undo swap-file"))
	}
	id := args[0]
	addr := ""
	if len(args) > 1 {
		addr = args[1]
	}
	sc, ok := host.Scripts[id]
	if !ok || sc.Undo == "" {
		return a.fail(fmt.Errorf("%s can't be undone", id))
	}
	if !a.terminal() {
		return a.fail(sshhost.ErrNeedsTerminal)
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
		return a.fail(err)
	}
	applied := false
	for _, f := range host.Applied(manifest) {
		applied = applied || f == id
	}
	if !applied {
		return a.fail(fmt.Errorf("the host manifest doesn't show %s as applied by OAP; nothing to undo", id))
	}
	fmt.Fprintf(a.out, "Undo %s on %s.\n  Commands:\n%s\n", id, s.Address, indent(sc.Undo, "    "))
	if ans := a.ask("Undo it? [y/N] ", "n"); !strings.EqualFold(ans, "y") && !strings.EqualFold(ans, "yes") {
		fmt.Fprintln(a.out, "Not undone.")
		return host.ExitOwner
	}
	if _, err := a.ssh.Run(ctx, t, sc.Undo); err != nil {
		return a.fail(fmt.Errorf("undoing %s failed: %v", id, err))
	}
	if _, err := a.ssh.Run(ctx, t, host.RecordScript(id, "undo", a.now())); err != nil {
		return a.fail(fmt.Errorf("%s was undone but couldn't be recorded: %v", id, err))
	}
	fmt.Fprintf(a.out, "✓ %s undone\n", id)
	return a.check(ctx, s.Address, false)
}

func countFixes(bs []host.Batch) int {
	n := 0
	for _, b := range bs {
		n += len(b.Fixes)
	}
	return n
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
