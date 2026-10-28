package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/desiredyaml"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

// agentClient opens an SSH-forwarded socket to oapd and returns a client
// for its agent API. Until device login (R3.6) this is how the owner's
// machine reaches OAP; the forward ends with ctx.
func (a *app) agentClient(ctx context.Context, s *host.Server) (oapv1connect.AgentServiceClient, func(), error) {
	hc, base, stop, err := a.transport(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	return oapv1connect.NewAgentServiceClient(hc, base), stop, nil
}

// transport reaches oapd's API: as an agent with its saved token when
// OAP_AGENT names one, directly when OAP_AGENT_URL is set (tests), as the
// owner on the public API once an owner key is enrolled from this machine,
// or else as the owner through an SSH-forwarded socket.
func (a *app) transport(ctx context.Context, s *host.Server) (*http.Client, string, func(), error) {
	if name := os.Getenv("OAP_AGENT"); name != "" {
		cr, err := a.credentials(ctx, name)
		if err != nil {
			return nil, "", nil, err
		}
		return &http.Client{Transport: bearer{token: cr.Token, next: http.DefaultTransport}}, cr.URL, func() {}, nil
	}
	if os.Getenv("OAP_AGENT_URL") == "" && a.hasOwnerKey(s) {
		cr, err := a.ownerSession(ctx, s)
		if err != nil {
			return nil, "", nil, err
		}
		return &http.Client{Transport: bearer{token: cr.Token, next: http.DefaultTransport}}, cr.URL, func() {}, nil
	}
	return a.localTransport(ctx, s)
}

// localTransport reaches oapd's local socket as the owner: through an SSH
// forward, or directly when OAP_AGENT_URL is set (tests).
func (a *app) localTransport(ctx context.Context, s *host.Server) (*http.Client, string, func(), error) {
	if u := os.Getenv("OAP_AGENT_URL"); u != "" {
		return http.DefaultClient, u, func() {}, nil
	}
	t, err := a.target(s.Address)
	if err != nil {
		return nil, "", nil, err
	}
	sock := filepath.Join(t.Dir, "oapd.sock")
	if len(sock) > 100 { // unix socket paths are short on macOS
		sock = filepath.Join(os.TempDir(), fmt.Sprintf("oap-%d.sock", os.Getpid()))
	}
	fwd, err := a.ssh.Forward(ctx, t, sock, host.OapdSocket)
	if err != nil {
		return nil, "", nil, err
	}
	stop := func() {
		fwd.Process.Kill()
		fwd.Wait()
		os.Remove(sock)
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	return &http.Client{Transport: tr}, "http://oapd", stop, nil
}

// bearer adds an agent's token to every request.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// whyOf returns an API error's why code.
func whyOf(err error) string {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return ""
	}
	for _, d := range ce.Details() {
		if v, derr := d.Value(); derr == nil {
			if ed, ok := v.(*oapv1.ErrorDetail); ok {
				return ed.GetWhy()
			}
		}
	}
	return ""
}

func (a *app) appApply(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("app apply", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	yes := fs.Bool("yes", false, "apply without asking, when not at a terminal")
	asJSON := fs.Bool("json", false, "print the outcome as JSON")
	events := fs.Bool("events", false, "print each event as a line of JSON")
	defer func() { a.mode = human }()
	file, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	a.mode = pickMode(*asJSON, *events)
	addr := fs.Arg(0)
	if file == "" {
		return a.fail(errors.New("name the application's oap.yaml"))
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return a.fail(err)
	}
	tree, probs := desiredyaml.Parse(raw)
	if len(probs) > 0 {
		return a.fail(fmt.Errorf("%s: %s %s", file, probs[0].Why, probs[0].Message))
	}
	app, probs := spec.ParseApplication(tree)
	if len(probs) > 0 {
		return a.fail(fmt.Errorf("%s: %s at %s: %s", file, probs[0].Why, probs[0].Path, probs[0].Message))
	}
	s, err := a.serverFor(addr)
	if err != nil {
		return a.fail(err)
	}
	if os.Getenv("OAP_AGENT_URL") == "" && os.Getenv("OAP_AGENT") == "" { // the owner, over SSH
		if s.Install == "" {
			return a.fail(fmt.Errorf("OAP isn't installed on %s; run %s host install first", s.Address, a.cli))
		}
		t, err := a.target(s.Address)
		if err != nil {
			return a.fail(err)
		}
		// Images built on this machine are sent to the server; others are
		// pulled from their registry when they start.
		for _, name := range app.Names() {
			img := app.Components[name].Image
			if _, _, err := a.localImage(ctx, img); err == nil {
				if err := a.shipImage(ctx, t, img); err != nil {
					return a.fail(err)
				}
			}
		}
	}
	client, stop, err := a.agentClient(ctx, s)
	if err != nil {
		return a.fail(err)
	}
	defer stop()

	patch := spec.Clone(tree).(map[string]any)
	for _, k := range []string{"api_version", "kind", "name"} {
		delete(patch, k) // identity can't be patched; it names the application
	}
	st, err := structpb.NewStruct(patch)
	if err != nil {
		return a.fail(err)
	}
	in := &oapv1.Intent{Application: app.Name, Because: "apply " + filepath.Base(file),
		Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: st}}}
	opID, code := a.runIntent(ctx, client, s, in, *yes)
	if code == noChange {
		a.u().Success(fmt.Sprintf("%s is already as %s describes.", app.Name, file))
		return host.ExitOK
	}
	if code != host.ExitOK {
		return code
	}
	where := ""
	if app.Ingress != nil && app.Ingress.Domain != "" {
		scheme := "https"
		if !app.Ingress.HTTPS {
			scheme = "http"
		}
		where = fmt.Sprintf(" at %s://%s", scheme, app.Ingress.Domain)
	}
	a.u().Success(fmt.Sprintf("%s is live%s (operation %s).", app.Name, where, opID))
	return host.ExitOK
}

