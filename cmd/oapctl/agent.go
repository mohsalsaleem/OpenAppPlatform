package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/agentapi"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/wire"
	"google.golang.org/protobuf/proto"
)

// Output modes for agent API commands (R3.5): human text, --json (the
// response in OAP's wire JSON), --events (one event per line, as JSON).
type mode int

const (
	human mode = iota
	jsonMode
	eventsMode
)

// agentFlags parses the shared flags and positional arguments.
func agentFlags(name string, args []string, a *app) (mode, []string, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	j := fs.Bool("json", false, "print the result as JSON")
	ev := fs.Bool("events", false, "print each event as a line of JSON")
	var pos []string
	for len(args) > 0 {
		if strings.HasPrefix(args[0], "-") {
			if err := fs.Parse(args); err != nil {
				return 0, nil, nil, err
			}
			args = fs.Args()
			continue
		}
		pos, args = append(pos, args[0]), args[1:]
	}
	m := human
	switch {
	case *ev:
		m = eventsMode
	case *j:
		m = jsonMode
	}
	return m, pos, fs, nil
}

func (a *app) printJSON(m proto.Message) {
	b, err := wire.Marshal(m)
	if err != nil {
		a.fail(err)
		return
	}
	fmt.Fprintf(a.out, "%s\n", b)
}

// connectClient opens the agent API for the first server, or OAP_AGENT_URL.
func (a *app) connectClient(ctx context.Context, addr string) (oapv1connect.AgentServiceClient, func(), error) {
	s, err := a.serverFor(addr)
	if err != nil {
		return nil, nil, err
	}
	return a.agentClient(ctx, s)
}

func status(h oapv1.HealthStatus) string {
	return strings.ToLower(strings.TrimPrefix(h.String(), "HEALTH_STATUS_"))
}

// observeCmd: observe [APP] [--json].
func (a *app) observeCmd(ctx context.Context, args []string) int {
	m, pos, _, err := agentFlags("observe", args, a)
	if err != nil {
		return 2
	}
	req := &oapv1.ObserveRequest{Detail: true}
	if len(pos) > 0 {
		req.Application = pos[0]
	}
	c, stop, err := a.connectClient(ctx, "")
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	res, err := c.Observe(ctx, connect.NewRequest(req))
	if err != nil {
		return a.fail(err)
	}
	if m != human {
		a.printJSON(res.Msg)
		return 0
	}
	w := res.Msg.World
	fmt.Fprintf(a.out, "revision %d · host %s\n", w.GetRevision(), w.GetHost().GetName())
	if len(w.Applications) == 0 {
		fmt.Fprintln(a.out, "No applications yet.")
	}
	for _, app := range w.Applications {
		fmt.Fprintf(a.out, "\n%s  %s\n", app.Name, status(app.GetHealth().GetStatus()))
		for _, comp := range app.Components {
			state := "converged"
			if !comp.Converged {
				state = "NOT converged"
			}
			d, act := comp.GetDesired(), comp.GetActual()
			fmt.Fprintf(a.out, "  %-12s %-13s %d/%d running  release %s  %s\n", comp.Name, state, act.GetInstances(), d.GetInstances(),
				d.GetRelease(), status(comp.GetHealth().GetStatus()))
			for _, r := range d.GetRoutes() {
				fmt.Fprintf(a.out, "  %-12s route %s%s → :%d\n", "", r.GetHost(), r.GetPathPrefix(), r.GetPort())
			}
		}
	}
	return 0
}

// explainCmd: explain APP [COMPONENT] [--json].
func (a *app) explainCmd(ctx context.Context, args []string) int {
	m, pos, _, err := agentFlags("explain", args, a)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		return a.fail(errors.New("usage: explain APP [COMPONENT]"))
	}
	req := &oapv1.ExplainRequest{Application: pos[0]}
	if len(pos) > 1 {
		req.Component = pos[1]
	}
	c, stop, err := a.connectClient(ctx, "")
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	res, err := c.Explain(ctx, connect.NewRequest(req))
	if err != nil {
		return a.fail(err)
	}
	if m != human {
		a.printJSON(res.Msg)
		return 0
	}
	if len(res.Msg.Gaps) == 0 {
		fmt.Fprintf(a.out, "%s is as desired (revision %d).\n", pos[0], res.Msg.Revision)
		return 0
	}
	fmt.Fprintf(a.out, "%s differs from desired (revision %d):\n", pos[0], res.Msg.Revision)
	for _, g := range res.Msg.Gaps {
		fmt.Fprintf(a.out, "  gap   %s\n", g)
	}
	for _, cause := range res.Msg.Causes {
		fmt.Fprintf(a.out, "  cause %s (%d%%): %s\n", cause.Why, cause.Confidence, cause.Summary)
	}
	return 0
}

// eventLine is an event in human form.
func eventLine(e *oapv1.Event) string {
	switch k := e.Kind.(type) {
	case *oapv1.Event_JournalEntry:
		je := k.JournalEntry
		kind := strings.ToLower(strings.TrimPrefix(je.GetKind().String(), "ENTRY_KIND_"))
		return fmt.Sprintf("%5d  %-8s %s", e.Revision, kind, je.GetSummary())
	case *oapv1.Event_OperationProgress:
		p := k.OperationProgress
		step := strings.ToLower(strings.TrimPrefix(p.GetStep().GetKind().String(), "STEP_KIND_"))
		target := p.GetStep().GetComponent()
		if o := p.GetStep().GetOrdinal(); o > 0 {
			target = fmt.Sprintf("%s#%d", target, o)
		}
		return fmt.Sprintf("%5d  step %-3d %-22s %-12s %s", e.Revision, p.StepIndex, step, target,
			strings.ToLower(strings.TrimPrefix(p.GetState().String(), "STEP_STATE_")))
	case *oapv1.Event_Outcome:
		return fmt.Sprintf("%5d  outcome  %s %s", e.Revision, k.Outcome.GetOperationId(),
			strings.ToLower(strings.TrimPrefix(k.Outcome.GetStatus().String(), "OUTCOME_STATUS_")))
	}
	return fmt.Sprintf("%5d  event", e.Revision)
}

