// Package service implements the agent primitives (docs/architecture/
// agent-native-rewrite.md, section 5) over the core and the engine:
// observe, propose, apply, await and explain. Ask and login need identity
// and notifications (R3.6, R3.7) and report that they aren't available yet.
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/policy"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/proposal"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Journal entry kinds the service writes, besides the executor's "step".
const (
	KindProposal = "proposal"
	KindApply    = "apply"
	KindOutcome  = "outcome"
)

// Code classifies an error for the surface.
type Code int

const (
	InvalidArgument Code = iota + 1
	NotFound
	FailedPrecondition
	PermissionDenied
	Unimplemented
	Internal
	Unauthenticated
)

// Error is a service error with a typed detail: a why code and a remedy.
type Error struct {
	Code   Code
	Detail *oapv1.ErrorDetail
}

func (e *Error) Error() string { return e.Detail.GetWhy() + ": " + e.Detail.GetMessage() }

func fail(code Code, why, format string, args ...any) *Error {
	return &Error{Code: code, Detail: &oapv1.ErrorDetail{Why: why, Message: fmt.Sprintf(format, args...)}}
}

var refresh = &oapv1.Remedy{Action: &oapv1.Remedy_Refresh{Refresh: true}}

// Why codes the service adds to the core's.
const (
	WhyStaleRevision   = "service.stale_revision"
	WhyStaleBase       = "service.stale_base"
	WhyUnknownProposal = "service.unknown_proposal"
	WhyUnknownApp      = "service.unknown_application"
	WhyNotAllowed      = "service.not_allowed"
	WhyNotAvailable    = "service.not_available"
	WhyUnknownOp       = "service.unknown_operation"
)

// Caller is who is asking: the owner (on oapd's local socket, reached over
// SSH) or an agent holding a token.
type Caller struct {
	Owner bool
	Agent string // the name policy judges; "owner" for the owner
	// Apps are the applications an agent may act on; "*" for all. The
	// owner may act on everything.
	Apps []string
	// Local is true for the owner on oapd's local socket, reached over
	// SSH, and false for an owner session signed in over the network.
	Local bool
}

// Allows reports whether the caller may act on an application.
func (c Caller) Allows(app string) bool {
	if c.Owner {
		return true
	}
	for _, a := range c.Apps {
		if a == "*" || a == app {
			return true
		}
	}
	return false
}

// WhyOutOfScope is the error for an agent acting outside its scope.
const WhyOutOfScope = "identity.out_of_scope"

func outOfScope(c Caller, app string) error {
	return fail(PermissionDenied, WhyOutOfScope, "agent %s may not act on %s", c.Agent, app)
}

// Owner is the caller on the local socket.
var Owner = Caller{Owner: true, Agent: "owner", Local: true}

// Service runs the primitives.
type Service struct {
	Desired  ports.DesiredRepo
	Journal  ports.OpStore
	Clock    ports.Clock
	Observer *engine.Observer
	Executor *engine.Executor
	// MaxAge is how old an observation may be before it is stale.
	MaxAge time.Duration
	// LoginTTL is how long a device login waits for the owner.
	LoginTTL time.Duration
	// Notifier tells the owner about asks and trusted logins; nil sends none.
	Notifier ports.Notifier
	// SecretStore keeps the owner's secret values; nil means none can be set.
	SecretStore ports.SecretStore
	// Extensions runs installed extensions; nil means none can be installed.
	Extensions ports.Extensions
	// Builder builds images, through the build queue; nil means no builds.
	Builder ports.Builder
	// Facts reports facts about the commits deploys come from; nil means
	// proposals carry none.
	Facts ports.FactSource

	serializer *engine.Serializer
	runCtx     context.Context
	mu         sync.Mutex // orders appends that read the journal first

	nmu    sync.Mutex
	notify chan struct{} // closed and replaced on every journal append

	idOnce sync.Once
	id     *identity

	askOnce sync.Once
	asks    *askBook
}

// notifyingJournal wakes Await whenever anything is appended, including
// the executor's step entries.
type notifyingJournal struct {
	ports.OpStore
	wake func()
}

func (j notifyingJournal) Append(ctx context.Context, entries ...ops.Entry) (uint64, error) {
	rev, err := j.OpStore.Append(ctx, entries...)
	j.wake()
	return rev, err
}

func (s *Service) changed() <-chan struct{} {
	s.nmu.Lock()
	defer s.nmu.Unlock()
	if s.notify == nil {
		s.notify = make(chan struct{})
	}
	return s.notify
}

