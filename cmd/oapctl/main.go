// Command oapctl is the rewrite's CLI (working name until cutover). With no
// arguments it shows where onboarding stands and what happens next
// (docs/architecture/onboarding.md, "First launch"). Other commands are
// under construction; see docs/architecture/rewrite-plan.md.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sshhost"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := &app{cli: filepath.Base(os.Args[0]), in: bufio.NewReader(os.Stdin), out: os.Stdout, errOut: os.Stderr,
		ssh: sshhost.Client{}, now: time.Now}
	a.terminal = isTerminal
	if a.terminal() && os.Getenv("OAP_PLAIN") == "" && os.Getenv("TERM") != "dumb" {
		a.ui = newTUI(a)
	}
	os.Exit(a.run(ctx, os.Args[1:]))
}

// app holds everything a command touches, so tests can replace it.
type app struct {
	cli         string
	in          *bufio.Reader
	out, errOut io.Writer
	ssh         sshhost.Client
	terminal    func() bool
	now         func() time.Time
	configDir   string // overrides the default, for tests
	ui          ui     // nil: plain text
	docker      func(ctx context.Context, args ...string) *exec.Cmd
}

// u is the owner's interface: the Charm one when set, else plain text.
func (a *app) u() ui {
	if a.ui != nil {
		return a.ui
	}
	return plainUI{a: a, cli: a.cli}
}

const usage = `Usage:
  %[1]s                     where onboarding stands and what happens next
  %[1]s --json              the same, as JSON for agents
  %[1]s host connect ADDR   connect a server (user@host[:port]); asks for its password once
        [--identity FILE]  use a key you already have instead of the password
        [--fingerprint FP] accept the host key without asking, if it matches FP
  %[1]s host check [ADDR]   read-only check of the server [--json]
  %[1]s host fix [ADDR]     apply the fixes the check proposes, each approved by you [--json: show them]
  %[1]s host undo FIX [ADDR] undo a fix OAP applied
  %[1]s host install [ADDR] --image IMAGE   install oapd and Caddy on the server
  %[1]s app apply FILE [ADDR]               create or update an application from its oap.yaml
  %[1]s app scale APP COMPONENT N [ADDR]    run N instances of a component
  %[1]s mcp [ADDR]                          serve OAP's tools over MCP on stdin and stdout
`

func (a *app) run(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "--json" {
		return a.frontDoor(ctx, len(args) > 0)
	}
	switch {
	case args[0] == "help" || args[0] == "-h" || args[0] == "--help":
		fmt.Fprintf(a.out, usage, a.cli)
		return 0
	case len(args) >= 2 && args[0] == "host" && args[1] == "connect":
		return a.hostConnect(ctx, args[2:])
	case len(args) >= 2 && args[0] == "host" && args[1] == "check":
		return a.hostCheck(ctx, args[2:])
	case len(args) >= 2 && args[0] == "host" && args[1] == "fix":
		return a.hostFix(ctx, args[2:])
	case len(args) >= 2 && args[0] == "host" && args[1] == "undo":
		return a.hostUndo(ctx, args[2:])
	case len(args) >= 2 && args[0] == "host" && args[1] == "install":
		return a.hostInstall(ctx, args[2:])
	case len(args) >= 2 && args[0] == "app" && args[1] == "apply":
		return a.appApply(ctx, args[2:])
	case len(args) >= 2 && args[0] == "app" && args[1] == "scale":
		return a.appScale(ctx, args[2:])
	case args[0] == "mcp":
		return a.mcpStdio(ctx, args[1:])
	}
	fmt.Fprintf(a.errOut, "%s: unknown command %q\n\n"+usage, a.cli, strings.Join(args, " "), a.cli)
	return 2
}

// frontDoor is `oap` with no arguments.
func (a *app) frontDoor(ctx context.Context, asJSON bool) int {
	j, err := a.loadJourney()
	if err != nil {
		return a.fail(err)
	}
	s := j.first()
	if asJSON || !a.terminal() {
		st := host.Plan(s, a.cli)
		a.writeJSON(st)
		return st.Exit
	}
	if s == nil {
		a.u().Welcome()
		addr := a.u().Ask("Server address", "")
		if addr == "" {
			return a.fail(errors.New("no server address given"))
		}
		if !strings.Contains(addr, "@") {
			addr = a.u().Ask("Username", "root") + "@" + addr
		}
		if code := a.connect(ctx, addr, "", ""); code != 0 {
			return code
		}
		return a.check(ctx, addr, false)
	}
	switch s.Stage {
	case host.StageNew:
		if code := a.connect(ctx, s.Address, "", ""); code != 0 {
			return code
		}
		return a.check(ctx, s.Address, false)
	case host.StageConnected:
		return a.check(ctx, s.Address, false)
	case host.StageChecked:
		if blocked, stopped := host.Blocking(s.Findings); blocked && !stopped {
			return a.fix(ctx, s, false)
		}
	}
	st := host.Plan(s, a.cli)
	a.u().Status(st)
	return st.Exit
}