// watchCmd: watch [APP] [--since N] [--limit N] [--events|--json].
func (a *app) watchCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	since := fs.Uint64("since", 0, "events after this revision (0: from now)")
	limit := fs.Int("limit", 0, "stop after this many events (0: until interrupted)")
	ev := fs.Bool("events", false, "")
	j := fs.Bool("json", false, "")
	app, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if app == "" && fs.NArg() > 0 {
		app = fs.Arg(0)
	}
	c, stop, err := a.connectClient(ctx, "")
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	stream, err := c.Watch(ctx, connect.NewRequest(&oapv1.WatchRequest{SinceRevision: *since, Application: app}))
	if err != nil {
		return a.fail(err)
	}
	defer stream.Close()
	n := 0
	for stream.Receive() {
		e := stream.Msg().Event
		if *ev || *j {
			a.printJSON(e)
		} else {
			fmt.Fprintln(a.out, eventLine(e))
		}
		if n++; *limit > 0 && n >= *limit {
			return 0
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		return a.fail(err)
	}
	return 0
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// askCmd: ask QUESTION [--kind K] [--option O]... [--proposal ID]
// [--secret-ref R] [--expires D] [--wait] [--json].
func (a *app) askCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	kind := fs.String("kind", "blocker", "approval, secret, decision or blocker")
	var options multi
	fs.Var(&options, "option", "an option for a decision (repeat)")
	proposal := fs.String("proposal", "", "the proposal an approval is for")
	secretRef := fs.String("secret-ref", "", "the secret reference a secret ask is for")
	expires := fs.Duration("expires", time.Hour, "how long the owner has to answer")
	wait := fs.Bool("wait", false, "wait for the answer or the expiry")
	asJSON := fs.Bool("json", false, "")
	question, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	k, ok := map[string]oapv1.AskKind{"approval": oapv1.AskKind_ASK_KIND_APPROVAL, "secret": oapv1.AskKind_ASK_KIND_SECRET,
		"decision": oapv1.AskKind_ASK_KIND_DECISION, "blocker": oapv1.AskKind_ASK_KIND_BLOCKER}[*kind]
	if !ok {
		return a.fail(fmt.Errorf("kind is approval, secret, decision or blocker, not %q", *kind))
	}
	c, stop, err := a.connectClient(ctx, "")
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	obs, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{}))
	if err != nil {
		return a.fail(err)
	}
	since := obs.Msg.World.GetRevision()
	res, err := c.Ask(ctx, connect.NewRequest(&oapv1.AskRequest{Kind: k, Question: question, Options: options,
		ProposalId: *proposal, SecretRef: *secretRef, ExpiresInSeconds: int32(expires.Seconds())}))
	if err != nil {
		return a.fail(err)
	}
	id := res.Msg.AskId
	if !*wait {
		if *asJSON {
			a.printJSON(res.Msg)
		} else {
			fmt.Fprintf(a.out, "Asked the owner (%s). The answer arrives through watch; wait for it with --wait.\n", id)
		}
		return 0
	}
	if !*asJSON {
		fmt.Fprintf(a.out, "Asked the owner (%s); waiting for the answer…\n", id)
	}
	stream, err := c.Watch(ctx, connect.NewRequest(&oapv1.WatchRequest{SinceRevision: since}))
	if err != nil {
		return a.fail(err)
	}
	defer stream.Close()
	for stream.Receive() {
		ans := stream.Msg().Event.GetAskAnswered()
		if ans.GetAskId() != id {
			continue
		}
		if *asJSON {
			a.printJSON(ans)
		} else if ans.Expired {
			fmt.Fprintln(a.out, "The ask expired without an answer.")
		} else {
			fmt.Fprintf(a.out, "The owner answered: %s\n", ans.Answer)
		}
		if ans.Expired {
			return 4
		}
		return 0
	}
	if err := stream.Err(); err != nil {
		return a.fail(err)
	}
	return a.fail(errors.New("the stream ended before an answer"))
}

// briefingCmd: briefing [APP] — the generated briefing for this caller.
func (a *app) briefingCmd(ctx context.Context, args []string) int {
	app, _ := splitFirst(args)
	c, stop, err := a.connectClient(ctx, "")
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	res, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Application: app, Briefing: true}))
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprint(a.out, res.Msg.Briefing)
	return 0
}

// skillCmd: skill [NAME|schema] — a guide for agents, from the server, so
// it matches the server's version. NAME defaults to author-app.
func (a *app) skillCmd(ctx context.Context, args []string) int {
	name, _ := splitFirst(args)
	if name == "" {
		name = "author-app"
	}
	s, err := a.serverFor("")
	if err != nil {
		return a.fail(err)
	}
	hc, base, stop, err := a.transport(ctx, s)
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	path := agentapi.SkillsPath + name + ".md"
	if name == "schema" {
		path = agentapi.SchemaPath
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return a.fail(err)
	}
	res, err := hc.Do(req)
	if err != nil {
		return a.fail(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return a.fail(fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(body))))
	}
	a.out.Write(body)
	return 0
}
