package service

import (
	"context"
	"sort"
	"strings"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/briefing"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/policy"
)

// alwaysOwner is what needs the owner whatever an agent's grants.
var alwaysOwner = []string{
	"irreversible changes (volume deletion, destructive migrations)",
	"changes the envelope treats as sensitive (domains, secret bindings, by default)",
	"anything outside your applications or actions",
}

// Briefing renders the briefing for a caller: the whole server, or one
// application, limited to what the caller may see (R3.8).
func (s *Service) Briefing(ctx context.Context, c Caller, app string) (string, error) {
	if app != "" && !c.Allows(app) {
		return "", outOfScope(c, app)
	}
	w, err := s.world(ctx)
	if err != nil {
		return "", err
	}
	if app != "" {
		found := false
		for _, a := range w.Applications {
			found = found || a.Name == app
		}
		if !found {
			return "", fail(NotFound, WhyUnknownApp, "no application %q", app)
		}
	}
	if !c.Owner {
		var keep []*oapv1.Application
		for _, a := range w.Applications {
			if c.Allows(a.Name) {
				keep = append(keep, a)
			}
		}
		w.Applications = keep
	}
	if env, err := s.envelope(ctx); err == nil {
		policy.Affordances(env, c.Agent, w)
	}
	in := briefing.Input{World: w, App: app, Caller: c.Agent, Owner: c.Owner, Apps: c.Apps, AlwaysOwner: alwaysOwner}
	if !c.Owner {
		id := s.ids()
		id.mu.Lock()
		if a, ok := id.agents[c.Agent]; ok {
			in.May = append([]string(nil), a.may...)
		}
		id.mu.Unlock()
	}
	in.Changes, err = s.recentChanges(ctx, c)
	if err != nil {
		return "", err
	}
	b := s.book()
	b.mu.Lock()
	now := s.Clock.Now()
	for _, q := range b.asks {
		if !q.closed && now.Before(q.expires) && (c.Owner || q.agent == c.Agent) {
			in.Asks = append(in.Asks, briefing.Ask{ID: q.id, Agent: q.agent, Question: q.req.GetQuestion(), Expires: q.expires})
		}
	}
	b.mu.Unlock()
	sort.Slice(in.Asks, func(i, j int) bool { return in.Asks[i].Expires.Before(in.Asks[j].Expires) })
	if note, err := s.Desired.Briefing(ctx, ""); err == nil && note != "" {
		in.Notes = append(in.Notes, briefing.Note{Markdown: note})
	}
	for _, a := range w.Applications {
		if note, err := s.Desired.Briefing(ctx, a.Name); err == nil && note != "" {
			in.Notes = append(in.Notes, briefing.Note{App: a.Name, Markdown: note})
		}
	}
	return briefing.Render(in), nil
}

// recentChanges lists applied operations the caller may see, with their
// outcomes.
func (s *Service) recentChanges(ctx context.Context, c Caller) ([]briefing.Change, error) {
	entries, err := s.entries(ctx, 0)
	if err != nil {
		return nil, err
	}
	outcomes := map[string]string{}
	for _, e := range entries {
		if e.Kind == KindOutcome {
			if je, err := decode(e.Data); err == nil {
				outcomes[e.Operation] = strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(je.GetOutcome().GetStatus().String(), "OUTCOME_STATUS_"), "_", " "))
			}
		}
	}
	var out []briefing.Change
	for _, e := range entries {
		if e.Kind != KindApply {
			continue
		}
		je, err := decode(e.Data)
		if err != nil {
			continue
		}
		p, err := s.proposal(ctx, je.GetParentId())
		if err != nil {
			continue
		}
		app := p.GetIntent().GetApplication()
		if !c.Allows(app) {
			continue
		}
		by := je.GetActor().GetAgent()
		if je.GetActor().GetOwner() != "" {
			by = "owner"
		}
		outcome := outcomes[e.Operation]
		if outcome == "" {
			outcome = "running"
		}
		out = append(out, briefing.Change{At: e.At, App: app, Summary: summary(p.GetIntent()), By: by, Outcome: outcome})
	}
	return out, nil
}

// SetBriefing stores the owner's house rules.
func (s *Service) SetBriefing(ctx context.Context, c Caller, req *oapv1.SetBriefingRequest) (*oapv1.SetBriefingResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	if len(req.GetMarkdown()) > 16<<10 {
		return nil, fail(InvalidArgument, "briefing.too_long", "briefing.md is at most 16 KiB")
	}
	if err := s.Desired.SetBriefing(ctx, req.GetApplication(), req.GetMarkdown(), "owner"); err != nil {
		return nil, err
	}
	return &oapv1.SetBriefingResponse{}, nil
}