// noChange is runIntent's result when the intent changes nothing.
const noChange = -1

// runIntent proposes an intent, shows the plan, asks the owner (or takes
// yes without a terminal), applies it and follows it to its outcome. It
// returns the operation id and an exit code.
func (a *app) runIntent(ctx context.Context, client oapv1connect.AgentServiceClient, s *host.Server, in *oapv1.Intent, yes bool) (string, int) {
	res, err := client.Propose(ctx, connect.NewRequest(&oapv1.ProposeRequest{Intent: in}))
	if whyOf(err) == "intent.no_change" {
		return "", noChange
	}
	if err != nil {
		return "", a.fail(fmt.Errorf("propose: %v", err))
	}
	p := res.Msg.Proposal
	var lines []string
	for i, step := range p.Steps {
		target := step.Component
		if step.Ordinal > 0 {
			target = fmt.Sprintf("%s#%d", step.Component, step.Ordinal)
		}
		lines = append(lines, fmt.Sprintf("%2d %s %s", i, strings.ToLower(strings.TrimPrefix(step.Kind.String(), "STEP_KIND_")), target))
	}
	var why []string
	for _, r := range p.Reasons {
		why = append(why, r.Message)
	}
	verdict := strings.ToLower(strings.TrimPrefix(p.Verdict.String(), "VERDICT_"))
	because := fmt.Sprintf("%s; risk %s", verdict, strings.ToLower(strings.TrimPrefix(p.Risk.String(), "RISK_")))
	if len(why) > 0 {
		because += "; " + strings.Join(why, "; ")
	}
	target := in.Application
	if target == "" {
		target = "the server's extensions"
	}
	a.u().Change(fmt.Sprintf("Plan for %s on %s (%d steps)", target, s.Address, len(p.Steps)), because, strings.Join(lines, "\n"))
	if p.Verdict == oapv1.Verdict_VERDICT_DENIED {
		return "", a.fail(errors.New("the envelope denies this change"))
	}
	switch {
	case a.mode != human && !yes:
		a.printJSON(p)
		return "", host.ExitOwner
	case a.terminal() && a.mode == human:
		if !a.u().Confirm("Apply this plan?") {
			a.u().Note("Not applied.")
			return "", host.ExitOwner
		}
	case !yes:
		a.writeJSON(p)
		return "", host.ExitOwner
	}
	return a.applyAndFollow(ctx, client, p, target, lines)
}