func (s *Service) wake() {
	s.nmu.Lock()
	defer s.nmu.Unlock()
	if s.notify != nil {
		close(s.notify)
	}
	s.notify = make(chan struct{})
}

// Start begins accepting applies, running operations under ctx, and
// resumes operations a crash interrupted: applied with no outcome.
func (s *Service) Start(ctx context.Context) error {
	if s.MaxAge == 0 {
		s.MaxAge = 2 * time.Minute
	}
	s.runCtx = ctx
	if _, ok := s.Journal.(notifyingJournal); !ok {
		s.Journal = notifyingJournal{OpStore: s.Journal, wake: s.wake}
	}
	if _, ok := s.Executor.Journal.(notifyingJournal); !ok {
		s.Executor.Journal = notifyingJournal{OpStore: s.Executor.Journal, wake: s.wake}
	}
	s.serializer = engine.NewSerializer(s.Executor.Run)
	entries, err := s.entries(ctx, 0)
	if err != nil {
		return err
	}
	s.loadIdentity(entries)
	s.loadAsks(entries)
	if err := s.loadExtensions(ctx); err != nil {
		return err
	}
	go func() { // record asks' expiry as it happens
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.ExpireAsks(context.WithoutCancel(ctx))
			}
		}
	}()
	done := map[string]bool{}
	for _, e := range entries {
		if e.Kind == KindOutcome {
			done[e.Operation] = true
		}
	}
	for _, e := range entries {
		if e.Kind != KindApply || done[e.Operation] {
			continue
		}
		je, err := decode(e.Data)
		if err != nil {
			return err
		}
		p, err := s.proposal(ctx, je.GetParentId())
		if err != nil {
			return err
		}
		if isExtensionOp(p) {
			s.runExtension(e.Operation, p)
			continue
		}
		if isBuildOp(p) {
			s.runBuild(e.Operation, p)
			continue
		}
		op, err := s.operation(ctx, e.Operation, p, false)
		if err != nil {
			s.outcome(op.ID, err)
			continue
		}
		s.submit(op)
	}
	return nil
}

func (s *Service) entries(ctx context.Context, after uint64) ([]ops.Entry, error) {
	var out []ops.Entry
	for {
		page, err := s.Journal.Since(ctx, after, 500)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		out = append(out, page...)
		after = page[len(page)-1].Revision
	}
}

var marshal = protojson.MarshalOptions{UseProtoNames: true}

func decode(b []byte) (*oapv1.JournalEntry, error) {
	var je oapv1.JournalEntry
	if err := protojson.Unmarshal(b, &je); err != nil {
		return nil, fmt.Errorf("service: damaged journal entry: %w", err)
	}
	return &je, nil
}

func actor(c Caller) *oapv1.Actor {
	if c.Owner {
		return &oapv1.Actor{Who: &oapv1.Actor_Owner{Owner: "owner"}}
	}
	return &oapv1.Actor{Who: &oapv1.Actor_Agent{Agent: c.Agent}}
}

func (s *Service) append(ctx context.Context, kind, operation string, je *oapv1.JournalEntry) (uint64, error) {
	data, err := marshal.Marshal(je)
	if err != nil {
		return 0, err
	}
	return s.Journal.Append(ctx, ops.Entry{At: s.Clock.Now(), Kind: kind, Operation: operation, Data: data})
}

// Observe returns the world model at the journal's head revision.
func (s *Service) Observe(ctx context.Context, c Caller, req *oapv1.ObserveRequest) (*oapv1.ObserveResponse, error) {
	if app := req.GetApplication(); app != "" && !c.Allows(app) {
		return nil, outOfScope(c, app)
	}
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	if !c.Owner { // an agent sees only its applications
		var keep []*oapv1.Application
		for _, a := range w.Applications {
			if c.Allows(a.Name) {
				keep = append(keep, a)
			}
		}
		w.Applications = keep
	}
	if app := req.GetApplication(); app != "" {
		var keep []*oapv1.Application
		for _, a := range w.Applications {
			if a.Name == app {
				keep = append(keep, a)
			}
		}
		if len(keep) == 0 {
			return nil, fail(NotFound, WhyUnknownApp, "no application %q", app)
		}
		w.Applications = keep
	}
	if !req.GetDetail() {
		for _, a := range w.Applications {
			for _, comp := range a.Components {
				comp.Instances = nil
			}
		}
	}
	if env, err := s.envelope(ctx); err == nil {
		policy.Affordances(env, c.Agent, w)
	}
	out := &oapv1.ObserveResponse{World: w}
	if req.GetBriefing() {
		md, err := s.Briefing(ctx, c, req.GetApplication())
		if err != nil {
			return nil, err
		}
		out.Briefing = md
	}
	return out, nil
}

