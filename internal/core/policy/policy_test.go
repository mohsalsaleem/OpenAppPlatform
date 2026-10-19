package policy

import (
	"math/rand/v2"
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/plan"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const shop = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "shop", "environment": "production",
  "ingress": {"domain": "shop.example.com", "routes": {"/": {"component": "web"}}},
  "components": {
    "web": {"kind": "web", "image": "reg/web:1", "port": 8080, "instances": 2},
    "worker": {"kind": "worker", "image": "reg/worker:1"},
    "migrate": {"kind": "job", "image": "reg/migrate:1"},
    "db": {"kind": "data", "image": "postgres:17", "port": 5432, "volumes": {"pgdata": "/data"},
           "resources": {"memory_mib": 256, "cpu_millis": 500}}}}`

const envelopeJSON = `{
  "agents": {
    "claude": {"scope": {"applications": ["shop"], "components": ["web", "worker", "migrate"]},
               "may": ["scale", "deploy", "rollback", "configure"]},
    "ops": {"scope": {"applications": ["*"]}, "may": ["scale", "deploy", "rollback", "configure", "add-component", "remove-component", "spec-patch"]}
  },
  "invariants": [
    {"min_healthy": {"application": "shop", "component": "web", "instances": 1}},
    {"snapshot_before_volume_change": true},
    {"max_releases": {"per_application_per_hour": 3}},
    {"within_quotas": true}
  ],
  "risk": {"sensitive": ["secret-bindings"]},
  "change_facts": {"ci-not-green": "deny", "migrations-changed": "sensitive", "docs-only": "routine", "no-change-facts": "routine"},
  "breakers": {"failed_applies_per_hour": 3}
}`

func tree(t testing.TB, s string) any {
	t.Helper()
	v, probs := spec.FromJSON([]byte(s))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return v
}

func envelope(t testing.TB, s string) *oapv1.Envelope {
	t.Helper()
	env, probs := ParseEnvelope(tree(t, s))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return env
}

func on(change any) *oapv1.Intent {
	in := &oapv1.Intent{Application: "shop"}
	switch c := change.(type) {
	case *oapv1.Scale:
		in.Change = &oapv1.Intent_Scale{Scale: c}
	case *oapv1.Deploy:
		in.Change = &oapv1.Intent_Deploy{Deploy: c}
	case *oapv1.Configure:
		in.Change = &oapv1.Intent_Configure{Configure: c}
	case *oapv1.RemoveComponent:
		in.Change = &oapv1.Intent_RemoveComponent{RemoveComponent: c}
	case *oapv1.SpecPatch:
		in.Change = &oapv1.Intent_SpecPatch{SpecPatch: c}
	}
	return in
}

// proposal compiles and plans an intent against the shop app and returns the
// policy input for an agent.
func proposal(t testing.TB, agent string, env *oapv1.Envelope, in *oapv1.Intent, base string) Input {
	t.Helper()
	cur := tree(t, base)
	res, err := intent.Compile(intent.Current{Tree: cur}, in)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := spec.ParseApplication(cur)
	oldHash, _ := spec.Hash(cur)
	p := plan.Build(plan.Input{Application: "shop", Old: old, OldTree: cur, OldHash: oldHash, New: res.App,
		NewTree: res.Tree, Rollback: res.Rollback, DeleteVolumes: res.DeleteVolumes, Intent: in})
	return Input{Agent: agent, Envelope: env, Kind: string(res.Kind), Changes: res.Changes, Old: old, New: res.App,
		Irreversible: p.Irreversible,
		Proposal:     &oapv1.Proposal{Intent: in, Steps: p.Steps, BlastRadius: p.BlastRadius}}
}

func whys(d Decision) string {
	var out []string
	for _, r := range d.Reasons {
		out = append(out, r.Why)
	}
	return strings.Join(out, ",")
}

func TestEvaluate(t *testing.T) {
	env := envelope(t, envelopeJSON)
	green := &oapv1.ChangeFacts{Ci: "green", Reviewed: true, Signed: true}
	cases := []struct {
		name    string
		agent   string
		in      *oapv1.Intent
		base    string
		edit    func(*Input)
		verdict oapv1.Verdict
		risk    oapv1.Risk
		why     string
	}{
		{"routine scale", "claude", on(&oapv1.Scale{Component: "web", Instances: 3}), shop, nil,
			oapv1.Verdict_VERDICT_ALLOWED, oapv1.Risk_RISK_ROUTINE, WhyWithinEnvelope},
		{"unknown agent", "stranger", on(&oapv1.Scale{Component: "web", Instances: 3}), shop, nil,
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyUnknownAgent},
		{"paused agent", "claude", on(&oapv1.Scale{Component: "web", Instances: 3}), shop,
			func(in *Input) { in.Counters.Paused = true },
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyAgentPaused},
		{"breaker open", "claude", on(&oapv1.Scale{Component: "web", Instances: 3}), shop,
			func(in *Input) { in.Counters.FailedAppliesLastHour = 3 },
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyBreakerOpen},
		{"application out of scope", "claude", on(&oapv1.Scale{Component: "web", Instances: 3}), shop,
			func(in *Input) { in.Proposal.Intent.Application = "blog" },
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyApplicationScope},
		{"component out of scope", "claude", on(&oapv1.Deploy{Images: map[string]string{"db": "postgres:18"}}), shop,
			func(in *Input) { in.Proposal.ChangeFacts = green },
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyComponentScope},
		{"action not allowed", "claude", on(&oapv1.SpecPatch{MergePatch: patch(t, map[string]any{
			"components": map[string]any{"web": map[string]any{"instances": 3}}})}), shop, nil,
			oapv1.Verdict_VERDICT_NEEDS_APPROVAL, oapv1.Risk_RISK_SENSITIVE, WhyNotAllowedAction},
		{"min healthy after change", "ops", on(&oapv1.RemoveComponent{Component: "worker"}), shop,
			func(in *Input) {
				in.Envelope = envelope(t, strings.Replace(envelopeJSON, `"component": "web", "instances": 1`, `"component": "worker", "instances": 1`, 1))
			},
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_SENSITIVE, WhyMinHealthy},
		{"min healthy during plan", "ops", on(&oapv1.Deploy{Images: map[string]string{"db": "postgres:18"}}), shop,
			func(in *Input) {
				in.Proposal.ChangeFacts = green
				in.Envelope = envelope(t, strings.Replace(envelopeJSON, `"component": "web", "instances": 1`, `"component": "db", "instances": 1`, 1))
			},
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyMinHealthy},
		{"max releases", "claude", on(&oapv1.Deploy{Images: map[string]string{"web": "reg/web:2"}}), shop,
			func(in *Input) { in.Counters.ReleasesLastHour = 3; in.Proposal.ChangeFacts = green },
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyMaxReleases},
		{"max releases ignores scale-down", "claude", on(&oapv1.Scale{Component: "web", Instances: 1}), shop,
			func(in *Input) { in.Counters.ReleasesLastHour = 3 },
			oapv1.Verdict_VERDICT_ALLOWED, oapv1.Risk_RISK_ROUTINE, WhyWithinEnvelope},
		{"over quota", "ops", on(&oapv1.Scale{Component: "web", Instances: 3}), shop,
			func(in *Input) { in.MemoryBudgetMiB = 200 },
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyQuota},
		{"sensitive category", "claude", on(&oapv1.Configure{Component: "web", MemoryLimitMib: proto.Int32(128), CpuMillis: proto.Int32(100)}), shop, nil,
			oapv1.Verdict_VERDICT_NEEDS_APPROVAL, oapv1.Risk_RISK_SENSITIVE, WhySensitiveCategory},
		{"ci not green", "claude", on(&oapv1.Deploy{Images: map[string]string{"web": "reg/web:2"}}), shop,
			func(in *Input) { in.Proposal.ChangeFacts = &oapv1.ChangeFacts{Ci: "red", Reviewed: true, Signed: true} },
			oapv1.Verdict_VERDICT_DENIED, oapv1.Risk_RISK_ROUTINE, WhyFact},
		{"migrations changed", "claude", on(&oapv1.Deploy{Images: map[string]string{"web": "reg/web:2"}}), shop,
			func(in *Input) {
				in.Proposal.ChangeFacts = &oapv1.ChangeFacts{Ci: "green", Reviewed: true, Signed: true, MigrationsChanged: true}
			},
			oapv1.Verdict_VERDICT_NEEDS_APPROVAL, oapv1.Risk_RISK_SENSITIVE, WhyFact},
		{"deploy without facts follows the envelope", "claude", on(&oapv1.Deploy{Images: map[string]string{"web": "reg/web:2"}}), shop, nil,
			oapv1.Verdict_VERDICT_ALLOWED, oapv1.Risk_RISK_ROUTINE, WhyWithinEnvelope},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := proposal(t, c.agent, env, c.in, c.base)
			if c.edit != nil {
				c.edit(&in)
			}
			d := Evaluate(in)
			if d.Verdict != c.verdict || d.Risk != c.risk || !strings.Contains(whys(d), c.why) {
				t.Fatalf("got %v %v %s; want %v %v with %s\n%v", d.Verdict, d.Risk, whys(d), c.verdict, c.risk, c.why, d.Reasons)
			}
			wantNext := map[oapv1.Verdict]string{oapv1.Verdict_VERDICT_ALLOWED: "apply", oapv1.Verdict_VERDICT_NEEDS_APPROVAL: "ask"}[c.verdict]
			if strings.Join(d.Next, ",") != wantNext {
				t.Fatalf("next: %v", d.Next)
			}
		})
	}
}

func patch(t testing.TB, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIrreversibleAlwaysNeedsTheOwner(t *testing.T) {
	// The most permissive envelope possible: every category routine, every action allowed.
	permissive := envelope(t, `{
	  "agents": {"ops": {"scope": {"applications": ["*"], "components": ["*"]},
	    "may": ["scale", "deploy", "rollback", "configure", "add-component", "remove-component", "spec-patch"]}},
	  "risk": {"routine": ["images", "instances", "env", "resources", "secret-bindings", "connections", "ordering", "routes",
	    "domains", "volumes", "host-ports", "health", "components-added", "components-removed", "job-flags", "security"]},
	  "change_facts": {"no-change-facts": "routine", "migrations-changed": "routine"}}`)
	destructive := strings.Replace(shop, `"migrate": {"kind": "job", "image": "reg/migrate:1"}`,
		`"migrate": {"kind": "job", "image": "reg/migrate:1", "destructive": true}`, 1)
	for name, c := range map[string]struct {
		in   *oapv1.Intent
		base string
	}{
		"destructive job": {on(&oapv1.Deploy{Images: map[string]string{"migrate": "reg/migrate:2"}}), destructive},
		"volume deletion": {on(&oapv1.RemoveComponent{Component: "db", DeleteVolumes: true}), shop},
	} {
		t.Run(name, func(t *testing.T) {
			d := Evaluate(proposal(t, "ops", permissive, c.in, c.base))
			if d.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL || d.Risk != oapv1.Risk_RISK_IRREVERSIBLE || !strings.Contains(whys(d), WhyIrreversible) {
				t.Fatalf("got %v %v %v", d.Verdict, d.Risk, d.Reasons)
			}
		})
	}
}

func TestClaimedRiskIsIgnored(t *testing.T) {
	in := proposal(t, "claude", envelope(t, envelopeJSON),
		on(&oapv1.Configure{Component: "web", MemoryLimitMib: proto.Int32(128), CpuMillis: proto.Int32(100)}), shop)
	// An agent hands back a proposal claiming it is routine and allowed.
	in.Proposal.Risk = oapv1.Risk_RISK_ROUTINE
	in.Proposal.Verdict = oapv1.Verdict_VERDICT_ALLOWED
	in.Proposal.Reasons = []*oapv1.Reason{{Why: "agent.says_so", Message: "trust me"}}
	d := Evaluate(in)
	if d.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL || d.Risk != oapv1.Risk_RISK_SENSITIVE || strings.Contains(whys(d), "agent.") {
		t.Fatalf("claimed risk was used: %v %v %v", d.Verdict, d.Risk, d.Reasons)
	}
}

func TestSnapshotInvariant(t *testing.T) {
	in := proposal(t, "ops", envelope(t, envelopeJSON), on(&oapv1.Deploy{Images: map[string]string{"db": "postgres:18"}}), shop)
	if d := Evaluate(in); strings.Contains(whys(d), WhySnapshotFirst) {
		t.Fatalf("the planner snapshots first: %v", d.Reasons)
	}
	var steps []*oapv1.Step
	for _, s := range in.Proposal.Steps {
		if s.Kind != oapv1.StepKind_STEP_KIND_SNAPSHOT_VOLUME {
			steps = append(steps, s)
		}
	}
	in.Proposal.Steps = steps
	if d := Evaluate(in); d.Verdict != oapv1.Verdict_VERDICT_DENIED || !strings.Contains(whys(d), WhySnapshotFirst) {
		t.Fatalf("a plan without snapshots must be denied: %v", d.Reasons)
	}
}

func TestOverridingDefaultTiers(t *testing.T) {
	env := envelope(t, strings.Replace(envelopeJSON, `"risk": {"sensitive": ["secret-bindings"]}`,
		`"risk": {"routine": ["resources"], "sensitive": ["images"]}`, 1))
	resources := proposal(t, "claude", env, on(&oapv1.Configure{Component: "web", MemoryLimitMib: proto.Int32(128), CpuMillis: proto.Int32(100)}), shop)
	if d := Evaluate(resources); d.Verdict != oapv1.Verdict_VERDICT_ALLOWED {
		t.Fatalf("resources made routine: %v", d.Reasons)
	}
	images := proposal(t, "claude", env, on(&oapv1.Deploy{Images: map[string]string{"web": "reg/web:2"}}), shop)
	if d := Evaluate(images); d.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
		t.Fatalf("images made sensitive: %v", d.Reasons)
	}
}

func TestDecisionsAreDeterministic(t *testing.T) {
	env := envelope(t, envelopeJSON)
	in := proposal(t, "claude", env, on(&oapv1.SpecPatch{MergePatch: patch(t, map[string]any{
		"components": map[string]any{"web": map[string]any{"secrets": map[string]any{"TOKEN": "api-token"}, "instances": 3}},
		"ingress":    map[string]any{"https": true}})}), shop)
	in.Proposal.ChangeFacts = &oapv1.ChangeFacts{Ci: "green", Reviewed: true, Signed: true, MigrationsChanged: true}
	first := Evaluate(in)
	if len(first.Reasons) < 4 {
		t.Fatalf("the input should produce several reasons, so their order matters: %v", first.Reasons)
	}
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 200; i++ {
		shuffled := in
		shuffled.Changes = append([]spec.Change(nil), in.Changes...)
		r.Shuffle(len(shuffled.Changes), func(a, b int) { shuffled.Changes[a], shuffled.Changes[b] = shuffled.Changes[b], shuffled.Changes[a] })
		envCopy := proto.Clone(env).(*oapv1.Envelope)
		r.Shuffle(len(envCopy.Risk), func(a, b int) { envCopy.Risk[a], envCopy.Risk[b] = envCopy.Risk[b], envCopy.Risk[a] })
		r.Shuffle(len(envCopy.Invariants), func(a, b int) {
			envCopy.Invariants[a], envCopy.Invariants[b] = envCopy.Invariants[b], envCopy.Invariants[a]
		})
		shuffled.Envelope = envCopy
		got := Evaluate(shuffled)
		if got.Verdict != first.Verdict || got.Risk != first.Risk || len(got.Reasons) != len(first.Reasons) {
			t.Fatalf("decision changed with input order: %v vs %v", got, first)
		}
		for k := range got.Reasons {
			if !proto.Equal(got.Reasons[k], first.Reasons[k]) {
				t.Fatalf("reasons changed with input order: %v vs %v", got.Reasons, first.Reasons)
			}
		}
	}
}

func TestParseEnvelope(t *testing.T) {
	env := envelope(t, envelopeJSON)
	if len(env.Agents) != 2 || env.Agents[0].Agent != "claude" || len(env.Invariants) != 4 ||
		env.Breakers.FailedAppliesPerHour != 3 || !strings.HasPrefix(env.Version, "sha256:") {
		t.Fatalf("%v", env)
	}
	bad := map[string]string{
		`{"agents": {"x": {"scope": {"applications": ["a"]}, "may": ["reboot"]}}}`:         spec.WhyEnum,
		`{"agents": {"x": {"scope": {"applications": ["a"]}, "may": [], "risk": "low"}}}`:  spec.WhyUnknownField,
		`{"risk": {"routine": ["routes"], "sensitive": ["routes"]}}`:                       spec.WhyConflict,
		`{"risk": {"irreversible": ["volumes"]}}`:                                          spec.WhyUnknownField,
		`{"invariants": [{"within_quotas": true, "snapshot_before_volume_change": true}]}`: spec.WhyLength,
		`{"change_facts": {"ci-not-green": "maybe"}}`:                                      spec.WhyEnum,
		`{"agents": {"x": {"scope": {"components": ["web"]}, "may": []}}}`:                 spec.WhyRequired,
	}
	for doc, why := range bad {
		_, probs := ParseEnvelope(tree(t, doc))
		if len(probs) == 0 || probs[0].Why != why {
			t.Errorf("%s: want %s, got %v", doc, why, probs)
		}
	}
}

func TestAffordances(t *testing.T) {
	env := envelope(t, envelopeJSON)
	w := &oapv1.World{Applications: []*oapv1.Application{
		{Name: "shop", Components: []*oapv1.Component{{Name: "web"}, {Name: "db"}}},
		{Name: "blog", Components: []*oapv1.Component{{Name: "web"}}},
	}}
	Affordances(env, "claude", w)
	get := func(as []*oapv1.Affordance, action string) oapv1.Verdict {
		for _, a := range as {
			if a.Action == action {
				return a.Verdict
			}
		}
		return oapv1.Verdict_VERDICT_UNSPECIFIED
	}
	shopApp, blog := w.Applications[0], w.Applications[1]
	checks := []struct {
		got, want oapv1.Verdict
		what      string
	}{
		{get(shopApp.Components[0].Affordances, "scale"), oapv1.Verdict_VERDICT_ALLOWED, "scale shop.web"},
		{get(shopApp.Components[0].Affordances, "remove-component"), oapv1.Verdict_VERDICT_NEEDS_APPROVAL, "remove shop.web"},
		{get(shopApp.Components[1].Affordances, "scale"), oapv1.Verdict_VERDICT_DENIED, "scale shop.db (out of scope)"},
		{get(shopApp.Affordances, "deploy"), oapv1.Verdict_VERDICT_ALLOWED, "deploy shop"},
		{get(blog.Affordances, "deploy"), oapv1.Verdict_VERDICT_DENIED, "deploy blog"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.what, c.got, c.want)
		}
	}
	Affordances(env, "stranger", w)
	if get(shopApp.Affordances, "deploy") != oapv1.Verdict_VERDICT_DENIED {
		t.Error("unknown agents may do nothing")
	}
}
