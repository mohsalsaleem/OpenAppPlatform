package service

import (
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
)

// TestBriefingIsScopedToTheCaller: an agent's briefing covers only its
// applications, its grants and its own asks, with the owner's house rules
// labelled; observe returns the same briefing.
func TestBriefingIsScopedToTheCaller(t *testing.T) {
	r := newRig(t)
	r.create(t)
	blog := strings.Replace(strings.Replace(siteJSON, `"name": "site"`, `"name": "blog"`, 1), "site.example.com", "blog.example.com", 1)
	p, _ := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: patch(t, "blog", blog)})
	a, _ := r.svc.Apply(ctx, Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	r.await(t, a.OperationId)
	r.svc.SetBriefing(ctx, Owner, &oapv1.SetBriefingRequest{Markdown: "Deploy only on weekdays."})
	r.svc.SetBriefing(ctx, Owner, &oapv1.SetBriefingRequest{Application: "site", Markdown: "Keep site at 1 instance at night."})
	r.svc.SetBriefing(ctx, Owner, &oapv1.SetBriefingRequest{Application: "blog", Markdown: "The blog is frozen."})
	if _, err := r.svc.SetBriefing(ctx, Caller{Agent: "x", Apps: []string{"*"}}, &oapv1.SetBriefingRequest{Markdown: "hijack"}); code(err) != PermissionDenied {
		t.Fatalf("an agent set the house rules: %v", err)
	}

	r.svc.Observer.Resync(ctx)
	claude := agentCaller(t, r, "claude", "site")
	r.svc.Ask(ctx, claude, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "Need a DNS record"})
	r.svc.Ask(ctx, Owner, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "Owner's own reminder"})
	md, err := r.svc.Briefing(ctx, claude, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### site", "You are agent `claude`. Applications: site. Actions: scale.", "create site by owner, achieved",
		"Need a DNS record", "## House rules for the server (written by the owner)", "> Deploy only on weekdays.",
		"## House rules for site (written by the owner)", "Always needs the owner"} {
		if !strings.Contains(md, want) {
			t.Errorf("agent briefing lacks %q", want)
		}
	}
	for _, not := range []string{"### blog", "The blog is frozen", "create blog", "Owner's own reminder"} {
		if strings.Contains(md, not) {
			t.Errorf("agent briefing shows %q", not)
		}
	}
	if _, err := r.svc.Briefing(ctx, claude, "blog"); code(err) != PermissionDenied {
		t.Fatalf("out-of-scope briefing: %v", err)
	}
	o, err := r.svc.Observe(ctx, claude, &oapv1.ObserveRequest{Application: "site", Briefing: true})
	if err != nil || !strings.Contains(o.Briefing, "# OAP briefing: sim · site") {
		t.Fatalf("observe briefing: %v %q", err, o.GetBriefing())
	}
	own, _ := r.svc.Briefing(ctx, Owner, "")
	if !strings.Contains(own, "### blog") || !strings.Contains(own, "Owner's own reminder") || strings.Contains(own, "Always needs the owner") {
		t.Fatalf("owner briefing:\n%s", own)
	}
	if _, err := r.svc.Briefing(ctx, Owner, "nope"); code(err) != NotFound {
		t.Fatalf("unknown app: %v", err)
	}
}
