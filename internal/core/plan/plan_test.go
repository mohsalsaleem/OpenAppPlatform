package plan

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

var update = flag.Bool("update", false, "rewrite golden files")

func parse(t testing.TB, tree any) *spec.Application {
	t.Helper()
	app, probs := spec.ParseApplication(tree)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return app
}

type history map[string]any

func (h history) lookup(hash string) (any, bool) { v, ok := h[hash]; return v, ok }

// compile runs an intent through the compiler and returns the planner input.
func compile(t testing.TB, tree any, h history, in *oapv1.Intent) (Input, *intent.Error) {
	t.Helper()
	res, err := intent.Compile(intent.Current{Tree: tree, History: h.lookup}, in)
	if err != nil {
		return Input{}, err
	}
	oldHash, _ := spec.Hash(tree)
	h[oldHash] = tree
	h[res.Hash] = res.Tree
	return Input{Application: res.App.Name, Old: parse(t, tree), OldTree: tree, OldHash: oldHash,
		New: res.App, NewTree: res.Tree, Rollback: res.Rollback, DeleteVolumes: res.DeleteVolumes, Intent: in}, nil
}

func mustCompile(t testing.TB, tree any, h history, in *oapv1.Intent) Input {
	t.Helper()
	input, err := compile(t, tree, h, in)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func jsonTree(t testing.TB, s string) any {
	t.Helper()
	v, probs := spec.FromJSON([]byte(s))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return v
}

func on(app string, change any) *oapv1.Intent {
	in := &oapv1.Intent{Application: app}
	switch c := change.(type) {
	case *oapv1.Scale:
		in.Change = &oapv1.Intent_Scale{Scale: c}
	case *oapv1.Deploy:
		in.Change = &oapv1.Intent_Deploy{Deploy: c}
	case *oapv1.Rollback:
		in.Change = &oapv1.Intent_Rollback{Rollback: c}
	case *oapv1.Configure:
		in.Change = &oapv1.Intent_Configure{Configure: c}
	case *oapv1.AddComponent:
		in.Change = &oapv1.Intent_AddComponent{AddComponent: c}
	case *oapv1.RemoveComponent:
		in.Change = &oapv1.Intent_RemoveComponent{RemoveComponent: c}
	case *oapv1.SpecPatch:
		in.Change = &oapv1.Intent_SpecPatch{SpecPatch: c}
	}
	return in
}

// render prints a plan one step per line, with releases shortened to names
// the reader can follow.
func render(p *Plan, releases map[string]string) string {
	var b strings.Builder
	for i, s := range p.Steps {
		kind := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(s.Kind.String(), "STEP_KIND_"), "_", "-"))
		fmt.Fprintf(&b, "%2d %-22s", i, kind)
		if s.Component != "" {
			fmt.Fprintf(&b, " %s", s.Component)
			if s.Ordinal > 0 {
				fmt.Fprintf(&b, "#%d", s.Ordinal)
			}
		}
		if s.Release != "" {
			name := releases[s.Release]
			if name == "" {
				name = s.Release
			}
			fmt.Fprintf(&b, " @%s", name)
		}
		if s.Volume != "" {
			fmt.Fprintf(&b, " volume=%s", s.Volume)
		}
		if len(s.After) > 0 {
			fmt.Fprintf(&b, " after %v", s.After)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "blast radius: %v, downtime %s\n", p.BlastRadius.Components, p.BlastRadius.Downtime)
	if len(p.Irreversible) > 0 {
		fmt.Fprintf(&b, "irreversible: %s\n", strings.Join(p.Irreversible, "; "))
	} else {
		undo, _ := json.Marshal(p.Undo.GetChange())
		fmt.Fprintf(&b, "undo: %s\n", strings.TrimSpace(string(undo)))
	}
	return b.String()
}

func goldenPlan(t *testing.T, name string, in Input, p *Plan) {
	t.Helper()
	if err := simulate(in, p); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	// Name releases "old", "new", or "same" when a component keeps its release.
	releases := map[string]string{}
	if in.Old != nil {
		for _, c := range in.Old.Components {
			releases[model.Release(c)] = "old"
		}
	}
	for _, c := range in.New.Components {
		if releases[model.Release(c)] == "old" {
			releases[model.Release(c)] = "same"
		} else {
			releases[model.Release(c)] = "new"
		}
	}
	got := render(p, releases)
	path := filepath.Join("testdata", name+".txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Fatalf("%s differs from its golden file:\n%s", name, got)
	}
}

