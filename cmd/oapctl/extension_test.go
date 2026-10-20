package main

import (
	"context"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/listeners"
)

// TestExtensionInstallThroughTheCLI (R4.1): on a server with the real
// extension host, the owner installs a notifier extension through the CLI;
// the plan says it is sensitive, and the extension starts and is checked
// against its grants. An agent allowed the action can only propose it.
// With a hanging notifier extension installed, an agent's ask still
// returns at once: extensions never hold up the core.
func TestExtensionInstallThroughTheCLI(t *testing.T) {
	bin := t.TempDir()
	for mode, name := range map[string]string{"good": "testext", "hang": "slow"} {
		out, err := exec.Command("go", "build", "-ldflags", "-X main.mode="+mode+" -X main.name="+name, "-o", filepath.Join(bin, mode),
			"../../internal/adapters/extension/testdata/testext").CombinedOutput()
		if err != nil {
			t.Fatalf("build %s: %v\n%s", mode, err, out)
		}
	}
	ctx := context.Background()
	clock := sim.NewClock(time.Now().UTC())
	rt, edge, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim"}), sim.NewEdge(), &sim.Journal{}, sim.NewDesired()
	env, _ := spec.FromJSON([]byte(`{"agents": {"owner": {"scope": {"applications": ["*"]}, "may": ["scale", "spec-patch", "install-extension", "remove-extension"]}}}`))
	desired.SetEnvelope(ctx, env, "owner", "test")
	host := &extension.Host{SecretValue: func(name string) (string, error) { return "value-of-" + name, nil }}
	t.Cleanup(host.Close)
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Extensions: host,
		Notifier: extension.Fanout{Host: host}, Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	local, public := httptest.NewServer(listeners.Socket(svc)), httptest.NewServer(listeners.Public(svc))
	t.Cleanup(local.Close)
	t.Cleanup(public.Close)
	dir := t.TempDir()
	owner := map[string]string{"OAP_AGENT_URL": local.URL}

	code, out := cli(t, dir, owner, "ext", "install", "testext", "--path", filepath.Join(bin, "good"), "--point", "notifier",
		"--setting", "channel=ops", "--secret", "token", "--yes")
	if code != 0 || !strings.Contains(out, "sensitive") || !strings.Contains(out, "extensions") {
		t.Fatalf("owner install: %d\n%s", code, out)
	}
	if pid := host.PID("testext"); pid == 0 {
		t.Fatal("the installed extension wasn't started and verified")
	}

	// An agent with the action and the server's scope can only propose it.
	agentTok := approveAgent(t, svc, "ops", []string{"*"}, []string{"install-extension"})
	agent, _ := svc.Authenticate(ctx, agentTok)
	in := &oapv1.Intent{Change: &oapv1.Intent_InstallExtension{InstallExtension: &oapv1.InstallExtension{
		Name: "slow", Path: filepath.Join(bin, "hang"), Sha256: fileDigest(t, filepath.Join(bin, "hang")), Points: []string{"notifier"}}}}
	p, err := svc.Propose(ctx, agent, &oapv1.ProposeRequest{Intent: in})
	if err != nil || p.Proposal.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL || p.Proposal.Risk != oapv1.Risk_RISK_SENSITIVE {
		t.Fatalf("agent's proposal: %v %v", p.GetProposal().GetVerdict(), err)
	}
	if _, err := svc.Apply(ctx, agent, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id}); err == nil {
		t.Fatal("an agent applied a sensitive extension install")
	}

	// The owner installs the hanging one; an agent's ask still returns at once.
	if code, out := cli(t, dir, owner, "ext", "install", "slow", "--path", filepath.Join(bin, "hang"), "--point", "notifier", "--yes"); code != 0 {
		t.Fatalf("install slow: %s", out)
	}
	start := time.Now()
	if _, err := svc.Ask(ctx, agent, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "Is anyone there?"}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("an ask waited %s for a hanging notifier extension", took)
	}

	if code, out := cli(t, dir, owner, "ext", "remove", "slow", "--yes"); code != 0 {
		t.Fatalf("remove: %s", out)
	}
	tree, _, _ := desired.Extensions(ctx)
	if all, _ := spec.ParseExtensions(tree); len(all) != 1 || all["testext"].Name != "testext" {
		t.Fatalf("desired extensions: %v", tree)
	}
}

// approveAgent signs an agent in through device login and approves it.
func approveAgent(t *testing.T, svc *service.Service, name string, apps, may []string) string {
	t.Helper()
	ctx := context.Background()
	codes := make(chan string, 1)
	tok := make(chan string, 1)
	go svc.Login(ctx, &oapv1.LoginRequest{Agent: name}, func(r *oapv1.LoginResponse) error {
		if p := r.GetPending(); p != nil {
			codes <- p.UserCode
		}
		if a := r.GetApproved(); a != nil {
			tok <- a.Token
		}
		return nil
	})
	if _, err := svc.ApproveLogin(ctx, service.Owner, &oapv1.ApproveLoginRequest{UserCode: <-codes, Applications: apps, May: may}); err != nil {
		t.Fatal(err)
	}
	return <-tok
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("shasum", "-a", "256", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))[0]
}
