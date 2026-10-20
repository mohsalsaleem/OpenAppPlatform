package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Asks (R3.7): an agent's structured way to involve the owner. An ask is
// recorded in the journal and sent to the owner through the notifier; the
// owner's answer, or its expiry, is another journal entry, so both reach
// the agent through watch. An expired ask can't be answered. Answering an
// approval ask with "approve" lets the agent apply that proposal.

// Journal entry kinds for asks.
const (
	KindAsk       = "ask"
	KindAskAnswer = "ask_answer"
)

// Why codes for asks.
const (
	WhyAskExpired  = "ask.expired"
	WhyAskAnswered = "ask.answered"
	WhyUnknownAsk  = "ask.unknown"
	WhyBadAnswer   = "ask.bad_answer"
	WhyBadAsk      = "ask.bad_request"
)

// Ask lifetimes.
const (
	DefaultAskTTL = time.Hour
	MinAskTTL     = time.Minute
	MaxAskTTL     = 7 * 24 * time.Hour
)

type askState struct {
	id, agent, app string
	req            *oapv1.AskRequest
	expires        time.Time
	closed         bool // answered or expired
	approved       bool
}

type askBook struct {
	mu   sync.Mutex
	asks map[string]*askState
}

func (s *Service) book() *askBook {
	s.askOnce.Do(func() { s.asks = &askBook{asks: map[string]*askState{}} })
	return s.asks
}

// loadAsks replays asks and answers from the journal.
func (s *Service) loadAsks(entries []ops.Entry) {
	b := s.book()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range entries {
		je, err := decode(e.Data)
		if err != nil {
			continue
		}
		switch e.Kind {
		case KindAsk:
			a := &askState{id: je.GetId(), agent: je.GetActor().GetAgent(), req: je.GetAsk()}
			a.expires = e.At.Add(time.Duration(a.req.GetExpiresInSeconds()) * time.Second)
			if p, err := s.proposal(context.Background(), a.req.GetProposalId()); err == nil {
				a.app = p.GetIntent().GetApplication()
			}
			b.asks[a.id] = a
		case KindAskAnswer:
			if a, ok := b.asks[je.GetParentId()]; ok {
				a.closed = true
				a.approved = a.req.GetKind() == oapv1.AskKind_ASK_KIND_APPROVAL && je.GetAnswer() == "approve"
			}
		}
	}
}

func (s *Service) tellOwner(ctx context.Context, n ops.Notification) {
	if s.Notifier != nil {
		s.Notifier.Notify(ctx, n) // a failed notification doesn't fail the ask
	}
}

// Ask records an agent's ask and notifies the owner.
func (s *Service) Ask(ctx context.Context, c Caller, req *oapv1.AskRequest) (*oapv1.AskResponse, error) {
	if strings.TrimSpace(req.GetQuestion()) == "" {
		return nil, fail(InvalidArgument, WhyBadAsk, "an ask needs a question")
	}
	ttl := time.Duration(req.GetExpiresInSeconds()) * time.Second
	if ttl == 0 {
		ttl = DefaultAskTTL
	}
	if ttl < MinAskTTL || ttl > MaxAskTTL {
		return nil, fail(InvalidArgument, WhyBadAsk, "an ask expires in 1 minute to 7 days")
	}
	app := ""
	switch req.GetKind() {
	case oapv1.AskKind_ASK_KIND_APPROVAL:
		p, err := s.proposal(ctx, req.GetProposalId())
		if err != nil {
			return nil, err
		}
		app = p.GetIntent().GetApplication()
		if !c.Allows(app) {
			return nil, outOfScope(c, app)
		}
		if p.GetVerdict() != oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
			return nil, fail(FailedPrecondition, WhyBadAsk, "proposal %s doesn't need approval (verdict %s)", p.GetId(), p.GetVerdict())
		}
	case oapv1.AskKind_ASK_KIND_DECISION:
		if len(req.GetOptions()) < 2 {
			return nil, fail(InvalidArgument, WhyBadAsk, "a decision offers at least two options")
		}
	case oapv1.AskKind_ASK_KIND_SECRET:
		if req.GetSecretRef() == "" {
			return nil, fail(InvalidArgument, WhyBadAsk, "a secret ask names the secret reference to fill")
		}
	case oapv1.AskKind_ASK_KIND_BLOCKER:
	default:
		return nil, fail(InvalidArgument, WhyBadAsk, "choose a kind: approval, secret, decision or blocker")
	}
	stored := proto.Clone(req).(*oapv1.AskRequest)
	stored.ExpiresInSeconds = int32(ttl.Seconds())
	id := "ask_" + randomString(9)
	now := s.Clock.Now()
	if _, err := s.Journal.Append(ctx, ops.Entry{At: now, Kind: KindAsk, Operation: id, Data: mustEntry(&oapv1.JournalEntry{
		Id: id, Kind: oapv1.EntryKind_ENTRY_KIND_ASK, Actor: actor(c), Summary: req.GetQuestion(),
		Detail: &oapv1.JournalEntry_Ask{Ask: stored}})}); err != nil {
		return nil, err
	}
	b := s.book()
	b.mu.Lock()
	b.asks[id] = &askState{id: id, agent: c.Agent, app: app, req: stored, expires: now.Add(ttl)}
	b.mu.Unlock()
	kind := strings.ToLower(strings.TrimPrefix(req.GetKind().String(), "ASK_KIND_"))
	body := fmt.Sprintf("%s (%s, expires %s)\nAnswer: oapctl owner answer %s ANSWER", req.GetQuestion(), kind, now.Add(ttl).UTC().Format(time.RFC3339), id)
	if len(req.GetOptions()) > 0 {
		body += "\nOptions: " + strings.Join(req.GetOptions(), ", ")
	}
	s.tellOwner(ctx, ops.Notification{Kind: "ask", Subject: fmt.Sprintf("%s asks: %s", c.Agent, req.GetQuestion()), Body: body})
	return &oapv1.AskResponse{AskId: id}, nil
}

