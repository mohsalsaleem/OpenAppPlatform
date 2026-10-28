package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var ctx = context.Background()

const envelopeJSON = `{"agents": {
  "owner": {"scope": {"applications": ["*"]}, "may": ["scale", "deploy", "rollback", "configure", "add-component", "remove-component", "spec-patch"]},
  "claude": {"scope": {"applications": ["site"]}, "may": ["scale", "deploy"]}},
 "invariants": [{"snapshot_before_volume_change": true}]}`

const siteJSON = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "site", "environment": "production",
  "ingress": {"domain": "site.example.com", "routes": {"/": {"component": "web"}}},
  "components": {"web": {"kind": "web", "image": "reg/site:1", "port": 8080}}}`

type rig struct {
	svc     *Service
	runtime *sim.Runtime
	edge    *sim.Edge
	journal *sim.Journal
	desired *sim.Desired
	clock   *sim.Clock
}

func newRig(t *testing.T) *rig {
	t.Helper()
	clock := sim.NewClock(time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC))
	r := &rig{runtime: sim.NewRuntime(clock, model.HostObservation{Name: "sim", MemoryTotalMiB: 950, CPUMillisTotal: 1000}),
		edge: sim.NewEdge(), journal: &sim.Journal{}, desired: sim.NewDesired(), clock: clock}
	env, _ := spec.FromJSON([]byte(envelopeJSON))
	r.desired.SetEnvelope(ctx, env, "owner", "test envelope")
	r.svc = r.service()
	if err := r.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) service() *Service {
	return &Service{Desired: r.desired, Journal: r.journal, Clock: r.clock,
		Observer: &engine.Observer{Runtime: r.runtime, Clock: r.clock},
		Executor: &engine.Executor{Runtime: r.runtime, Edge: r.edge, Journal: r.journal, Desired: r.desired, Clock: r.clock}}
}

func patch(t *testing.T, app, js string) *oapv1.Intent {
	t.Helper()
	tree, probs := spec.FromJSON([]byte(js))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	m := tree.(map[string]any)
	for _, k := range []string{"api_version", "kind", "name"} {
		delete(m, k) // identity comes from the base; patches can't touch it
	}
	st, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return &oapv1.Intent{Application: app, Because: "create " + app, Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: st}}}
}

func scale(app string, n int32) *oapv1.Intent {
	return &oapv1.Intent{Application: app, Change: &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "web", Instances: n}}}
}

func code(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// await collects an operation's events until its outcome.
func (r *rig) await(t *testing.T, op string) []*oapv1.Event {
	t.Helper()
	var evs []*oapv1.Event
	if err := r.svc.Await(ctx, Owner, &oapv1.AwaitRequest{OperationId: op, TimeoutSeconds: 600}, func(e *oapv1.Event) error {
		evs = append(evs, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return evs
}

func (r *rig) create(t *testing.T) string {
	t.Helper()
	p, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "site", siteJSON)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	evs := r.await(t, a.OperationId)
	if o := evs[len(evs)-1].GetOutcome(); o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("create outcome %v", o)
	}
	return a.OperationId
}

// TestCreateAndAwait: a new application is a spec_patch from nothing; the
// owner applies it; Await streams every step in order and ends achieved.
func TestCreateAndAwait(t *testing.T) {
	r := newRig(t)
	p, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "site", siteJSON)})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Proposal.Steps) == 0 || p.Proposal.Id == "" || p.Proposal.Agent != "owner" {
		t.Fatalf("proposal %+v", p.Proposal)
	}
	a, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	evs := r.await(t, a.OperationId)
	last := -1
	succeeded := 0
	for _, e := range evs[:len(evs)-1] {
		pr := e.GetOperationProgress()
		if pr == nil || pr.Step == nil || int(pr.StepIndex) < last {
			t.Fatalf("event %v", e)
		}
		last = int(pr.StepIndex)
		if pr.State == oapv1.StepState_STEP_STATE_SUCCEEDED {
			succeeded++
		}
	}
	if succeeded != len(p.Proposal.Steps) || evs[len(evs)-1].GetOutcome().GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("%d of %d steps succeeded; outcome %v", succeeded, len(p.Proposal.Steps), evs[len(evs)-1])
	}
	if len(r.runtime.Running()) != 1 {
		t.Fatalf("running %v", r.runtime.Running())
	}
	again, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil || again.OperationId != a.OperationId {
		t.Fatalf("second apply: %v %v", again, err)
	}
	if len(r.runtime.Running()) != 1 {
		t.Fatal("applying twice ran the operation twice")
	}
}

func TestObserve(t *testing.T) {
	r := newRig(t)
	if w, err := r.svc.Observe(ctx, Owner, &oapv1.ObserveRequest{}); err != nil || len(w.World.Applications) != 0 {
		t.Fatalf("empty world: %v %v", w, err)
	}
	r.create(t)
	r.svc.Observer.Resync(ctx)
	w, err := r.svc.Observe(ctx, Owner, &oapv1.ObserveRequest{Application: "site", Detail: true})
	if err != nil || len(w.World.Applications) != 1 {
		t.Fatal(w, err)
	}
	web := w.World.Applications[0].Components[0]
	if !web.Converged || len(web.Instances) != 1 || w.World.Revision == 0 || w.World.Host.GetName() != "sim" {
		t.Fatalf("web %v", web)
	}
	if w, _ := r.svc.Observe(ctx, Owner, &oapv1.ObserveRequest{Application: "site"}); len(w.World.Applications[0].Components[0].Instances) != 0 {
		t.Fatal("compact observe lists instances")
	}
	if _, err := r.svc.Observe(ctx, Owner, &oapv1.ObserveRequest{Application: "nope"}); code(err) != NotFound {
		t.Fatalf("unknown application: %v", err)
	}
}

func TestProposeRefusals(t *testing.T) {
	r := newRig(t)
	if _, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 2)}); code(err) != NotFound {
		t.Fatalf("scaling a missing application: %v", err)
	}
	r.create(t)
	head, _ := r.journal.Head(ctx)
	_, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 2), ExpectedRevision: head - 1})
	var e *Error
	if !errors.As(err, &e) || e.Code != FailedPrecondition || e.Detail.GetRemedy().GetRefresh() != true {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 0)}); code(err) != InvalidArgument {
		t.Fatalf("invalid intent: %v", err)
	}
}

// TestApplyFollowsTheVerdict: an agent applies what the envelope allows
// it; the owner approves what needs approval; an agent can't.
func TestApplyFollowsTheVerdict(t *testing.T) {
	r := newRig(t)
	r.create(t)
	claude := Caller{Agent: "claude", Apps: []string{"site"}}
	p, err := r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: scale("site", 2)})
	if err != nil {
		t.Fatal(err)
	}
	if p.Proposal.Verdict != oapv1.Verdict_VERDICT_ALLOWED {
		t.Fatalf("scale within the envelope: %v %v", p.Proposal.Verdict, p.Proposal.Reasons)
	}
	a, err := r.svc.Apply(ctx, claude, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	r.await(t, a.OperationId)
	if len(r.runtime.Running()) != 2 {
		t.Fatalf("running %d", len(r.runtime.Running()))
	}
	patchProp, err := r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: patch(t, "site", `{"components": {"web": {"port": 9090}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if patchProp.Proposal.Verdict == oapv1.Verdict_VERDICT_ALLOWED {
		t.Fatal("a spec patch outside claude's actions was allowed")
	}
	if _, err := r.svc.Apply(ctx, claude, &oapv1.ApplyRequest{ProposalId: patchProp.Proposal.Id}); code(err) != PermissionDenied {
		t.Fatalf("agent applied what needs the owner: %v", err)
	}
	if _, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: "prop_nope"}); code(err) != NotFound {
		t.Fatalf("unknown proposal: %v", err)
	}
}