const web = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "site", "environment": "production",
  "ingress": {"domain": "site.example.com", "routes": {"/": {"component": "web"}}},
  "components": {"web": {"kind": "web", "image": "reg/web:1", "port": 8080}}}`

const shop = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "shop", "environment": "production",
  "ingress": {"domain": "shop.example.com", "routes": {"/": {"component": "api"}}},
  "components": {
    "migrate": {"kind": "job", "image": "reg/migrations:7"},
    "api": {"kind": "web", "image": "reg/api:7", "port": 8080, "instances": 2, "after": ["migrate"],
            "uses": {"db": {"type": "database"}}},
    "worker": {"kind": "worker", "image": "reg/api:7", "uses": {"db": {"type": "database"}}},
    "db": {"kind": "data", "image": "postgres:17", "port": 5432, "volumes": {"pgdata": "/var/lib/postgresql/data"}}}}`

func TestGoldenScaleUpAndDown(t *testing.T) {
	h := history{}
	up := mustCompile(t, jsonTree(t, web), h, on("site", &oapv1.Scale{Component: "web", Instances: 3}))
	goldenPlan(t, "scale-1-to-3", up, Build(up))
	down := mustCompile(t, up.NewTree, h, on("site", &oapv1.Scale{Component: "web", Instances: 1}))
	goldenPlan(t, "scale-3-to-1", down, Build(down))
}

func TestGoldenDeployWithPreDeployJob(t *testing.T) {
	in := mustCompile(t, jsonTree(t, shop), history{}, on("shop", &oapv1.Deploy{Images: map[string]string{
		"api": "reg/api:8", "worker": "reg/api:8", "migrate": "reg/migrations:8"}}))
	p := Build(in)
	goldenPlan(t, "deploy-with-pre-deploy-job", in, p)
	job := -1
	for i, s := range p.Steps {
		if s.Kind == oapv1.StepKind_STEP_KIND_RUN_JOB {
			job = i
		}
		if s.Kind == oapv1.StepKind_STEP_KIND_START_INSTANCE && s.Component == "api" && !ancestors(p, i)[job] {
			t.Errorf("api#%d starts without waiting for the migration", s.Ordinal)
		}
	}
}

func TestGoldenRollbackSkipsJobs(t *testing.T) {
	h := history{}
	deploy := mustCompile(t, jsonTree(t, shop), h, on("shop", &oapv1.Deploy{Images: map[string]string{
		"api": "reg/api:8", "migrate": "reg/migrations:8"}}))
	back := mustCompile(t, deploy.NewTree, h, on("shop", &oapv1.Rollback{ToRelease: deploy.OldHash}))
	p := Build(back)
	goldenPlan(t, "rollback", back, p)
	for _, s := range p.Steps {
		if s.Kind == oapv1.StepKind_STEP_KIND_RUN_JOB {
			t.Fatal("rollback must not re-run jobs")
		}
	}
}

func TestGoldenComponentRemoval(t *testing.T) {
	h := history{}
	worker := mustCompile(t, jsonTree(t, shop), h, on("shop", &oapv1.RemoveComponent{Component: "worker"}))
	goldenPlan(t, "remove-worker", worker, Build(worker))

	// Removing db needs api to stop using it first; then delete its volume.
	cut := mustCompile(t, worker.NewTree, h, on("shop", &oapv1.SpecPatch{MergePatch: mustStruct(t,
		map[string]any{"components": map[string]any{"api": map[string]any{"uses": nil}}})}))
	db := mustCompile(t, cut.NewTree, h, on("shop", &oapv1.RemoveComponent{Component: "db", DeleteVolumes: true}))
	p := Build(db)
	goldenPlan(t, "remove-db-with-volume", db, p)
	if p.Undo != nil || len(p.Irreversible) == 0 {
		t.Fatal("deleting a volume must be irreversible")
	}
}

func TestDestructiveJobIsIrreversible(t *testing.T) {
	tree := jsonTree(t, strings.Replace(shop, `"migrate": {"kind": "job", "image": "reg/migrations:7"}`,
		`"migrate": {"kind": "job", "image": "reg/migrations:7", "destructive": true}`, 1))
	in := mustCompile(t, tree, history{}, on("shop", &oapv1.Deploy{Images: map[string]string{"migrate": "reg/migrations:8"}}))
	p := Build(in)
	if p.Undo != nil || len(p.Irreversible) != 1 || !strings.Contains(p.Irreversible[0], "destructive") {
		t.Fatalf("plan: %+v", p)
	}
	// A deploy that doesn't run the destructive job stays reversible.
	in = mustCompile(t, tree, history{}, on("shop", &oapv1.Deploy{Images: map[string]string{"worker": "reg/api:8"}}))
	if p := Build(in); p.Undo == nil {
		t.Fatal("not running the job keeps the plan reversible")
	}
}

