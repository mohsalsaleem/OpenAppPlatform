package service

import (
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

func installIntent(name string) *oapv1.Intent {
	return &oapv1.Intent{Change: &oapv1.Intent_InstallExtension{InstallExtension: &oapv1.InstallExtension{
		Name: name, Path: "/var/lib/oap/extensions/" + name, Sha256: strings.Repeat("ab", 32),
		Points: []string{"notifier"}, Settings: map[string]string{"channel": "ops"}, Secrets: []string{"smtp-password"}}}}
}

func extensionRig(t *testing.T, envelope string) (*rig, *sim.Extensions) {
	t.Helper()
	r := newRig(t)
	env, _ := spec.FromJSON([]byte(envelope))
	r.desired.SetEnvelope(ctx, env, "owner", "test envelope")
	x := &sim.Extensions{}
	r.svc.Extensions = x
	return r, x
}

const extensionEnvelope = `{"agents": {
  "owner": {"scope": {"applications": ["*"]}, "may": ["scale", "spec-patch", "install-extension", "remove-extension"]},
  "claude": {"scope": {"applications": ["site"]}, "may": ["scale", "install-extension"]},
  "ops": {"scope": {"applications": ["*"]}, "may": ["scale", "install-extension", "remove-extension"]}}}`

// TestInstallingAnExtensionIsSensitive: an extension install is a proposal
// judged by the envelope. By default it is sensitive: an agent may propose
// it but not apply it, an agent scoped to one application can't propose it
// at all, and the owner's install runs, starts and is verified.
func TestInstallingAnExtensionIsSensitive(t *testing.T) {
	r, x := extensionRig(t, extensionEnvelope)
	claude := Caller{Agent: "claude", Apps: []string{"site"}}
	ops := Caller{Agent: "ops", Apps: []string{"*"}}

	if _, err := r.svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: installIntent("mailer")}); code(err) != PermissionDenied {
		t.Fatalf("an agent scoped to one application proposed an extension: %v", err)
	}
	p, err := r.svc.Propose(ctx, ops, &oapv1.ProposeRequest{Intent: installIntent("mailer")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Proposal.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL || p.Proposal.Risk != oapv1.Risk_RISK_SENSITIVE ||
		!strings.Contains(reasons(p.Proposal), "extensions") {
		t.Fatalf("verdict %v, risk %v: %s", p.Proposal.Verdict, p.Proposal.Risk, reasons(p.Proposal))
	}
	if _, err := r.svc.Apply(ctx, ops, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id}); code(err) != PermissionDenied {
		t.Fatalf("an agent applied a sensitive extension install: %v", err)
	}
	if len(x.Installed) != 0 {
		t.Fatal("refused install reached the host")
	}

	op := r.applyAsOwner(t, installIntent("mailer"))
	if st := r.outcomeOf(t, op); st != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("owner's install: %v", st)
	}
	if x.Verified["mailer"] != 1 {
		t.Fatalf("not started and verified: %v", x.Verified)
	}
	tree, _, _ := r.desired.Extensions(ctx)
	if all, _ := spec.ParseExtensions(tree); all["mailer"].SHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("desired extensions: %v", tree)
	}

	// The same install again changes nothing; a bad one is refused.
	if _, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: installIntent("mailer")}); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("repeated install: %v", err)
	}
	bad := installIntent("mailer2")
	bad.GetInstallExtension().Sha256 = "nope"
	if _, err := r.svc.Propose(ctx, Owner, &oapv1.ProposeRequest{Intent: bad}); code(err) != InvalidArgument {
		t.Fatalf("bad digest: %v", err)
	}

	// Removing is a proposal too, and stops it.
	rm := &oapv1.Intent{Change: &oapv1.Intent_RemoveExtension{RemoveExtension: &oapv1.RemoveExtension{Name: "mailer"}}}
	if st := r.outcomeOf(t, r.applyAsOwner(t, rm)); st != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("remove: %v", st)
	}
	if _, ok := x.Installed["mailer"]; ok {
		t.Fatal("removed extension still installed")
	}
}

// TestOwnerMayRelaxExtensions: an envelope that makes the extensions
// category routine lets an agent with the action install one itself.
func TestOwnerMayRelaxExtensions(t *testing.T) {
	r, x := extensionRig(t, strings.Replace(extensionEnvelope, `"agents"`, `"risk": {"routine": ["extensions"]}, "agents"`, 1))
	ops := Caller{Agent: "ops", Apps: []string{"*"}}
	p, err := r.svc.Propose(ctx, ops, &oapv1.ProposeRequest{Intent: installIntent("mailer")})
	if err != nil || p.Proposal.Verdict != oapv1.Verdict_VERDICT_ALLOWED {
		t.Fatalf("relaxed envelope: %v %v", p.GetProposal().GetVerdict(), err)
	}
	a, err := r.svc.Apply(ctx, ops, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	if st := r.outcomeOf(t, a.OperationId); st != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED || x.Verified["mailer"] != 1 {
		t.Fatalf("agent's install: %v", st)
	}
}

// TestBrokenExtensionIsRolledBack: an extension that doesn't start as
// granted fails its operation, and the desired state goes back to before;
// a restart loads the installed extensions into the host.
func TestBrokenExtensionIsRolledBack(t *testing.T) {
	r, x := extensionRig(t, extensionEnvelope)
	r.outcomeOf(t, r.applyAsOwner(t, installIntent("good")))
	x.Broken = map[string]bool{"bad": true}
	if st := r.outcomeOf(t, r.applyAsOwner(t, installIntent("bad"))); st != oapv1.OutcomeStatus_OUTCOME_STATUS_NOT_ACHIEVED {
		t.Fatalf("broken extension: %v", st)
	}
	tree, _, _ := r.desired.Extensions(ctx)
	all, _ := spec.ParseExtensions(tree)
	if _, ok := all["bad"]; ok || len(all) != 1 {
		t.Fatalf("desired extensions after a failed install: %v", tree)
	}
	if _, ok := x.Installed["bad"]; ok {
		t.Fatal("the broken extension stays registered")
	}

	next := r.service()
	fresh := &sim.Extensions{}
	next.Extensions = fresh
	if err := next.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := fresh.Installed["good"]; !ok || len(fresh.Installed) != 1 {
		t.Fatalf("after a restart: %v", fresh.Installed)
	}
}

func (r *rig) applyAsOwner(t *testing.T, in *oapv1.Intent) string {
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

func (r *rig) outcomeOf(t *testing.T, op string) oapv1.OutcomeStatus {
	t.Helper()
	evs := r.await(t, op)
	return evs[len(evs)-1].GetOutcome().GetStatus()
}