func (s *Service) world(ctx context.Context) (*oapv1.World, error) {
	head, err := s.Journal.Head(ctx)
	if err != nil {
		return nil, err
	}
	names, err := s.Desired.Applications(ctx)
	if err != nil {
		return nil, err
	}
	var desired []model.Desired
	for _, n := range names {
		tree, hash, err := s.Desired.Get(ctx, n)
		if err != nil {
			return nil, err
		}
		app, probs := spec.ParseApplication(tree)
		if len(probs) > 0 {
			return nil, fail(Internal, probs[0].Why, "stored desired state of %s is invalid: %s", n, probs[0].Message)
		}
		desired = append(desired, model.Desired{App: app, Hash: hash})
	}
	var obs *model.Observation
	if s.Observer != nil {
		snap := s.Observer.Snapshot()
		if !snap.At.IsZero() {
			obs = &snap
		}
	}
	if obs != nil && s.Executor != nil && s.Executor.Edge != nil {
		// The runtime doesn't know routes; the edge does.
		cfg, _, err := s.Executor.Edge.Config(ctx)
		if err != nil {
			return nil, err
		}
		apps := map[string]*spec.Application{}
		for _, d := range desired {
			apps[d.App.Name] = d.App
		}
		insts := make([]model.ObservedInstance, len(obs.Instances))
		copy(insts, obs.Instances)
		for i, in := range insts {
			ref := ops.InstanceRef{Application: in.Application, Component: in.Component, Ordinal: int(in.Ordinal), Release: in.Release}
			insts[i].Routed = routedAsDesired(cfg, apps[in.Application], ref)
		}
		o := *obs
		o.Instances = insts
		obs = &o
	}
	return model.Build(head, desired, obs, model.Options{Now: s.Clock.Now(), MaxAge: s.MaxAge}), nil
}

// routedAsDesired reports whether the edge routes an instance exactly as
// desired: every desired route and no other. An instance routed under an
// old domain or path isn't routed as desired, so its component isn't
// converged.
func routedAsDesired(cfg ops.EdgeConfig, app *spec.Application, ref ops.InstanceRef) bool {
	var want []ops.RouteMember
	if app != nil {
		want = ops.Members(app, ref)
	}
	have := map[ops.RouteMember]bool{}
	for _, m := range cfg.Members {
		if m.Instance == ref {
			have[m] = true
		}
	}
	if len(want) == 0 {
		return len(have) > 0
	}
	if len(have) != len(want) {
		return false
	}
	for _, m := range want {
		if !have[m] {
			return false
		}
	}
	return true
}

func (s *Service) envelope(ctx context.Context) (*oapv1.Envelope, error) {
	tree, version, err := s.Desired.Envelope(ctx)
	if err != nil {
		return nil, err
	}
	env, probs := policy.ParseEnvelope(tree)
	if len(probs) > 0 {
		return nil, fail(Internal, probs[0].Why, "the envelope is invalid: %s", probs[0].Message)
	}
	env.Version = version
	return env, nil
}

// newBase is the desired state an application starts from before its
// first change: nothing but its name. Creating an application is a
// spec_patch from it.
func newBase(app string) map[string]any {
	return map[string]any{"api_version": "oap.dev/v1", "kind": "Application", "name": app,
		"environment": "production", "components": map[string]any{}}
}

// current returns an application's desired state, or the base of a new
// application when the intent may create it.
func (s *Service) current(ctx context.Context, app string, creating bool) (any, error) {
	tree, _, err := s.Desired.Get(ctx, app)
	switch {
	case err == nil:
		return tree, nil
	case errors.Is(err, ops.ErrNoApplication) && creating:
		return newBase(app), nil
	case errors.Is(err, ops.ErrNoApplication):
		e := fail(NotFound, WhyUnknownApp, "no application %q; create it with a spec_patch holding its whole spec", app)
		return nil, e
	}
	return nil, err
}

func creates(in *oapv1.Intent) bool {
	_, ok := in.GetChange().(*oapv1.Intent_SpecPatch)
	return ok
}