// appScale scales one component: app scale APP COMPONENT N [ADDR].
func (a *app) appScale(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("app scale", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	yes := fs.Bool("yes", false, "apply without asking, when not at a terminal")
	asJSON := fs.Bool("json", false, "print the outcome as JSON")
	events := fs.Bool("events", false, "print each event as a line of JSON")
	defer func() { a.mode = human }()
	var pos []string
	for len(args) > 0 {
		if strings.HasPrefix(args[0], "-") {
			if err := fs.Parse(args); err != nil {
				return 2
			}
			args = fs.Args()
			continue
		}
		pos, args = append(pos, args[0]), args[1:]
	}
	a.mode = pickMode(*asJSON, *events)
	if len(pos) < 3 {
		return a.fail(errors.New("usage: app scale APP COMPONENT INSTANCES [ADDR]"))
	}
	n, err := strconv.Atoi(pos[2])
	if err != nil || n < 0 {
		return a.fail(fmt.Errorf("%q isn't a number of instances", pos[2]))
	}
	addr := ""
	if len(pos) > 3 {
		addr = pos[3]
	}
	s, err := a.serverFor(addr)
	if err != nil {
		return a.fail(err)
	}
	client, stop, err := a.agentClient(ctx, s)
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	in := &oapv1.Intent{Application: pos[0], Because: fmt.Sprintf("scale %s to %d", pos[1], n),
		Change: &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: pos[1], Instances: int32(n)}}}
	opID, code := a.runIntent(ctx, client, s, in, *yes)
	switch code {
	case noChange:
		a.u().Success(fmt.Sprintf("%s/%s already runs %d instances.", pos[0], pos[1], n))
		return host.ExitOK
	case host.ExitOK:
		a.u().Success(fmt.Sprintf("%s/%s runs %d instances (operation %s).", pos[0], pos[1], n, opID))
	}
	return code
}

// serverFor is the server a command acts on; with OAP_AGENT_URL set it is
// whatever that URL serves.
func (a *app) serverFor(addr string) (*host.Server, error) {
	if u := os.Getenv("OAP_AGENT_URL"); u != "" {
		return &host.Server{Address: u}, nil
	}
	if name := os.Getenv("OAP_AGENT"); name != "" { // an agent's credentials name its server
		return &host.Server{Address: "agent " + name}, nil
	}
	return a.server(addr)
}

// mcpStdio serves MCP on stdin and stdout for an agent on this machine,
// relaying each message to oapd's MCP endpoint: with the agent's token when
// OAP_AGENT names one, else as the owner over the SSH forward.
func (a *app) mcpStdio(ctx context.Context, args []string) int {
	addr, _ := splitFirst(args)
	s, err := a.serverFor(addr)
	if err != nil {
		return a.fail(err)
	}
	var client *http.Client
	var endpoint string
	if os.Getenv("OAP_AGENT") != "" || os.Getenv("OAP_AGENT_URL") != "" || a.hasOwnerKey(s) {
		hc, base, stop, err := a.transport(ctx, s)
		if err != nil {
			return a.fail(err)
		}
		defer stop()
		client, endpoint = hc, base
	} else {
		t, err := a.target(s.Address)
		if err != nil {
			return a.fail(err)
		}
		sock := filepath.Join(t.Dir, "mcp.sock")
		fwd, err := a.ssh.Forward(ctx, t, sock, host.OapdSocket)
		if err != nil {
			return a.fail(err)
		}
		defer func() { fwd.Process.Kill(); fwd.Wait(); os.Remove(sock) }()
		client = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}}}
		endpoint = "http://oapd"
	}
	sc := bufio.NewScanner(a.in)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		resp, err := client.Post(endpoint+"/mcp", "application/json", strings.NewReader(line))
		if err != nil {
			return a.fail(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// A refusal (401) carries a JSON-RPC error; pass it on so the
		// agent isn't left waiting.
		if b := bytes.TrimSpace(body); len(b) > 0 && json.Valid(b) {
			fmt.Fprintf(a.out, "%s\n", b)
		}
	}
	return 0
}