func (a *app) hostConnect(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("host connect", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	identity := fs.String("identity", "", "")
	fingerprint := fs.String("fingerprint", "", "")
	addr, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if addr == "" && fs.NArg() > 0 {
		addr = fs.Arg(0)
	}
	if addr == "" {
		fmt.Fprintf(a.errOut, usage, a.cli)
		return 2
	}
	if code := a.connect(ctx, addr, *identity, *fingerprint); code != 0 {
		return code
	}
	if a.terminal() {
		return a.check(ctx, addr, false)
	}
	return 0
}

func (a *app) hostCheck(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("host check", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	asJSON := fs.Bool("json", false, "")
	addr, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if addr == "" && fs.NArg() > 0 {
		addr = fs.Arg(0)
	}
	if addr == "" {
		j, err := a.loadJourney()
		if err != nil {
			return a.fail(err)
		}
		if s := j.first(); s != nil {
			addr = s.Address
		}
	}
	if addr == "" {
		return a.fail(fmt.Errorf("no server connected yet; run %s first", a.cli))
	}
	return a.check(ctx, addr, *asJSON || !a.terminal())
}

// connect pins the host key, creates the bootstrap key and installs it.
func (a *app) connect(ctx context.Context, addr, identity, fingerprint string) int {
	t, err := a.target(addr)
	if err != nil {
		return a.fail(err)
	}
	if identity == "" && !a.terminal() {
		return a.fail(sshhost.ErrNeedsTerminal)
	}
	k, err := a.ssh.ScanHostKey(ctx, t)
	if err != nil {
		return a.fail(err)
	}
	switch {
	case fingerprint != "":
		if !strings.HasSuffix(k.Fingerprint, " "+fingerprint) && k.Fingerprint != fingerprint {
			return a.fail(fmt.Errorf("the server's host key is %s, not %s: refusing to connect", k.Fingerprint, fingerprint))
		}
	case a.terminal():
		if !a.u().Confirm(fmt.Sprintf("Host key: %s\nDoes it match your provider's console?", k.Fingerprint)) {
			return a.fail(errors.New("host key not confirmed; nothing was changed"))
		}
	default:
		return a.fail(fmt.Errorf("the host key %s needs the owner's confirmation: run this in a terminal, or pass --fingerprint", k.Fingerprint))
	}
	if err := sshhost.PinHostKey(t, k); err != nil {
		return a.fail(err)
	}
	laptop, _ := os.Hostname()
	pub, err := a.ssh.EnsureKey(ctx, t, fmt.Sprintf("oap-bootstrap %s %s", a.now().UTC().Format("2006-01-02"), laptop))
	if err != nil {
		return a.fail(err)
	}
	if identity != "" {
		err = a.ssh.InstallKeyWithIdentity(ctx, t, pub, identity)
	} else {
		a.u().Note(fmt.Sprintf("ssh will ask for the password of %s. It is used once to install OAP's key and never stored.", t.Address()))
		err = a.ssh.InstallKeyWithPassword(ctx, t, pub)
	}
	if err != nil {
		return a.fail(err)
	}
	if _, err := a.ssh.Run(ctx, t, "true"); err != nil {
		return a.fail(fmt.Errorf("the bootstrap key was installed but doesn't log in: %v", err))
	}
	if err := a.update(host.Server{Address: t.Address(), Stage: host.StageConnected}); err != nil {
		return a.fail(err)
	}
	a.u().Success(fmt.Sprintf("Connected to %s with OAP's bootstrap key. The password is no longer needed.", t.Address()))
	return 0
}

// check runs the read-only probe and records the findings.
func (a *app) check(ctx context.Context, addr string, asJSON bool) int {
	t, err := a.target(addr)
	if err != nil {
		return a.fail(err)
	}
	var out string
	probe := func(func(string)) error {
		var err error
		out, err = a.ssh.Run(ctx, t, host.Probe)
		return err
	}
	if asJSON {
		err = probe(nil)
	} else {
		err = a.u().Run("Checking the server (read-only)", probe)
	}
	if err != nil {
		return a.fail(err)
	}
	facts, err := host.ParseProbe(out)
	if err != nil {
		return a.fail(err)
	}
	s := host.Server{Address: t.Address(), Stage: host.StageChecked, Summary: host.Summary(facts), Findings: host.Check(facts)}
	prev, _ := a.server(t.Address())
	if prev != nil {
		s.Install = prev.Install
	}
	if blocked, _ := host.Blocking(s.Findings); !blocked {
		s.Stage = host.StageReady
		// A check never moves a server back past steps it has done.
		if prev != nil && later(prev.Stage, host.StageReady) {
			s.Stage = prev.Stage
		}
	}
	if err := a.update(s); err != nil {
		return a.fail(err)
	}
	st := host.Plan(&s, a.cli)
	if asJSON {
		a.writeJSON(st)
	} else {
		a.u().Status(st)
	}
	return st.Exit
}

// later reports whether stage a comes after stage b in the journey.
func later(a, b host.Stage) bool {
	order := []host.Stage{host.StageNew, host.StageConnected, host.StageChecked, host.StageReady, host.StageInstalled, host.StageOwned, host.StageHandedOff}
	ia, ib := -1, -1
	for i, s := range order {
		if s == a {
			ia = i
		}
		if s == b {
			ib = i
		}
	}
	return ia > ib
}

func (a *app) target(addr string) (sshhost.Target, error) {
	user, h, port, err := sshhost.ParseAddress(addr)
	if err != nil {
		return sshhost.Target{}, err
	}
	dirName := h
	if port != 22 {
		dirName = fmt.Sprintf("%s_%d", h, port)
	}
	dirName = strings.ReplaceAll(dirName, ":", "_")
	return sshhost.Target{User: user, Host: h, Port: port, Dir: filepath.Join(a.dir(), "hosts", dirName)}, nil
}

func (a *app) writeJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintf(a.out, "%s\n", b)
}

func (a *app) fail(err error) int {
	fmt.Fprintf(a.errOut, "%s: %v\n", a.cli, err)
	return 1
}

// splitFirst separates a leading positional argument from flags.
func splitFirst(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func isTerminal() bool {
	in, err1 := os.Stdin.Stat()
	out, err2 := os.Stdout.Stat()
	return err1 == nil && err2 == nil && in.Mode()&os.ModeCharDevice != 0 && out.Mode()&os.ModeCharDevice != 0
}