func TestApplyRefusesAStaleBase(t *testing.T) {
	r := newRig(t)
	r.create(t)
	first, _ := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 2)})
	second, _ := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 3)})
	a, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: first.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	r.await(t, a.OperationId)
	_, err = r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: second.Proposal.Id})
	var e *Error
	if !errors.As(err, &e) || e.Code != FailedPrecondition || e.Detail.Why != WhyStaleBase {
		t.Fatalf("stale base applied: %v", err)
	}
}

func TestFailedStepIsNotAchieved(t *testing.T) {
	r := newRig(t)
	r.runtime.Unhealthy = map[string]bool{"web": true}
	p, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "site", siteJSON)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	evs := r.await(t, a.OperationId)
	o := evs[len(evs)-1].GetOutcome()
	if o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_NOT_ACHIEVED || len(o.Reasons) == 0 {
		t.Fatalf("outcome %v", o)
	}
	if err := r.svc.Await(ctx, Owner, &oapv1.AwaitRequest{OperationId: "op_nope"}, func(*oapv1.Event) error { return nil }); code(err) != NotFound {
		t.Fatalf("unknown operation: %v", err)
	}
}

func TestExplainFindsMissingInstances(t *testing.T) {
	r := newRig(t)
	r.create(t)
	for _, ref := range r.runtime.Running() {
		r.runtime.Stop(ctx, ref) // the instance dies behind OAP's back
	}
	r.svc.Observer.Resync(ctx)
	x, err := r.svc.Explain(ctx, Owner, &oapv1.ExplainRequest{Application: "site"})
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Gaps) != 1 || x.Gaps[0] != "applications.site.components.web" || len(x.Causes) == 0 {
		t.Fatalf("explain %v", x)
	}
	if _, err := r.svc.Explain(ctx, Owner, &oapv1.ExplainRequest{Application: "nope"}); code(err) != NotFound {
		t.Fatal(err)
	}
}