// Propose compiles, plans and judges an intent, records the proposal in
// the journal and returns it. It changes nothing else.
func (s *Service) Propose(ctx context.Context, c Caller, req *oapv1.ProposeRequest) (*oapv1.ProposeResponse, error) {
	in := req.GetIntent()
	if intent.IsExtension(in) {
		if !c.Allows("") {
			return nil, extensionOutOfScope(c)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		head, err := s.Journal.Head(ctx)
		if err != nil {
			return nil, err
		}
		if exp := req.GetExpectedRevision(); exp != 0 && exp != head {
			e := fail(FailedPrecondition, WhyStaleRevision, "decided at revision %d, but the world is at %d", exp, head)
			e.Detail.Remedy = refresh
			return nil, e
		}
		return s.proposeExtension(ctx, c, req, head)
	}
	if in.GetApplication() == "" {
		return nil, fail(InvalidArgument, intent.WhyIncomplete, "the intent names no application")
	}
	if !c.Allows(in.GetApplication()) {
		return nil, outOfScope(c, in.GetApplication())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	head, err := s.Journal.Head(ctx)
	if err != nil {
		return nil, err
	}
	if exp := req.GetExpectedRevision(); exp != 0 && exp != head {
		e := fail(FailedPrecondition, WhyStaleRevision, "decided at revision %d, but the world is at %d", exp, head)
		e.Detail.Remedy = refresh
		return nil, e
	}
	if in.GetBuild() != nil {
		return s.proposeBuild(ctx, c, in, head)
	}
	tree, err := s.current(ctx, in.GetApplication(), creates(in))
	if err != nil {
		return nil, err
	}
	env, err := s.envelope(ctx)
	if err != nil {
		return nil, err
	}
	p, ierr := proposal.Propose(proposal.Inputs{Agent: c.Agent, BaseRevision: head, Tree: tree,
		History: func(h string) (any, bool) { t, ok, _ := s.Desired.ByHash(ctx, h); return t, ok },
		Intent:  in, Envelope: env, Facts: s.changeFacts(ctx, in)})
	if ierr != nil {
		return nil, &Error{Code: InvalidArgument, Detail: ierr.Detail()}
	}
	if _, err := s.append(ctx, KindProposal, "", &oapv1.JournalEntry{Id: p.Id, Kind: oapv1.EntryKind_ENTRY_KIND_PROPOSAL,
		Actor: actor(c), BaseRevision: head, Summary: summary(in), Detail: &oapv1.JournalEntry_Proposal{Proposal: p}}); err != nil {
		return nil, err
	}
	return &oapv1.ProposeResponse{Proposal: p}, nil
}

func summary(in *oapv1.Intent) string {
	if b := strings.TrimSpace(in.GetBecause()); b != "" {
		return b
	}
	if e := extensionSummary(in); e != "" {
		return e
	}
	return fmt.Sprintf("%s %s", intent.KindOf(in), in.GetApplication())
}

func (s *Service) proposal(ctx context.Context, id string) (*oapv1.Proposal, error) {
	entries, err := s.entries(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Kind != KindProposal {
			continue
		}
		je, err := decode(e.Data)
		if err != nil {
			return nil, err
		}
		if je.GetId() == id {
			return je.GetProposal(), nil
		}
	}
	return nil, fail(NotFound, WhyUnknownProposal, "no proposal %q; propose again", id)
}

// operation rebuilds the operation a proposal applies. The application's
// desired state must still be the proposal's base unless the operation
// already wrote it (resume after a crash).
func (s *Service) operation(ctx context.Context, id string, p *oapv1.Proposal, fresh bool) (engine.Operation, error) {
	in := p.GetIntent()
	app := in.GetApplication()
	op := engine.Operation{ID: id, Application: app, Steps: p.GetSteps(), Author: p.GetAgent(), Message: summary(in)}
	base, err := s.current(ctx, app, creates(in))
	if err != nil {
		return op, err
	}
	if h, _ := spec.Hash(base); h != p.GetBaseSpecHash() {
		if fresh {
			e := fail(FailedPrecondition, WhyStaleBase, "%s changed since this proposal was made; propose again", app)
			e.Detail.Remedy = refresh
			return op, e
		}
		// Resuming: the operation may already have written its new state;
		// start from the proposal's base instead.
		t, ok, _ := s.Desired.ByHash(ctx, p.GetBaseSpecHash())
		if !ok {
			t = newBase(app)
		}
		base = t
	}
	res, ierr := intent.Compile(intent.Current{Tree: base, History: func(h string) (any, bool) {
		t, ok, _ := s.Desired.ByHash(ctx, h)
		return t, ok
	}}, in)
	if ierr != nil {
		return op, &Error{Code: FailedPrecondition, Detail: ierr.Detail()}
	}
	op.Old, _ = spec.ParseApplication(base)
	op.New, op.NewTree, op.OldTree = res.App, res.Tree, base
	return op, nil
}

func opID(proposalID string) string { return "op_" + strings.TrimPrefix(proposalID, "prop_") }

// Apply runs exactly the proposal with this id. A proposal the envelope
// allows may be applied by its agent; one that needs approval only by the
// owner; a denied one never. Applying the same proposal again returns the
// same operation.
func (s *Service) Apply(ctx context.Context, c Caller, req *oapv1.ApplyRequest) (*oapv1.ApplyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.proposal(ctx, req.GetProposalId())
	if err != nil {
		return nil, err
	}
	if isExtensionOp(p) && !c.Allows("") {
		return nil, extensionOutOfScope(c)
	}
	if !c.Allows(p.GetIntent().GetApplication()) {
		return nil, outOfScope(c, p.GetIntent().GetApplication())
	}
	id := opID(p.GetId())
	entries, err := s.entries(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Kind == KindApply && e.Operation == id {
			je, _ := decode(e.Data)
			return &oapv1.ApplyResponse{OperationId: id, EntryId: je.GetId()}, nil
		}
	}
	switch v := p.GetVerdict(); {
	case v == oapv1.Verdict_VERDICT_ALLOWED && (c.Owner || c.Agent == p.GetAgent()):
	case v == oapv1.Verdict_VERDICT_NEEDS_APPROVAL && c.Owner:
	case v == oapv1.Verdict_VERDICT_NEEDS_APPROVAL && c.Agent == p.GetAgent() && s.approvedByOwner(p.GetId()):
	case v == oapv1.Verdict_VERDICT_NEEDS_APPROVAL:
		e := fail(PermissionDenied, WhyNotAllowed, "this proposal needs the owner's approval")
		e.Detail.Remedy = &oapv1.Remedy{Action: &oapv1.Remedy_Ask{Ask: &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_APPROVAL,
			ProposalId: p.GetId(), Question: "Approve " + summary(p.GetIntent()) + "?"}}}
		return nil, e
	default:
		return nil, fail(PermissionDenied, WhyNotAllowed, "the envelope denies this proposal: %s", reasons(p))
	}
	if isBuildOp(p) {
		entryID := "apply_" + strings.TrimPrefix(id, "op_")
		head, _ := s.Journal.Head(ctx)
		if _, err := s.append(ctx, KindApply, id, &oapv1.JournalEntry{Id: entryID, ParentId: p.GetId(), Kind: oapv1.EntryKind_ENTRY_KIND_APPLY,
			Actor: actor(c), BaseRevision: head, Summary: summary(p.GetIntent()), Detail: &oapv1.JournalEntry_OperationId{OperationId: id}}); err != nil {
			return nil, err
		}
		s.runBuild(id, p)
		return &oapv1.ApplyResponse{OperationId: id, EntryId: entryID}, nil
	}
	if isExtensionOp(p) {
		if err := s.checkExtensionBase(ctx, p); err != nil {
			return nil, err
		}
		entryID := "apply_" + strings.TrimPrefix(id, "op_")
		head, _ := s.Journal.Head(ctx)
		if _, err := s.append(ctx, KindApply, id, &oapv1.JournalEntry{Id: entryID, ParentId: p.GetId(), Kind: oapv1.EntryKind_ENTRY_KIND_APPLY,
			Actor: actor(c), BaseRevision: head, Summary: summary(p.GetIntent()), Detail: &oapv1.JournalEntry_OperationId{OperationId: id}}); err != nil {
			return nil, err
		}
		s.runExtension(id, p)
		return &oapv1.ApplyResponse{OperationId: id, EntryId: entryID}, nil
	}
	op, err := s.operation(ctx, id, p, true)
	if err != nil {
		return nil, err
	}
	entryID := "apply_" + strings.TrimPrefix(id, "op_")
	head, _ := s.Journal.Head(ctx)
	if _, err := s.append(ctx, KindApply, id, &oapv1.JournalEntry{Id: entryID, ParentId: p.GetId(), Kind: oapv1.EntryKind_ENTRY_KIND_APPLY,
		Actor: actor(c), BaseRevision: head, Summary: op.Message, Detail: &oapv1.JournalEntry_OperationId{OperationId: id}}); err != nil {
		return nil, err
	}
	s.submit(op)
	return &oapv1.ApplyResponse{OperationId: id, EntryId: entryID}, nil
}

func reasons(p *oapv1.Proposal) string {
	var out []string
	for _, r := range p.GetReasons() {
		out = append(out, r.GetMessage())
	}
	return strings.Join(out, "; ")
}

func (s *Service) submit(op engine.Operation) {
	done := s.serializer.Submit(s.runCtx, op)
	go func() {
		err := <-done
		var se *engine.StepError
		if errors.As(err, &se) {
			s.restoreDesired(op)
		}
		s.outcome(op.ID, err)
	}()
}

// restoreDesired puts an application's desired state back to where a
// failed operation started, so it describes what runs: the next proposal
// then plans from the instances that actually serve. The failure stays in
// the journal and the outcome.
func (s *Service) restoreDesired(op engine.Operation) {
	if op.OldTree == nil || op.Old == nil {
		return // a new application has no earlier desired state to go back to
	}
	ctx := context.WithoutCancel(s.runCtx)
	cur, curHash, err := s.Desired.Get(ctx, op.Application)
	if err != nil || cur == nil {
		return
	}
	if oldHash, err := spec.Hash(op.OldTree); err != nil || oldHash == curHash {
		return
	}
	s.Desired.Commit(ctx, op.Application, op.OldTree, "oapd", "Restore after "+op.ID+" wasn't achieved")
}

func (s *Service) outcome(id string, runErr error) { s.outcomeWith(id, runErr, nil, nil) }

// outcomeWith records an operation's outcome, with the images it built and
// their commits.
func (s *Service) outcomeWith(id string, runErr error, images, commits map[string]string) {
	o := &oapv1.Outcome{OperationId: id, Status: oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED}
	if runErr == nil {
		o.Images, o.Commits = images, commits
	}
	var se *engine.StepError
	switch {
	case runErr == nil:
	case errors.As(runErr, &se):
		o.Status = oapv1.OutcomeStatus_OUTCOME_STATUS_NOT_ACHIEVED
		o.Reasons = []*oapv1.Reason{{Why: "engine.step_failed", Message: se.Error()}}
	default:
		o.Status = oapv1.OutcomeStatus_OUTCOME_STATUS_UNCERTAIN
		o.Reasons = []*oapv1.Reason{{Why: "engine.interrupted", Message: runErr.Error()}}
	}
	ctx := context.WithoutCancel(s.runCtx)
	// Observe the runtime before telling anyone the outcome, so a caller
	// that observes right after await sees the operation's result.
	if s.Observer != nil {
		s.Observer.Resync(ctx) // on failure the world stays as last observed and says how old it is
	}
	s.append(ctx, KindOutcome, id, &oapv1.JournalEntry{Id: "outcome_" + strings.TrimPrefix(id, "op_"), Kind: oapv1.EntryKind_ENTRY_KIND_OUTCOME,
		Actor: &oapv1.Actor{Who: &oapv1.Actor_System{System: true}}, Summary: o.Status.String(), Detail: &oapv1.JournalEntry_Outcome{Outcome: o}})
}

// Await sends an operation's progress, step by step, and ends with its
// outcome; after timeout it ends with an uncertain outcome. A caller that
// lost the stream resumes with since_revision set to the last revision it
// saw, and gets every later event exactly once.
func (s *Service) Await(ctx context.Context, c Caller, req *oapv1.AwaitRequest, send func(*oapv1.Event) error) error {
	id := req.GetOperationId()
	timeout := time.Duration(req.GetTimeoutSeconds()) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if _, err := s.entriesFor(ctx, id); err != nil {
		return err
	}
	if !c.Owner {
		if app := s.operationApp(ctx, id); !c.Allows(app) {
			return outOfScope(c, app)
		}
	}
	deadline := s.Clock.Now().Add(timeout)
	var last uint64
	err := s.stream(ctx, req.GetSinceRevision(), func(op, app, _ string) bool { return op == id }, deadline, func(e *oapv1.Event) (bool, error) {
		last = e.Revision
		if e.GetJournalEntry() != nil {
			return false, nil // await reports progress and the outcome only
		}
		if err := send(e); err != nil {
			return true, err
		}
		return e.GetOutcome() != nil, nil
	})
	if errors.Is(err, errDeadline) {
		return send(&oapv1.Event{Revision: last, At: timestamppb.New(s.Clock.Now()), Kind: &oapv1.Event_Outcome{Outcome: &oapv1.Outcome{
			OperationId: id, Status: oapv1.OutcomeStatus_OUTCOME_STATUS_UNCERTAIN,
			Reasons: []*oapv1.Reason{{Why: "service.await_timeout", Message: fmt.Sprintf("no outcome within %s; await again from revision %d", timeout, last)}}}}})
	}
	return err
}

// Watch streams the journal's events after a revision (0: from now) until
// the caller goes away, optionally for one application. A caller that
// reconnects with the last revision it saw misses nothing and sees nothing
// twice: every event carries its journal revision, in order.
func (s *Service) Watch(ctx context.Context, c Caller, req *oapv1.WatchRequest, send func(*oapv1.Event) error) error {
	after := req.GetSinceRevision()
	if after == 0 {
		head, err := s.Journal.Head(ctx)
		if err != nil {
			return err
		}
		after = head
	}
	app := req.GetApplication()
	if app != "" && !c.Allows(app) {
		return outOfScope(c, app)
	}
	visible := func(a, asker string) bool { // an agent sees its applications and its own asks
		return c.Owner || (a != "" && c.Allows(a)) || (asker != "" && asker == c.Agent)
	}
	return s.stream(ctx, after, func(_, a, asker string) bool { return (app == "" || a == app) && visible(a, asker) }, time.Time{}, func(e *oapv1.Event) (bool, error) {
		return false, send(e)
	})
}

var errDeadline = errors.New("service: deadline")

// stream sends the events of journal entries after a revision that match,
// waiting for new ones, until emit says stop, ctx ends or the deadline
// (if not zero) passes. match gets the entry's operation and application.
func (s *Service) stream(ctx context.Context, after uint64, match func(op, app, asker string) bool, deadline time.Time,
	emit func(*oapv1.Event) (stop bool, err error)) error {
	apps := map[string]string{}         // operation -> application
	steps := map[string][]*oapv1.Step{} // operation -> its steps
	index := func(e ops.Entry) {
		if e.Kind != KindApply {
			return
		}
		je, err := decode(e.Data)
		if err != nil {
			return
		}
		if p, err := s.proposal(ctx, je.GetParentId()); err == nil {
			apps[e.Operation] = p.GetIntent().GetApplication()
			steps[e.Operation] = p.GetSteps()
		}
	}
	// Index operations recorded before the start, so their later events
	// can be matched by application.
	if after > 0 {
		earlier, err := s.entries(ctx, 0)
		if err != nil {
			return err
		}
		for _, e := range earlier {
			if e.Revision > after {
				break
			}
			index(e)
		}
	}
	for {
		changed := s.changed() // before reading, so no append is missed
		page, err := s.entries(ctx, after)
		if err != nil {
			return err
		}
		for _, e := range page {
			after = e.Revision
			index(e)
			ev, app, asker := s.event(e, apps, steps)
			if ev == nil || !match(e.Operation, app, asker) {
				continue
			}
			stop, err := emit(ev)
			if err != nil || stop {
				return err
			}
		}
		if !deadline.IsZero() && !s.Clock.Now().Before(deadline) {
			return errDeadline
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second): // re-check the deadline
		}
	}
}

