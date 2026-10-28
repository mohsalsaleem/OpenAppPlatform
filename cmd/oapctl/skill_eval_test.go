package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/desiredyaml"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"google.golang.org/protobuf/types/known/structpb"
)

// R3.16's acceptance: fresh agents, given only the oap-author-app skill
// package and one reference repository each, produced these specs against
// vultr_oap (propose only). The expectations below were fixed before the
// agents ran.
var skillEvals = []struct {
	repo, app, domain string
	secretVars        []string // must be under secrets, never env
	kinds             []string // component kinds the spec must have
}{
	{"notes-go-postgres", "notes", "notes.139-180-132-23.sslip.io", []string{"DATABASE_URL", "SESSION_SECRET"}, []string{"web", "data", "job"}},
	{"shop-node-worker", "shop", "shop.example.com", []string{"STRIPE_SECRET_KEY", "SMTP_PASSWORD"}, []string{"web", "worker", "data"}},
}

func TestSkillEvalSpecsAccepted(t *testing.T) {
	base := "../../internal/core/skills/testdata"
	for _, ev := range skillEvals {
		t.Run(ev.repo, func(t *testing.T) {
			out := filepath.Join(base, "agent-runs", ev.repo)
			raw, err := os.ReadFile(filepath.Join(out, "oap.yaml"))
			if err != nil {
				t.Fatal(err)
			}

			// A spec OAP accepts.
			tree, probs := desiredyaml.Parse(raw)
			if len(probs) > 0 {
				t.Fatalf("not valid YAML for OAP: %v", probs)
			}
			app, probs := spec.ParseApplication(tree)
			if len(probs) > 0 {
				t.Fatalf("not a valid spec: %v", probs)
			}
			if app.Name != ev.app || app.Ingress == nil || app.Ingress.Domain != ev.domain {
				t.Errorf("name %q, ingress %+v; want %s at %s", app.Name, app.Ingress, ev.app, ev.domain)
			}
			proposeAsOwner(t, ev.app, tree)

			// The shape the repository needs.
			kinds := map[string]bool{}
			for _, c := range app.Components {
				kinds[c.Kind] = true
				if c.Kind == "data" && len(c.Volumes) == 0 {
					t.Error("a data component has no volume")
				}
			}
			for _, k := range ev.kinds {
				if !kinds[k] {
					t.Errorf("no %s component", k)
				}
			}

			// Secrets are references, and no secret value appears anywhere.
			for _, v := range ev.secretVars {
				ref := false
				for name, c := range app.Components {
					if _, ok := c.Env[v]; ok {
						t.Errorf("%s puts %s in env", name, v)
					}
					_, ok := c.Secrets[v]
					ref = ref || ok
				}
				if !ref {
					t.Errorf("%s isn't a secret reference", v)
				}
			}
			canaries := envCanaries(t, filepath.Join(base, "repos", ev.repo, ".env"))
			filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				b, _ := os.ReadFile(p)
				for _, c := range canaries {
					if strings.Contains(string(b), c) {
						t.Errorf("%s holds a secret value from the repository's .env", filepath.Base(p))
					}
				}
				return nil
			})
		})
	}
}

// envCanaries returns the values in a .env file that contain "canary",
// and the password inside a canary URL.
func envCanaries(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		_, v, ok := strings.Cut(l, "=")
		if !ok || !strings.Contains(v, "canary") {
			continue
		}
		out = append(out, v)
		for _, f := range strings.FieldsFunc(v, func(r rune) bool { return strings.ContainsRune(":/@?", r) }) {
			if strings.Contains(f, "canary") {
				out = append(out, f)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no canaries; the scan would prove nothing")
	}
	return out
}

// proposeAsOwner proposes creating the application on a fresh simulated
// server, as the owner: OAP must accept the spec.
func proposeAsOwner(t *testing.T, app string, tree any) {
	t.Helper()
	ctx := context.Background()
	clock := sim.NewClock(time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC))
	rt, edge, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim", MemoryTotalMiB: 950}), sim.NewEdge(), &sim.Journal{}, sim.NewDesired()
	env, _ := spec.FromJSON([]byte(`{"agents": {"owner": {"scope": {"applications": ["*"]}, "may": ["scale", "deploy", "rollback", "configure", "add-component", "remove-component", "spec-patch"]}}}`))
	desired.SetEnvelope(ctx, env, "owner", "test envelope")
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	m := spec.Clone(tree).(map[string]any)
	for _, k := range []string{"api_version", "kind", "name"} {
		delete(m, k) // as oapctl app apply sends it: identity names the application
	}
	patch, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Propose(ctx, service.Owner, &oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: app,
		Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: patch}}}})
	if err != nil {
		t.Fatalf("OAP refused the spec: %v", err)
	}
	if p.Proposal.Verdict == oapv1.Verdict_VERDICT_DENIED {
		t.Fatalf("denied: %v", p.Proposal.Reasons)
	}
}