func mustStruct(t testing.TB, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ancestors returns every step that must finish before step i.
func ancestors(p *Plan, i int) map[int]bool {
	out := map[int]bool{}
	var walk func(int)
	walk = func(j int) {
		for _, a := range p.Steps[j].After {
			if !out[int(a)] {
				out[int(a)] = true
				walk(int(a))
			}
		}
	}
	walk(i)
	return out
}

type key struct {
	component string
	ordinal   int32
	release   string
}

// simulate runs a plan's steps in order against a model of the runtime and
// edge, starting from the old desired state, and checks the planner's rules:
//
//	(a) no step stops an instance that is still a route member;
//	(b) every routed instance leaves through route-remove, a confirmed reload,
//	    then drain, before it stops;
//	and that the end state matches the new desired state.
func simulate(in Input, p *Plan) error {
	running, ready, routedNow := map[key]bool{}, map[key]bool{}, map[key]bool{}
	removedAt := map[key]int{}  // route-remove step, awaiting a confirmed reload
	confirmed := map[key]bool{} // route-remove confirmed by a reload
	drained := map[key]bool{}
	if in.Old != nil {
		for name, c := range in.Old.Components {
			if c.Kind == spec.KindJob {
				continue
			}
			for i := 1; i <= c.Instances; i++ {
				k := key{name, int32(i), model.Release(c)}
				running[k], ready[k] = true, true
				if routed(in.Old, name) {
					routedNow[k] = true
				}
			}
		}
	}
	for i, s := range p.Steps {
		for _, a := range s.After {
			if int(a) >= i {
				return fmt.Errorf("step %d depends on later step %d", i, a)
			}
		}
		k := key{s.Component, s.Ordinal, s.Release}
		switch s.Kind {
		case oapv1.StepKind_STEP_KIND_START_INSTANCE:
			if running[k] {
				return fmt.Errorf("step %d starts %v twice", i, k)
			}
			running[k] = true
		case oapv1.StepKind_STEP_KIND_AWAIT_READY:
			if !running[k] {
				return fmt.Errorf("step %d awaits %v, which isn't running", i, k)
			}
			ready[k] = true
		case oapv1.StepKind_STEP_KIND_ROUTE_ADD:
			if !ready[k] {
				return fmt.Errorf("step %d routes %v before it is ready", i, k)
			}
			routedNow[k] = true
		case oapv1.StepKind_STEP_KIND_ROUTE_REMOVE:
			if !routedNow[k] {
				return fmt.Errorf("step %d removes %v, which isn't routed", i, k)
			}
			delete(routedNow, k)
			removedAt[k] = i
		case oapv1.StepKind_STEP_KIND_EDGE_RELOAD_CONFIRMED:
			for r := range removedAt {
				confirmed[r] = true
				delete(removedAt, r)
			}
		case oapv1.StepKind_STEP_KIND_DRAIN:
			if !confirmed[k] {
				return fmt.Errorf("step %d drains %v before its removal from the route is confirmed", i, k)
			}
			drained[k] = true
		case oapv1.StepKind_STEP_KIND_STOP:
			if !running[k] {
				return fmt.Errorf("step %d stops %v, which isn't running", i, k)
			}
			if routedNow[k] {
				return fmt.Errorf("rule (a): step %d stops %v while it is still a route member", i, k)
			}
			if confirmed[k] && !drained[k] {
				return fmt.Errorf("rule (b): step %d stops %v without draining it", i, k)
			}
			if _, pending := removedAt[k]; pending {
				return fmt.Errorf("rule (b): step %d stops %v before its route removal is confirmed", i, k)
			}
			delete(running, k)
			delete(ready, k)
		}
	}
	want := map[key]bool{}
	for name, c := range in.New.Components {
		if c.Kind == spec.KindJob {
			continue
		}
		for i := 1; i <= c.Instances; i++ {
			k := key{name, int32(i), model.Release(c)}
			want[k] = true
			if routed(in.New, name) != routedNow[k] {
				return fmt.Errorf("end state: %v routed=%v, want %v", k, routedNow[k], routed(in.New, name))
			}
		}
	}
	for k := range running {
		if !want[k] {
			return fmt.Errorf("end state: %v still running", k)
		}
	}
	for k := range want {
		if !running[k] {
			return fmt.Errorf("end state: %v not running", k)
		}
	}
	for k := range routedNow {
		if !want[k] {
			return fmt.Errorf("end state: %v still routed", k)
		}
	}
	return nil
}