func pickMode(asJSON, events bool) mode {
	switch {
	case events:
		return eventsMode
	case asJSON:
		return jsonMode
	}
	return human
}

// intentFlags parses an app command's positional arguments and its --yes,
// --json and --events flags, in any order.
func (a *app) intentFlags(name string, args []string, extra func(*flag.FlagSet)) ([]string, bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	yes := fs.Bool("yes", false, "apply without asking, when not at a terminal")
	asJSON := fs.Bool("json", false, "print the outcome as JSON")
	events := fs.Bool("events", false, "print each event as a line of JSON")
	if extra != nil {
		extra(fs)
	}
	var pos []string
	for len(args) > 0 {
		if strings.HasPrefix(args[0], "-") {
			if err := fs.Parse(args); err != nil {
				return nil, false, err
			}
			args = fs.Args()
			continue
		}
		pos, args = append(pos, args[0]), args[1:]
	}
	a.mode = pickMode(*asJSON, *events)
	return pos, *yes, nil
}

// runAppIntent proposes, applies and follows an intent on the first server.
func (a *app) runAppIntent(ctx context.Context, in *oapv1.Intent, yes bool, done string) int {
	s, err := a.serverFor("")
	if err != nil {
		return a.fail(err)
	}
	client, stop, err := a.agentClient(ctx, s)
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	opID, code := a.runIntent(ctx, client, s, in, yes)
	switch code {
	case noChange:
		a.u().Success("Nothing to change.")
		return host.ExitOK
	case host.ExitOK:
		if a.mode == human {
			a.u().Success(fmt.Sprintf("%s (operation %s).", done, opID))
		}
	}
	return code
}

// appDeploy rolls out new images: app deploy APP COMPONENT=IMAGE... [--yes].
func (a *app) appDeploy(ctx context.Context, args []string) int {
	defer func() { a.mode = human }()
	var repo, commit *string
	pos, yes, err := a.intentFlags("app deploy", args, func(fs *flag.FlagSet) {
		repo = fs.String("repo", "", "the repository the images were built from, for change facts")
		commit = fs.String("commit", "", "the commit they were built from")
	})
	if err != nil {
		return 2
	}
	if len(pos) < 2 {
		return a.fail(errors.New("usage: app deploy APP COMPONENT=IMAGE [COMPONENT=IMAGE]... [--repo URL --commit SHA] [--yes]"))
	}
	images := map[string]string{}
	for _, kv := range pos[1:] {
		c, img, ok := strings.Cut(kv, "=")
		if !ok || c == "" || img == "" {
			return a.fail(fmt.Errorf("%q isn't COMPONENT=IMAGE", kv))
		}
		images[c] = img
	}
	d := &oapv1.Deploy{Images: images}
	if *repo != "" || *commit != "" {
		d.Source = &oapv1.Source{Repository: *repo, Commit: *commit}
	}
	in := &oapv1.Intent{Application: pos[0], Because: "deploy new images", Change: &oapv1.Intent_Deploy{Deploy: d}}
	return a.runAppIntent(ctx, in, yes, "Deployed "+pos[0])
}

// appRollback returns an application's workloads to the images of an
// earlier desired state: app rollback APP --to SPEC_HASH [--yes]. A failed
// operation prints the hash to roll back to.
func (a *app) appRollback(ctx context.Context, args []string) int {
	defer func() { a.mode = human }()
	var to *string
	pos, yes, err := a.intentFlags("app rollback", args, func(fs *flag.FlagSet) {
		to = fs.String("to", "", "the spec hash of the desired state to return to")
	})
	if err != nil {
		return 2
	}
	if len(pos) < 1 || *to == "" {
		return a.fail(errors.New("usage: app rollback APP --to SPEC_HASH [--yes]"))
	}
	in := &oapv1.Intent{Application: pos[0], Because: "roll back to " + *to,
		Change: &oapv1.Intent_Rollback{Rollback: &oapv1.Rollback{ToRelease: *to}}}
	return a.runAppIntent(ctx, in, yes, "Rolled back "+pos[0])
}