// TestResumeAfterCrash: an apply recorded with no outcome is resumed by
// the next service on the same journal and runs to its outcome once.
func TestResumeAfterCrash(t *testing.T) {
	r := newRig(t)
	p, _ := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "site", siteJSON)})
	id := opID(p.Proposal.Id)
	// The process dies right after recording the apply.
	r.svc.append(ctx, KindApply, id, &oapv1.JournalEntry{Id: "apply_x", ParentId: p.Proposal.Id, Kind: oapv1.EntryKind_ENTRY_KIND_APPLY,
		Detail: &oapv1.JournalEntry_OperationId{OperationId: id}})
	next := r.service()
	if err := next.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r.svc = next
	evs := r.await(t, id)
	if evs[len(evs)-1].GetOutcome().GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED || len(r.runtime.Running()) != 1 {
		t.Fatalf("resumed outcome %v, running %d", evs[len(evs)-1], len(r.runtime.Running()))
	}
	again := r.service()
	again.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	entries, _ := r.journal.Since(ctx, 0, 10000)
	outcomes := 0
	for _, e := range entries {
		if e.Kind == KindOutcome {
			outcomes++
		}
	}
	if outcomes != 1 {
		t.Fatalf("%d outcomes for one operation", outcomes)
	}
}

// TestDomainChangeMovesRoutes: changing an application's domain, with no
// new release, moves its routes at the edge: the new host is routed and
// the old one isn't, and nothing restarts. (Found deploying Nisha Shipping:
// the plan wrote desired state and changed nothing at the edge.)
func TestDomainChangeMovesRoutes(t *testing.T) {
	r := newRig(t)
	r.create(t)
	running := r.runtime.Running()
	p, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "site", `{"ingress": {"domain": "www.example.com"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	evs := r.await(t, a.OperationId)
	if evs[len(evs)-1].GetOutcome().GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("outcome %v", evs[len(evs)-1])
	}
	cfg, _, _ := r.edge.Config(ctx)
	hosts := map[string]bool{}
	for _, m := range cfg.Members {
		hosts[m.Host] = true
	}
	if !hosts["www.example.com"] || hosts["site.example.com"] || len(hosts) != 1 {
		t.Fatalf("edge hosts after the domain change: %v (plan: %v)", hosts, p.Proposal.Steps)
	}
	if now := r.runtime.Running(); len(now) != len(running) || now[0] != running[0] {
		t.Fatalf("instances changed: %v -> %v", running, now)
	}
}

// TestWrongHostAtTheEdgeIsAGap: an instance the edge routes under another
// host than the desired one isn't converged, and Explain says why. (Found
// on vultr_oap: OAP reported converged while Caddy served the old domain.)
func TestWrongHostAtTheEdgeIsAGap(t *testing.T) {
	r := newRig(t)
	r.create(t)
	ref := r.runtime.Running()[0]
	cfg, _, _ := r.edge.Config(ctx)
	for i := range cfg.Members {
		cfg.Members[i].Host = "old.example.com" // the edge never moved
	}
	r.edge.Apply(ctx, cfg)
	r.svc.Observer.Resync(ctx)
	w, err := r.svc.Observe(ctx, Owner, &oapv1.ObserveRequest{Application: "site", Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	web := w.World.Applications[0].Components[0]
	if web.Converged || web.Instances[0].Routed {
		t.Fatalf("%v routed under the wrong host counts as converged: %v", ref, web)
	}
	x, err := r.svc.Explain(ctx, Owner, &oapv1.ExplainRequest{Application: "site"})
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Gaps) != 1 || len(x.Causes) == 0 || x.Causes[0].Why != "explain.routes" {
		t.Fatalf("explain: %v", x)
	}
}

var errEnough = errors.New("test: enough events")

// collect streams with watch from a revision until it has seen the event at
// revision until, or stops after limit events (a disconnect).
func collect(t *testing.T, r *rig, since uint64, app string, until uint64, limit int) []*oapv1.Event {
	t.Helper()
	var evs []*oapv1.Event
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := r.svc.Watch(cctx, Owner, &oapv1.WatchRequest{SinceRevision: since, Application: app}, func(e *oapv1.Event) error {
		evs = append(evs, e)
		if e.Revision >= until || len(evs) == limit {
			return errEnough
		}
		return nil
	})
	if !errors.Is(err, errEnough) {
		t.Fatalf("watch ended: %v", err)
	}
	return evs
}

func revisions(evs []*oapv1.Event) []uint64 {
	var out []uint64
	for _, e := range evs {
		out = append(out, e.Revision)
	}
	return out
}

// TestWatchResumesWithoutGapsOrRepeats: cutting the stream after every
// possible event and resuming from the last revision seen yields exactly
// the uninterrupted stream.
func TestWatchResumesWithoutGapsOrRepeats(t *testing.T) {
	r := newRig(t)
	seed(t, r)
	start, _ := r.journal.Head(ctx)
	r.create(t)
	p, _ := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 2)})
	a, _ := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	r.await(t, a.OperationId)
	end, _ := r.journal.Head(ctx)
	full := collect(t, r, start, "", end, 0)
	if len(full) < 10 || full[len(full)-1].Revision != end {
		t.Fatalf("full stream: %v", revisions(full))
	}
	for i := 1; i < len(full); i++ {
		if full[i].Revision != full[i-1].Revision+1 {
			t.Fatalf("gap in the full stream: %v", revisions(full))
		}
	}
	for cut := 1; cut < len(full); cut++ {
		first := collect(t, r, start, "", end, cut)
		rest := collect(t, r, first[len(first)-1].Revision, "", end, 0)
		got := append(first, rest...)
		if fmt.Sprint(revisions(got)) != fmt.Sprint(revisions(full)) {
			t.Fatalf("cut after %d: %v, want %v", cut, revisions(got), revisions(full))
		}
		for i := range got {
			if !proto.Equal(got[i], full[i]) {
				t.Fatalf("cut after %d: event %d differs", cut, i)
			}
		}
	}
	// Live: a watcher started now sees the next operation as it happens.
	head, _ := r.journal.Head(ctx)
	done := make(chan []*oapv1.Event, 1)
	go func() {
		var evs []*oapv1.Event
		r.svc.Watch(ctx, Owner, &oapv1.WatchRequest{Application: "site"}, func(e *oapv1.Event) error {
			evs = append(evs, e)
			if e.GetOutcome() != nil {
				done <- evs
				return errEnough
			}
			return nil
		})
	}()
	time.Sleep(50 * time.Millisecond)
	p, _ = r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 1)})
	a, _ = r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	select {
	case evs := <-done:
		if evs[0].Revision != head+1 || evs[0].GetJournalEntry().GetProposal() == nil {
			t.Fatalf("live watch began with %v", evs[0])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("live watch saw no outcome")
	}
}

func TestWatchFiltersByApplication(t *testing.T) {
	r := newRig(t)
	seed(t, r)
	start, _ := r.journal.Head(ctx)
	r.create(t)
	other := strings.Replace(siteJSON, `"name": "site"`, `"name": "blog"`, 1)
	other = strings.Replace(other, "site.example.com", "blog.example.com", 1)
	p, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "blog", other)})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	r.await(t, a.OperationId)
	end, _ := r.journal.Head(ctx)
	all := collect(t, r, start, "", end, 0)
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var blog []*oapv1.Event
	r.svc.Watch(cctx, Owner, &oapv1.WatchRequest{SinceRevision: start, Application: "blog"}, func(e *oapv1.Event) error {
		blog = append(blog, e)
		if e.Revision >= end {
			return errEnough
		}
		return nil
	})
	if len(blog) == 0 || len(blog) >= len(all) {
		t.Fatalf("blog events %d of %d", len(blog), len(all))
	}
	for _, e := range blog {
		if id := e.GetOperationProgress().GetOperationId(); id != "" && id != a.OperationId {
			t.Fatalf("site's event in blog's stream: %v", e)
		}
	}
}

// TestAwaitResumes: an await cut after any event and resumed from the
// last revision seen ends with the same events as one uninterrupted.
func TestAwaitResumes(t *testing.T) {
	r := newRig(t)
	op := r.create(t)
	var full []*oapv1.Event
	r.svc.Await(ctx, Owner, &oapv1.AwaitRequest{OperationId: op}, func(e *oapv1.Event) error { full = append(full, e); return nil })
	for cut := 1; cut < len(full); cut++ {
		var got []*oapv1.Event
		r.svc.Await(ctx, Owner, &oapv1.AwaitRequest{OperationId: op}, func(e *oapv1.Event) error {
			got = append(got, e)
			if len(got) == cut {
				return errEnough
			}
			return nil
		})
		r.svc.Await(ctx, Owner, &oapv1.AwaitRequest{OperationId: op, SinceRevision: got[len(got)-1].Revision}, func(e *oapv1.Event) error {
			got = append(got, e)
			return nil
		})
		if fmt.Sprint(revisions(got)) != fmt.Sprint(revisions(full)) {
			t.Fatalf("cut after %d: %v, want %v", cut, revisions(got), revisions(full))
		}
	}
}

// seed writes one journal entry, so a test's starting revision isn't 0,
// which Watch reads as "from now".
func seed(t *testing.T, r *rig) {
	t.Helper()
	if _, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "seed", strings.Replace(siteJSON, "site.example.com", "seed.example.com", 1))}); err != nil {
		t.Fatal(err)
	}
}

// TestWorldAgreesWithOutcome: once await reports an operation achieved,
// observe already shows its result, without waiting for the observer's
// next event or resync.
func TestWorldAgreesWithOutcome(t *testing.T) {
	r := newRig(t)
	r.create(t)
	p, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: scale("site", 3)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	r.await(t, a.OperationId)
	w, err := r.svc.Observe(ctx, Owner, &oapv1.ObserveRequest{Application: "site", Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range w.World.Applications[0].Components {
		if c.Name == "web" && (!c.Converged || c.GetActual().GetInstances() != 3) {
			t.Fatalf("observe right after the outcome: converged %v, %d instances", c.Converged, c.GetActual().GetInstances())
		}
	}
}

// TestFailedDeployLeavesNoStrays: a deploy whose new instance never becomes
// healthy is not achieved; its unready instance is stopped, and the desired
// state goes back to what runs. The next deploy then replaces exactly the
// instances that serve: none of the old release is left behind (R5.2).
func TestFailedDeployLeavesNoStrays(t *testing.T) {
	r := newRig(t)
	r.create(t)
	r.await(t, r.applyOK(t, scale("site", 3)))
	_, before, _ := r.desired.Get(ctx, "site")
	deploy := func(image string) *oapv1.Outcome {
		t.Helper()
		evs := r.await(t, r.applyOK(t, &oapv1.Intent{Application: "site",
			Change: &oapv1.Intent_Deploy{Deploy: &oapv1.Deploy{Images: map[string]string{"web": image}}}}))
		return evs[len(evs)-1].GetOutcome()
	}
	images := func() map[string]int {
		out := map[string]int{}
		for _, ref := range r.runtime.Running() {
			out[ref.Release]++
		}
		return out
	}
	start := images()

	r.runtime.Unhealthy = map[string]bool{"web": true}
	if o := deploy("reg/site:bad"); o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_NOT_ACHIEVED {
		t.Fatalf("broken deploy: %v", o)
	}
	if _, after, _ := r.desired.Get(ctx, "site"); after != before {
		t.Fatal("the failed deploy left its release as the desired state")
	}
	if got := images(); fmt.Sprint(got) != fmt.Sprint(start) {
		t.Fatalf("after the failed deploy: %v, want %v (the unready instance stays running)", got, start)
	}

	r.runtime.Unhealthy = nil
	if o := deploy("reg/site:2"); o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("fix forward: %v", o)
	}
	got := images()
	if len(got) != 1 {
		t.Fatalf("after fixing forward: %v (instances of more than one release)", got)
	}
	for _, n := range got {
		if n != 3 {
			t.Fatalf("after fixing forward: %v", got)
		}
	}
}

// applyOK proposes and applies an intent as the owner.
func (r *rig) applyOK(t *testing.T, in *oapv1.Intent) string {
	t.Helper()
	p, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: in})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	return a.OperationId
}