// event turns a journal entry into an API event and names its
// application and, for asks, the agent that asked.
func (s *Service) event(e ops.Entry, apps map[string]string, steps map[string][]*oapv1.Step) (*oapv1.Event, string, string) {
	ev := &oapv1.Event{Revision: e.Revision, At: timestamppb.New(e.At)}
	switch e.Kind {
	case engine.KindStep:
		prog := &oapv1.OperationProgress{OperationId: e.Operation, StepIndex: int32(e.Step), State: stepState(e.State)}
		if st := steps[e.Operation]; e.Step < len(st) {
			prog.Step = proto.Clone(st[e.Step]).(*oapv1.Step)
		}
		ev.Kind = &oapv1.Event_OperationProgress{OperationProgress: prog}
		return ev, apps[e.Operation], ""
	case KindProposal, KindApply, KindOutcome:
		je, err := decode(e.Data)
		if err != nil {
			return nil, "", ""
		}
		if e.Kind == KindOutcome {
			ev.Kind = &oapv1.Event_Outcome{Outcome: je.GetOutcome()}
			return ev, apps[e.Operation], ""
		}
		ev.Kind = &oapv1.Event_JournalEntry{JournalEntry: je}
		if e.Kind == KindProposal {
			return ev, je.GetProposal().GetIntent().GetApplication(), ""
		}
		return ev, apps[e.Operation], ""
	case KindAsk, KindAskAnswer:
		je, err := decode(e.Data)
		if err != nil {
			return nil, "", ""
		}
		app, asker := s.askInfo(e.Operation)
		if e.Kind == KindAsk {
			ev.Kind = &oapv1.Event_JournalEntry{JournalEntry: je}
		} else {
			ev.Kind = &oapv1.Event_AskAnswered{AskAnswered: &oapv1.AskAnswered{AskId: je.GetParentId(), Answer: je.GetAnswer(),
				By: je.GetActor(), Expired: je.GetActor().GetSystem()}}
		}
		return ev, app, asker
	}
	return nil, "", ""
}