func mustEntry(je *oapv1.JournalEntry) []byte {
	b, _ := marshal.Marshal(je)
	return b
}

// answerEntry records an ask's answer, or its expiry.
func (s *Service) answerEntry(ctx context.Context, a *askState, answer string, by *oapv1.Actor, expired bool) error {
	summary := answer
	if expired {
		summary = "expired"
	}
	_, err := s.Journal.Append(ctx, ops.Entry{At: s.Clock.Now(), Kind: KindAskAnswer, Operation: a.id, Data: mustEntry(&oapv1.JournalEntry{
		Id: "answer_" + strings.TrimPrefix(a.id, "ask_"), ParentId: a.id, Kind: oapv1.EntryKind_ENTRY_KIND_ASK_ANSWER, Actor: by,
		Summary: summary, Detail: &oapv1.JournalEntry_Answer{Answer: answer}})})
	return err
}

// AnswerAsk answers an ask that is still open. Only the owner answers.
func (s *Service) AnswerAsk(ctx context.Context, c Caller, req *oapv1.AnswerAskRequest) (*oapv1.AnswerAskResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	b := s.book()
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.asks[req.GetAskId()]
	switch {
	case !ok:
		return nil, fail(NotFound, WhyUnknownAsk, "no ask %q", req.GetAskId())
	case a.closed:
		return nil, fail(FailedPrecondition, WhyAskAnswered, "ask %s is closed", a.id)
	case !s.Clock.Now().Before(a.expires):
		if err := s.answerEntry(ctx, a, "", &oapv1.Actor{Who: &oapv1.Actor_System{System: true}}, true); err != nil {
			return nil, err
		}
		a.closed = true
		return nil, fail(FailedPrecondition, WhyAskExpired, "ask %s expired at %s and can't be answered", a.id, a.expires.UTC().Format(time.RFC3339))
	}
	answer := strings.TrimSpace(req.GetAnswer())
	valid := func(allowed ...string) bool {
		for _, x := range allowed {
			if answer == x {
				return true
			}
		}
		return false
	}
	switch a.req.GetKind() {
	case oapv1.AskKind_ASK_KIND_APPROVAL:
		if !valid("approve", "deny") {
			return nil, fail(InvalidArgument, WhyBadAnswer, "answer an approval with approve or deny")
		}
	case oapv1.AskKind_ASK_KIND_DECISION:
		if !valid(a.req.GetOptions()...) {
			return nil, fail(InvalidArgument, WhyBadAnswer, "answer with one of: %s", strings.Join(a.req.GetOptions(), ", "))
		}
	case oapv1.AskKind_ASK_KIND_SECRET:
		if !valid("provided", "declined") {
			return nil, fail(InvalidArgument, WhyBadAnswer, "answer a secret ask with provided or declined; the value never passes through an answer")
		}
		if answer == "provided" && s.SecretStore != nil && !s.hasSecret(a.req.GetSecretRef()) {
			return nil, fail(FailedPrecondition, WhyBadAnswer, "secret %q isn't set; set it with: oapctl owner secret set %s --ask %s", a.req.GetSecretRef(), a.req.GetSecretRef(), a.id)
		}
	default:
		if answer == "" {
			return nil, fail(InvalidArgument, WhyBadAnswer, "the answer is empty")
		}
	}
	if err := s.answerEntry(ctx, a, answer, actor(c), false); err != nil {
		return nil, err
	}
	a.closed = true
	a.approved = a.req.GetKind() == oapv1.AskKind_ASK_KIND_APPROVAL && answer == "approve"
	return &oapv1.AnswerAskResponse{}, nil
}

// ListAsks returns the open asks, oldest expiry first.
func (s *Service) ListAsks(ctx context.Context, c Caller) (*oapv1.ListAsksResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	b := s.book()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := &oapv1.ListAsksResponse{}
	now := s.Clock.Now()
	for _, a := range b.asks {
		if !a.closed && now.Before(a.expires) {
			out.Asks = append(out.Asks, &oapv1.PendingAsk{AskId: a.id, Agent: a.agent, Request: a.req, ExpiresAt: timestamppb.New(a.expires)})
		}
	}
	sort.Slice(out.Asks, func(i, j int) bool { return out.Asks[i].ExpiresAt.AsTime().Before(out.Asks[j].ExpiresAt.AsTime()) })
	return out, nil
}

// ExpireAsks records the expiry of every open ask past its time. The
// service runs it every second; tests call it directly.
func (s *Service) ExpireAsks(ctx context.Context) error {
	b := s.book()
	b.mu.Lock()
	defer b.mu.Unlock()
	now := s.Clock.Now()
	for _, a := range b.asks {
		if !a.closed && !now.Before(a.expires) {
			if err := s.answerEntry(ctx, a, "", &oapv1.Actor{Who: &oapv1.Actor_System{System: true}}, true); err != nil {
				return err
			}
			a.closed = true
		}
	}
	return nil
}

// approvedByOwner reports whether the owner approved a proposal through
// an approval ask.
func (s *Service) approvedByOwner(proposalID string) bool {
	b := s.book()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, a := range b.asks {
		if a.approved && a.req.GetProposalId() == proposalID {
			return true
		}
	}
	return false
}

// askInfo names an ask's application and the agent that asked.
func (s *Service) askInfo(id string) (app, agent string) {
	b := s.book()
	b.mu.Lock()
	defer b.mu.Unlock()
	if a, ok := b.asks[id]; ok {
		return a.app, a.agent
	}
	return "", ""
}
