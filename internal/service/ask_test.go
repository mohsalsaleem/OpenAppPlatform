package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

type fakeNotifier struct {
	mu   sync.Mutex
	sent []ops.Notification
}

func (f *fakeNotifier) Notify(ctx context.Context, n ops.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	return nil
}

// agentCaller signs an agent in through a device login.
func agentCaller(t *testing.T, r *rig, name string, apps ...string) Caller {
	t.Helper()
	tok := token(t, login(t, r, &oapv1.LoginRequest{Agent: name}, func(code string) {
		r.svc.ApproveLogin(ctx, Owner, &oapv1.ApproveLoginRequest{UserCode: code, Applications: apps, May: []string{"scale"}})
	}))
	c, err := r.svc.Authenticate(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// watchAsk collects watch events for an agent until one matches done.
func watchAsk(t *testing.T, r *rig, c Caller, since uint64, done func(*oapv1.Event) bool) []*oapv1.Event {
	t.Helper()
	var evs []*oapv1.Event
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := r.svc.Watch(cctx, c, &oapv1.WatchRequest{SinceRevision: since}, func(e *oapv1.Event) error {
		evs = append(evs, e)
		if done(e) {
			return errEnough
		}
		return nil
	})
	if err != errEnough {
		t.Fatalf("watch ended: %v (%d events)", err, len(evs))
	}
	return evs
}

// TestAskAnswerAndExpiryFlowThroughWatch is R3.7's acceptance: an ask, its
// answer and an expiry reach the asking agent through watch, and an
// expired ask can't be answered.
func TestAskAnswerAndExpiryFlowThroughWatch(t *testing.T) {
	r := newRig(t)
	n := &fakeNotifier{}
	r.svc.Notifier = n
	claude := agentCaller(t, r, "claude", "site")
	start, _ := r.journal.Head(ctx)

	a, err := r.svc.Ask(ctx, claude, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_DECISION, Question: "Which region?", Options: []string{"eu", "us"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 1 || n.sent[0].Kind != "ask" || !strings.Contains(n.sent[0].Body, "oapctl owner answer "+a.AskId) {
		t.Fatalf("notification %v", n.sent)
	}
	if l, _ := r.svc.ListAsks(ctx, Owner); len(l.Asks) != 1 || l.Asks[0].Agent != "claude" {
		t.Fatalf("open asks %v", l)
	}
	if _, err := r.svc.AnswerAsk(ctx, claude, &oapv1.AnswerAskRequest{AskId: a.AskId, Answer: "eu"}); code(err) != PermissionDenied {
		t.Fatalf("an agent answered: %v", err)
	}
	if _, err := r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: a.AskId, Answer: "mars"}); code(err) != InvalidArgument {
		t.Fatalf("answer outside the options: %v", err)
	}
	if _, err := r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: a.AskId, Answer: "eu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: a.AskId, Answer: "us"}); code(err) != FailedPrecondition {
		t.Fatalf("answered twice: %v", err)
	}

	short, err := r.svc.Ask(ctx, claude, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "The registry login failed", ExpiresInSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(61 * time.Second)
	r.svc.ExpireAsks(ctx)
	_, err = r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: short.AskId, Answer: "fixed"})
	var e *Error
	if !errorsAs(err, &e) || e.Code != FailedPrecondition || e.Detail.Why != WhyAskAnswered {
		t.Fatalf("answering an expired ask: %v", err)
	}

	evs := watchAsk(t, r, claude, start, func(e *oapv1.Event) bool { return e.GetAskAnswered().GetAskId() == short.AskId })
	var asked, answered, expired bool
	for _, e := range evs {
		switch {
		case e.GetJournalEntry().GetAsk().GetQuestion() == "Which region?":
			asked = true
		case e.GetAskAnswered().GetAskId() == a.AskId && e.GetAskAnswered().GetAnswer() == "eu" && e.GetAskAnswered().GetBy().GetOwner() != "":
			answered = true
		case e.GetAskAnswered().GetAskId() == short.AskId && e.GetAskAnswered().GetExpired():
			expired = true
		}
	}
	if !asked || !answered || !expired {
		t.Fatalf("watch saw ask %v, answer %v, expiry %v: %v", asked, answered, expired, evs)
	}
	// Another agent doesn't see claude's asks.
	other := agentCaller(t, r, "other", "blog")
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	r.svc.Watch(cctx, other, &oapv1.WatchRequest{SinceRevision: start}, func(e *oapv1.Event) error {
		if e.GetAskAnswered() != nil || e.GetJournalEntry().GetAsk() != nil {
			t.Errorf("another agent saw %v", e)
		}
		return nil
	})
}

