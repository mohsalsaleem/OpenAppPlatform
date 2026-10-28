package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

type stubBuilder struct {
	got  []ops.BuildRequest
	fail error
}

func (b *stubBuilder) Build(ctx context.Context, req ops.BuildRequest) (ops.BuildResult, error) {
	b.got = append(b.got, req)
	if b.fail != nil {
		return ops.BuildResult{}, b.fail
	}
	return ops.BuildResult{Image: req.Image + ":3f2a9c1e0b7d", Commit: "3f2a9c1e0b7d5e6f"}, nil
}

func buildIntent(app, component, repo string) *oapv1.Intent {
	return &oapv1.Intent{Application: app, Change: &oapv1.Intent_Build{Build: &oapv1.Build{
		Component: component, Repository: repo, Commit: "main"}}}
}

// TestBuildIsARoutineProposal: an agent that may build, in its
// application's scope, builds without the owner; the outcome names the
// image; nothing in the desired state changes. Out of scope, without the
// action, or with bad input it doesn't build.
func TestBuildIsARoutineProposal(t *testing.T) {
	r, _ := extensionRig(t, `{"agents": {
  "owner": {"scope": {"applications": ["*"]}, "may": ["spec-patch", "build"]},
  "claude": {"scope": {"applications": ["site"]}, "may": ["scale", "build"]},
  "viewer": {"scope": {"applications": ["site"]}, "may": ["scale"]}}}`)
	b := &stubBuilder{}
	r.svc.Builder = b
	r.create(t)
	_, before, _ := r.desired.Get(ctx, "site")
	claude := Caller{Agent: "claude", Apps: []string{"site"}}

	p, err := r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: buildIntent("site", "web", "https://github.com/acme/site.git")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Proposal.Verdict != oapv1.Verdict_VERDICT_ALLOWED || p.Proposal.Risk != oapv1.Risk_RISK_ROUTINE {
		t.Fatalf("build: %v %v %s", p.Proposal.Verdict, p.Proposal.Risk, reasons(p.Proposal))
	}
	a, err := r.svc.Apply(ctx, claude, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	evs := r.await(t, a.OperationId)
	o := evs[len(evs)-1].GetOutcome()
	if o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED || o.GetImages()["web"] != "oap-build/site-web:3f2a9c1e0b7d" {
		t.Fatalf("outcome %v", o)
	}
	if got := b.got[0]; got.Repository != "https://github.com/acme/site.git" || got.Context != "." || got.Dockerfile != "Dockerfile" || got.MemoryMiB != 512 {
		t.Fatalf("build request %+v", got)
	}
	if _, after, _ := r.desired.Get(ctx, "site"); after != before {
		t.Fatal("a build changed the desired state")
	}

	if _, err := r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: buildIntent("blog", "web", "https://x/y.git")}); code(err) != PermissionDenied {
		t.Fatalf("out of scope: %v", err)
	}
	viewer := Caller{Agent: "viewer", Apps: []string{"site"}}
	if p, err := r.svc.Propose(ctx, viewer, &oapv1.ProposeRequest{Intent: buildIntent("site", "web", "https://x/y.git")}); err != nil || p.Proposal.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
		t.Fatalf("without the build action: %v %v", p.GetProposal().GetVerdict(), err)
	}
	for _, bad := range []*oapv1.Intent{buildIntent("site", "nope", "https://x/y.git"), buildIntent("site", "web", "not a url; rm -rf /")} {
		if _, err := r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: bad}); code(err) != InvalidArgument {
			t.Errorf("bad build %v: %v", bad.GetBuild(), err)
		}
	}

	b.fail = errors.New("compile error in main.go")
	p, _ = r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: buildIntent("site", "web", "https://github.com/acme/site.git")})
	a, _ = r.svc.Apply(ctx, claude, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	evs = r.await(t, a.OperationId)
	if o := evs[len(evs)-1].GetOutcome(); o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_NOT_ACHIEVED ||
		!strings.Contains(o.GetReasons()[0].GetMessage(), "compile error") || len(o.GetImages()) != 0 {
		t.Fatalf("failed build outcome %v", o)
	}
}