// operationApp names an operation's application.
func (s *Service) operationApp(ctx context.Context, id string) string {
	entries, err := s.entries(ctx, 0)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.Kind == KindApply && e.Operation == id {
			je, err := decode(e.Data)
			if err != nil {
				return ""
			}
			if p, err := s.proposal(ctx, je.GetParentId()); err == nil {
				return p.GetIntent().GetApplication()
			}
		}
	}
	return ""
}

// entriesFor fails when no apply entry exists for an operation.
func (s *Service) entriesFor(ctx context.Context, id string) ([]ops.Entry, error) {
	entries, err := s.entries(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Kind == KindApply && e.Operation == id {
			return entries, nil
		}
	}
	return nil, fail(NotFound, WhyUnknownOp, "no operation %q", id)
}

func stepState(s string) oapv1.StepState {
	switch s {
	case engine.StateStarted:
		return oapv1.StepState_STEP_STATE_STARTED
	case engine.StateSucceeded:
		return oapv1.StepState_STEP_STATE_SUCCEEDED
	case engine.StateFailed:
		return oapv1.StepState_STEP_STATE_FAILED
	}
	return oapv1.StepState_STEP_STATE_UNCERTAIN
}

// Explain answers why an application's actual state isn't its desired
// state, from the world model.
func (s *Service) Explain(ctx context.Context, c Caller, req *oapv1.ExplainRequest) (*oapv1.ExplainResponse, error) {
	if !c.Allows(req.GetApplication()) {
		return nil, outOfScope(c, req.GetApplication())
	}
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	out := &oapv1.ExplainResponse{Revision: w.GetRevision()}
	var app *oapv1.Application
	for _, a := range w.Applications {
		if a.Name == req.GetApplication() {
			app = a
		}
	}
	if app == nil {
		return nil, fail(NotFound, WhyUnknownApp, "no application %q", req.GetApplication())
	}
	for _, comp := range app.Components {
		if req.GetComponent() != "" && comp.Name != req.GetComponent() {
			continue
		}
		path := "applications." + app.Name + ".components." + comp.Name
		if comp.GetHealth().GetFreshness().GetStale() {
			out.Causes = append(out.Causes, &oapv1.Cause{Why: "explain.stale", Summary: comp.Name + ": the last observation is too old to judge", Confidence: 60})
		}
		if comp.Converged {
			continue
		}
		out.Gaps = append(out.Gaps, path)
		d, a := comp.GetDesired(), comp.GetActual()
		switch {
		case a == nil:
			out.Causes = append(out.Causes, &oapv1.Cause{Why: "explain.never_observed", Summary: comp.Name + ": never observed running", Confidence: 70,
				Evidence: comp.GetHealth().GetMissingEvidence()})
		case a.GetInstances() < d.GetInstances():
			out.Causes = append(out.Causes, &oapv1.Cause{Why: "explain.missing_instances", Confidence: 90,
				Summary: fmt.Sprintf("%s: %d of %d instances running", comp.Name, a.GetInstances(), d.GetInstances())})
		case len(d.GetRoutes()) > 0 && unrouted(comp):
			out.Causes = append(out.Causes, &oapv1.Cause{Why: "explain.routes", Confidence: 90,
				Summary: fmt.Sprintf("%s: the edge doesn't route its instances as desired (domain or paths differ); an operation that moves its routes fixes it", comp.Name)})
		case a.GetRelease() != d.GetRelease():
			out.Causes = append(out.Causes, &oapv1.Cause{Why: "explain.old_release", Confidence: 90,
				Summary: fmt.Sprintf("%s: running release %s, desired %s", comp.Name, a.GetRelease(), d.GetRelease())})
		default:
			out.Causes = append(out.Causes, &oapv1.Cause{Why: "explain.unhealthy", Confidence: 50,
				Summary: fmt.Sprintf("%s: %s", comp.Name, strings.TrimPrefix(comp.GetHealth().GetStatus().String(), "HEALTH_STATUS_"))})
		}
	}
	sort.Strings(out.Gaps)
	return out, nil
}

func unrouted(c *oapv1.Component) bool {
	for _, in := range c.GetInstances() {
		if !in.GetRouted() {
			return true
		}
	}
	return false
}