// TestApprovalAskLetsTheAgentApply: the needs-approval loop from the
// design: the agent's apply is refused with an ask as the remedy; the
// owner approves the ask; the agent's apply then runs.
func TestApprovalAskLetsTheAgentApply(t *testing.T) {
	r := newRig(t)
	r.create(t)
	claude := agentCaller(t, r, "claude", "site")
	p, err := r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: patch(t, "site", `{"components": {"web": {"port": 9090}}}`)})
	if err != nil || p.Proposal.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
		t.Fatalf("proposal %v %v", p, err)
	}
	_, err = r.svc.Apply(ctx, claude, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	var e *Error
	if !errorsAs(err, &e) || e.Detail.GetRemedy().GetAsk().GetProposalId() != p.Proposal.Id {
		t.Fatalf("refusal without an ask remedy: %v", err)
	}
	ask, err := r.svc.Ask(ctx, claude, e.Detail.GetRemedy().GetAsk())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: ask.AskId, Answer: "yes"}); code(err) != InvalidArgument {
		t.Fatalf("approval answered with yes: %v", err)
	}
	r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: ask.AskId, Answer: "approve"})
	a, err := r.svc.Apply(ctx, claude, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatalf("apply after approval: %v", err)
	}
	evs := r.await(t, a.OperationId)
	if evs[len(evs)-1].GetOutcome().GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("outcome %v", evs[len(evs)-1])
	}
	// Approvals and asks are replayed after a restart.
	next := r.service()
	if err := next.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !next.approvedByOwner(p.Proposal.Id) {
		t.Fatal("approval lost on restart")
	}
	if _, err := next.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: ask.AskId, Answer: "deny"}); code(err) != FailedPrecondition {
		t.Fatalf("closed ask reopened after restart: %v", err)
	}
}

func TestAskValidationAndTrustedLoginNotice(t *testing.T) {
	r := newRig(t)
	n := &fakeNotifier{}
	r.svc.Notifier = n
	for name, req := range map[string]*oapv1.AskRequest{
		"no question":        {Kind: oapv1.AskKind_ASK_KIND_BLOCKER},
		"no kind":            {Question: "?"},
		"one option":         {Kind: oapv1.AskKind_ASK_KIND_DECISION, Question: "?", Options: []string{"a"}},
		"secret without ref": {Kind: oapv1.AskKind_ASK_KIND_SECRET, Question: "?"},
		"too long":           {Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "?", ExpiresInSeconds: 8 * 24 * 3600},
	} {
		if _, err := r.svc.Ask(ctx, Owner, req); code(err) != InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	s, _ := r.svc.Ask(ctx, Owner, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_SECRET, Question: "Set the database URL", SecretRef: "db-url"})
	if _, err := r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: s.AskId, Answer: "postgres://u:p@db"}); code(err) != InvalidArgument {
		t.Fatalf("a secret value was accepted as an answer: %v", err)
	}
	k := newKey(t)
	login(t, r, k.request("claude", r.clock.Now(), nonce()), approveSite(t, r, true))
	n.sent = nil
	login(t, r, k.request("claude", r.clock.Now(), nonce()), nil)
	if len(n.sent) != 1 || n.sent[0].Kind != "login" || !strings.Contains(n.sent[0].Subject, "claude signed in") {
		t.Fatalf("trusted login notice: %v", n.sent)
	}
}