// extInstall installs or upgrades an extension:
// ext install NAME --path PATH [--sha256 HEX] --point P... [--setting K=V]... [--secret NAME]... [--memory MIB] [--yes].
// Without --sha256 the digest is computed from PATH on this machine.
func (a *app) extInstall(ctx context.Context, args []string) int {
	defer func() { a.mode = human }()
	var path, digest *string
	var memory *int
	var points, settings, secrets multi
	pos, yes, err := a.intentFlags("ext install", args, func(fs *flag.FlagSet) {
		path = fs.String("path", "", "the extension's binary on the server")
		digest = fs.String("sha256", "", "the binary's sha256 (default: computed from --path on this machine)")
		memory = fs.Int("memory", 0, "memory cap in MiB (default 64)")
		fs.Var(&points, "point", "an extension point it serves, for example notifier (repeat)")
		fs.Var(&settings, "setting", "a granted setting, NAME=VALUE (repeat)")
		fs.Var(&secrets, "secret", "a granted secret's name (repeat)")
	})
	if err != nil {
		return 2
	}
	if len(pos) != 1 || *path == "" || len(points) == 0 {
		return a.fail(errors.New("usage: ext install NAME --path PATH --point POINT [--sha256 HEX] [--setting K=V]... [--secret NAME]... [--memory MIB] [--yes]"))
	}
	if *digest == "" {
		b, err := os.ReadFile(*path)
		if err != nil {
			return a.fail(fmt.Errorf("give --sha256, or a --path readable on this machine: %v", err))
		}
		sum := sha256.Sum256(b)
		*digest = hex.EncodeToString(sum[:])
	}
	set := map[string]string{}
	for _, kv := range settings {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return a.fail(fmt.Errorf("%q isn't NAME=VALUE", kv))
		}
		set[k] = v
	}
	in := &oapv1.Intent{Because: "install extension " + pos[0], Change: &oapv1.Intent_InstallExtension{InstallExtension: &oapv1.InstallExtension{
		Name: pos[0], Path: *path, Sha256: *digest, Points: points, Settings: set, Secrets: secrets, MemoryMib: int32(*memory)}}}
	return a.runAppIntent(ctx, in, yes, "Installed extension "+pos[0])
}

// extRemove uninstalls an extension: ext remove NAME [--yes].
func (a *app) extRemove(ctx context.Context, args []string) int {
	defer func() { a.mode = human }()
	pos, yes, err := a.intentFlags("ext remove", args, nil)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		return a.fail(errors.New("usage: ext remove NAME [--yes]"))
	}
	in := &oapv1.Intent{Because: "remove extension " + pos[0], Change: &oapv1.Intent_RemoveExtension{RemoveExtension: &oapv1.RemoveExtension{Name: pos[0]}}}
	return a.runAppIntent(ctx, in, yes, "Removed extension "+pos[0])
}

// appBuild builds a component's image from source on the server:
// app build APP COMPONENT --repo URL --commit REF [--context DIR]
// [--dockerfile FILE] [--memory MIB] [--builder NAME] [--yes].
func (a *app) appBuild(ctx context.Context, args []string) int {
	defer func() { a.mode = human }()
	var repo, commit, context_, dockerfile, builder *string
	var memory *int
	pos, yes, err := a.intentFlags("app build", args, func(fs *flag.FlagSet) {
		repo = fs.String("repo", "", "the git repository URL")
		commit = fs.String("commit", "", "the commit, branch or tag to build")
		context_ = fs.String("context", "", "the build context in the repository (default .)")
		dockerfile = fs.String("dockerfile", "", "the Dockerfile in the context (default Dockerfile)")
		memory = fs.Int("memory", 0, "memory the build may use, in MiB (default 512)")
		builder = fs.String("builder", "", "the builder extension, when several are installed")
	})
	if err != nil {
		return 2
	}
	if len(pos) != 2 || *repo == "" || *commit == "" {
		return a.fail(errors.New("usage: app build APP COMPONENT --repo URL --commit REF [--context DIR] [--dockerfile FILE] [--memory MIB] [--yes]"))
	}
	in := &oapv1.Intent{Application: pos[0], Because: fmt.Sprintf("build %s at %s", pos[1], *commit),
		Change: &oapv1.Intent_Build{Build: &oapv1.Build{Component: pos[1], Repository: *repo, Commit: *commit,
			Context: *context_, Dockerfile: *dockerfile, MemoryMib: int32(*memory), Builder: *builder}}}
	code := a.runAppIntent(ctx, in, yes, "Built "+pos[0]+"/"+pos[1])
	if code == 0 && a.mode == human {
		if img := a.lastOutcome.GetImages()[pos[1]]; img != "" {
			fmt.Fprintf(a.out, "Image: %s\nDeploy it with: %s app deploy %s %s=%s\n", img, a.cli, pos[0], pos[1], img)
		}
	}
	return code
}

