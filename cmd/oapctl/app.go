package main

import (
	"bufio"
	"bytes"
	"context"
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
	if u := os.Getenv("OAP_AGENT_URL"); u != "" { // an agent API reached directly, as in tests
		return oapv1connect.NewAgentServiceClient(http.DefaultClient, u), func() {}, nil
	}
	t, err := a.target(s.Address)
	if err != nil {
		return nil, nil, err
	}
	sock := filepath.Join(t.Dir, "oapd.sock")
	if len(sock) > 100 { // unix socket paths are short on macOS
		sock = filepath.Join(os.TempDir(), fmt.Sprintf("oap-%d.sock", os.Getpid()))
	}
	fwd, err := a.ssh.Forward(ctx, t, sock, host.OapdSocket)
	if err != nil {
		return nil, nil, err
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
	return oapv1connect.NewAgentServiceClient(&http.Client{Transport: tr}, "http://oapd"), stop, nil
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
	if os.Getenv("OAP_AGENT_URL") == "" {
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
	a.u().Change(fmt.Sprintf("Plan for %s on %s (%d steps)", in.Application, s.Address, len(p.Steps)), because, strings.Join(lines, "\n"))
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
	applied, err := client.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: p.Id}))
	if err != nil {
		return "", a.fail(fmt.Errorf("apply: %v", err))
	}
	var outcome *oapv1.Outcome
	err = a.u().Run("Applying to "+in.Application, func(progress func(string)) error {
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
			return fmt.Errorf("%s: %s", strings.ToLower(strings.TrimPrefix(outcome.GetStatus().String(), "OUTCOME_STATUS_")), strings.Join(rs, "; "))
		}
		return nil
	})
	if err != nil {
		return applied.Msg.OperationId, a.fail(err)
	}
	return applied.Msg.OperationId, host.ExitOK
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
	return a.server(addr)
}

// mcpStdio serves MCP on stdin and stdout for an agent on this machine,
// relaying each message to oapd's MCP endpoint over the SSH forward.
func (a *app) mcpStdio(ctx context.Context, args []string) int {
	addr, _ := splitFirst(args)
	s, err := a.serverFor(addr)
	if err != nil {
		return a.fail(err)
	}
	endpoint, client := os.Getenv("OAP_AGENT_URL"), http.DefaultClient
	if endpoint == "" {
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
		if resp.StatusCode == http.StatusOK {
			fmt.Fprintf(a.out, "%s\n", bytes.TrimSpace(body))
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
