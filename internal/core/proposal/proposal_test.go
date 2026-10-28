package proposal

import (
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/policy"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const shop = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "shop", "environment": "production",
  "ingress": {"domain": "shop.example.com", "routes": {"/": {"component": "web"}}},
  "components": {
    "web": {"kind": "web", "image": "reg/web:1", "port": 8080, "instances": 2, "after": ["migrate"]},
    "migrate": {"kind": "job", "image": "reg/migrate:1"},
    "db": {"kind": "data", "image": "postgres:17", "port": 5432, "volumes": {"pgdata": "/data"}}}}`

const envelopeJSON = `{
  "agents": {"claude": {"scope": {"applications": ["shop"]}, "may": ["scale", "deploy", "rollback", "configure"]}},
  "invariants": [{"max_releases": {"per_application_per_hour": 3}}],
  "change_facts": {"migrations-changed": "sensitive", "no-change-facts": "routine"}}`

func tree(t testing.TB, s string) any {
	t.Helper()
	v, probs := spec.FromJSON([]byte(s))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return v
}

type world struct {
	desired   map[string]any
	envelopes map[string]*oapv1.Envelope
}

func newWorld(t testing.TB) (*world, Inputs) {
	t.Helper()
	w := &world{desired: map[string]any{}, envelopes: map[string]*oapv1.Envelope{}}
	base := tree(t, shop)
	h, _ := spec.Hash(base)
	w.desired[h] = base
	env, probs := policy.ParseEnvelope(tree(t, envelopeJSON))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	w.envelopes[env.Version] = env
	return w, Inputs{
		Agent:        "claude",
		BaseRevision: 41,
		Tree:         base,
		History:      w.lookup,
		Intent: &oapv1.Intent{Application: "shop", Because: "new release",
			Change: &oapv1.Intent_Deploy{Deploy: &oapv1.Deploy{Images: map[string]string{"web": "reg/web:2", "migrate": "reg/migrate:2"}}}},
		Facts:    &oapv1.ChangeFacts{Repository: "acme/shop", Commit: "9c2e1f0", Ci: "green", Reviewed: true, Signed: true},
		Envelope: env,
		Context:  &oapv1.PolicyContext{ReleasesLastHour: 1},
	}
}

func (w *world) lookup(h string) (any, bool) { v, ok := w.desired[h]; return v, ok }

func (w *world) stores() Stores {
	return Stores{Desired: w.lookup, Envelope: func(v string) (*oapv1.Envelope, bool) { e, ok := w.envelopes[v]; return e, ok }}
}

func propose(t testing.TB, in Inputs) *oapv1.Proposal {
	t.Helper()
	p, err := Propose(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSameInputsSameID(t *testing.T) {
	_, in := newWorld(t)
	a, b := propose(t, in), propose(t, in)
	if a.Id != b.Id || !strings.HasPrefix(a.Id, "prop_") || len(a.Id) != 37 {
		t.Fatalf("ids %s %s", a.Id, b.Id)
	}
	if a.Verdict != oapv1.Verdict_VERDICT_ALLOWED || a.Undo == nil || len(a.Steps) == 0 {
		t.Fatalf("unexpected proposal: %v", a)
	}
}

// Every input the plan or verdict depends on must change the id.
func TestEveryInputChangesTheID(t *testing.T) {
	w, base := newWorld(t)
	baseID := propose(t, base).Id
	otherEnv, _ := policy.ParseEnvelope(tree(t, strings.Replace(envelopeJSON, `"per_application_per_hour": 3`, `"per_application_per_hour": 4`, 1)))
	w.envelopes[otherEnv.Version] = otherEnv
	edits := map[string]func(*Inputs){
		"base revision": func(in *Inputs) { in.BaseRevision = 42 },
		"base desired state": func(in *Inputs) {
			in.Tree = tree(t, strings.Replace(shop, `"instances": 2`, `"instances": 3`, 1))
		},
		"agent": func(in *Inputs) {
			in.Agent = "other"
		},
		"intent image": func(in *Inputs) {
			in.Intent = proto.Clone(in.Intent).(*oapv1.Intent)
			in.Intent.GetDeploy().Images["web"] = "reg/web:3"
		},
		"intent reason": func(in *Inputs) {
			in.Intent = proto.Clone(in.Intent).(*oapv1.Intent)
			in.Intent.Because = "another reason"
		},
		"change facts": func(in *Inputs) {
			in.Facts = proto.Clone(in.Facts).(*oapv1.ChangeFacts)
			in.Facts.MigrationsChanged = true
		},
		"no change facts":  func(in *Inputs) { in.Facts = nil },
		"envelope version": func(in *Inputs) { in.Envelope = otherEnv },
		"policy context": func(in *Inputs) {
			in.Context = &oapv1.PolicyContext{ReleasesLastHour: 2}
		},
	}
	seen := map[string]string{baseID: "base"}
	for name, edit := range edits {
		in := base
		edit(&in)
		id := propose(t, in).Id
		if prev, dup := seen[id]; dup {
			t.Errorf("changing %s gave the same id as %s", name, prev)
		}
		seen[id] = name
	}
}

func TestOutputsDontChangeTheID(t *testing.T) {
	_, in := newWorld(t)
	p := propose(t, in)
	q := proto.Clone(p).(*oapv1.Proposal)
	q.Verdict, q.Risk, q.Reasons, q.Next, q.Undo, q.BlastRadius = 0, 0, nil, nil, nil, nil
	if id, _ := ID(q); id != p.Id {
		t.Fatal("outputs are derived from inputs and must not affect the id")
	}
}

// A stored proposal, round-tripped through JSON as storage would, replays to
// the identical proposal: same id, steps, verdict, risk and reasons.
func TestReplayReproducesTheVerdict(t *testing.T) {
	w, in := newWorld(t)
	cases := map[string]func(*Inputs){
		"allowed": nil,
		"needs approval": func(in *Inputs) {
			in.Facts = &oapv1.ChangeFacts{Ci: "green", Reviewed: true, Signed: true, MigrationsChanged: true}
		},
		"denied": func(in *Inputs) { in.Context = &oapv1.PolicyContext{ReleasesLastHour: 3} },
		"rollback": func(in *Inputs) {
			next := tree(t, strings.Replace(strings.Replace(shop, "reg/web:1", "reg/web:2", 1), "reg/migrate:1", "reg/migrate:2", 1))
			h, _ := spec.Hash(next)
			w.desired[h] = next
			oldHash, _ := spec.Hash(in.Tree)
			in.Tree = next
			in.Intent = &oapv1.Intent{Application: "shop", Change: &oapv1.Intent_Rollback{Rollback: &oapv1.Rollback{ToRelease: oldHash}}}
		},
		"spec patch": func(in *Inputs) {
			patch, _ := structpb.NewStruct(map[string]any{"components": map[string]any{"web": map[string]any{"instances": 3, "env": map[string]any{"PAGE_SIZE": "50"}}}})
			in.Intent = &oapv1.Intent{Application: "shop", Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: patch}}}
		},
	}
	verdicts := map[oapv1.Verdict]bool{}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			c := in
			if edit != nil {
				edit(&c)
			}
			p, err := Propose(c)
			if err != nil {
				t.Fatal(err)
			}
			verdicts[p.Verdict] = true
			raw, merr := protojson.Marshal(p)
			if merr != nil {
				t.Fatal(merr)
			}
			stored := &oapv1.Proposal{}
			if err := protojson.Unmarshal(raw, stored); err != nil {
				t.Fatal(err)
			}
			if err := Replay(stored, w.stores()); err != nil {
				t.Fatal(err)
			}
		})
	}
	if len(verdicts) != 3 {
		t.Fatalf("replay must be shown for all three verdicts, got %v", verdicts)
	}
}

func TestReplayDetectsTamperingAndDrift(t *testing.T) {
	w, in := newWorld(t)
	p := propose(t, in)
	check := func(name, why string, edit func(*oapv1.Proposal), stores Stores) {
		t.Helper()
		q := proto.Clone(p).(*oapv1.Proposal)
		edit(q)
		err := Replay(q, stores)
		if err == nil || !strings.HasPrefix(err.Error(), why) {
			t.Errorf("%s: want %s, got %v", name, why, err)
		}
	}
	check("steps edited", WhyTampered, func(q *oapv1.Proposal) { q.Steps = q.Steps[1:] }, w.stores())
	check("context edited", WhyTampered, func(q *oapv1.Proposal) { q.Context.ReleasesLastHour = 9 }, w.stores())
	check("verdict edited", WhyNotReproduced, func(q *oapv1.Proposal) { q.Verdict = oapv1.Verdict_VERDICT_DENIED }, w.stores())
	check("reasons edited", WhyNotReproduced, func(q *oapv1.Proposal) { q.Reasons = nil }, w.stores())
	check("base gone", WhyUnknownBase, func(*oapv1.Proposal) {},
		Stores{Desired: func(string) (any, bool) { return nil, false }, Envelope: w.stores().Envelope})
	check("envelope gone", WhyUnknownEnvelope, func(*oapv1.Proposal) {},
		Stores{Desired: w.lookup, Envelope: func(string) (*oapv1.Envelope, bool) { return nil, false }})
	if err := Replay(p, w.stores()); err != nil {
		t.Fatalf("untouched proposal must replay: %v", err)
	}
}