// applyAndFollow applies a proposal and follows its operation to the
// outcome. lines are the plan's steps as shown, for progress.
func (a *app) applyAndFollow(ctx context.Context, client oapv1connect.AgentServiceClient, p *oapv1.Proposal, target string, lines []string) (string, int) {
	applied, err := client.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: p.Id}))
	if err != nil {
		if p.Verdict == oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
			return "", a.fail(fmt.Errorf("apply: %v\nAsk the owner: %s ask --kind approval --proposal %s %q --wait\nThen apply it: %s apply %s",
				err, a.cli, p.Id, "Approve "+summaryOf(p.GetIntent())+"?", a.cli, p.Id))
		}
		return "", a.fail(fmt.Errorf("apply: %v", err))
	}
	var outcome *oapv1.Outcome
	err = a.u().Run("Applying to "+target, func(progress func(string)) error {
		stream, err := client.Await(ctx, connect.NewRequest(&oapv1.AwaitRequest{OperationId: applied.Msg.OperationId, TimeoutSeconds: 900}))
		if err != nil {
			return err
		}
		for stream.Receive() {
			e := stream.Msg().Event
			if a.mode == eventsMode {
				a.printJSON(e)
			}
			if a.mode == jsonMode && e.GetOutcome() != nil {
				a.printJSON(e)
			}
			if pr := e.GetOperationProgress(); pr != nil && int(pr.StepIndex) < len(lines) {
				progress(fmt.Sprintf("%s: %s", strings.TrimSpace(lines[pr.StepIndex]), strings.ToLower(strings.TrimPrefix(pr.State.String(), "STEP_STATE_"))))
			}
			if o := e.GetOutcome(); o != nil {
				outcome = o
				a.lastOutcome = o
			}
		}
		if err := stream.Err(); err != nil {
			return err
		}
		if outcome.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
			var rs []string
			for _, r := range outcome.GetReasons() {
				rs = append(rs, r.Message)
			}
			msg := fmt.Sprintf("%s: %s", strings.ToLower(strings.TrimPrefix(outcome.GetStatus().String(), "OUTCOME_STATUS_")), strings.Join(rs, "; "))
			if rb := p.GetUndo().GetRollback(); rb != nil {
				msg += fmt.Sprintf("\nRoll back with: %s app rollback %s --to %s", a.cli, p.GetIntent().GetApplication(), rb.ToRelease)
			}
			return errors.New(msg)
		}
		return nil
	})
	if err != nil {
		return applied.Msg.OperationId, a.fail(err)
	}
	return applied.Msg.OperationId, host.ExitOK
}

func summaryOf(in *oapv1.Intent) string {
	if b := strings.TrimSpace(in.GetBecause()); b != "" {
		return b
	}
	return in.GetApplication()
}

// applyCmd applies a proposal by its id, for example one the owner
// approved through an ask: apply PROPOSAL_ID [--json|--events].
func (a *app) applyCmd(ctx context.Context, args []string) int {
	defer func() { a.mode = human }()
	pos, _, err := a.intentFlags("apply", args, nil)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		return a.fail(errors.New("usage: apply PROPOSAL_ID"))
	}
	s, err := a.serverFor("")
	if err != nil {
		return a.fail(err)
	}
	client, stop, err := a.agentClient(ctx, s)
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	p := &oapv1.Proposal{Id: pos[0]}
	opID, code := a.applyAndFollow(ctx, client, p, "proposal "+pos[0], nil)
	if code == host.ExitOK && a.mode == human {
		a.u().Success(fmt.Sprintf("Applied %s (operation %s).", pos[0], opID))
	}
	return code
}
