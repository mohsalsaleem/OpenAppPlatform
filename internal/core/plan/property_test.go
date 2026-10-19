package plan

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

// randomApp generates a valid application: a mix of web, worker, job and data
// components with connections and ordering only to earlier components (so no
// cycles), some routes, some volumes and some host ports.
func randomApp(r *rand.Rand) map[string]any {
	components := map[string]any{}
	var names []string
	kinds := map[string]string{}
	routes := map[string]any{}
	for i := 0; i < 1+r.IntN(5); i++ {
		name := fmt.Sprintf("c%d", i)
		kind := []string{"web", "web", "worker", "job", "data"}[r.IntN(5)]
		c := map[string]any{"kind": kind, "image": fmt.Sprintf("reg/%s:%d", name, r.IntN(3))}
		switch kind {
		case "web":
			c["port"] = int64(8080)
			if r.IntN(5) == 0 {
				c["host_port"] = int64(9000 + i)
			} else {
				c["instances"] = int64(1 + r.IntN(4))
				if r.IntN(3) == 0 {
					c["volumes"] = map[string]any{"cache-" + name: "/cache"}
				}
				if r.IntN(2) == 0 {
					routes[fmt.Sprintf("/r%d", i)] = map[string]any{"component": name}
				}
			}
		case "worker":
			c["instances"] = int64(1 + r.IntN(3))
		case "job":
			if r.IntN(3) == 0 {
				c["destructive"] = true
			}
		case "data":
			c["port"] = int64(5432)
			c["volumes"] = map[string]any{"data-" + name: "/data"}
		}
		if len(names) > 0 && kind != "data" {
			dep := names[r.IntN(len(names))]
			switch kinds[dep] {
			case "data":
				c["uses"] = map[string]any{dep: map[string]any{"type": "database"}}
			case "web":
				c["uses"] = map[string]any{dep: map[string]any{"type": "http"}}
			default:
				c["after"] = []any{dep}
			}
		}
		components[name] = c
		names = append(names, name)
		kinds[name] = kind
	}
	app := map[string]any{"api_version": "oap.dev/v1", "kind": "Application", "name": "app",
		"environment": "production", "components": components}
	if len(routes) > 0 {
		app["ingress"] = map[string]any{"domain": "app.example.com", "routes": routes}
	}
	return app
}

// randomIntent picks a change that may or may not compile; callers skip the
// ones the compiler rejects.
func randomIntent(r *rand.Rand, app map[string]any, h history) *oapv1.Intent {
	components := app["components"].(map[string]any)
	var names []string
	for _, n := range sortedKeys(components) {
		names = append(names, n)
	}
	pick := names[r.IntN(len(names))]
	switch r.IntN(7) {
	case 0:
		return on("app", &oapv1.Scale{Component: pick, Instances: int32(1 + r.IntN(4))})
	case 1:
		images := map[string]string{}
		for _, n := range names {
			if r.IntN(2) == 0 {
				images[n] = fmt.Sprintf("reg/%s:%d", n, 10+r.IntN(5))
			}
		}
		return on("app", &oapv1.Deploy{Images: images})
	case 2:
		for hash := range h {
			return on("app", &oapv1.Rollback{ToRelease: hash})
		}
		return on("app", &oapv1.Rollback{ToRelease: "none"})
	case 3:
		return on("app", &oapv1.Configure{Component: pick, SetEnv: map[string]string{"V": fmt.Sprint(r.IntN(3))}})
	case 4:
		s, _ := structpb.NewStruct(map[string]any{"kind": "worker", "image": "reg/new:1"})
		return on("app", &oapv1.AddComponent{Name: fmt.Sprintf("n%d", r.IntN(3)), Spec: s})
	case 5:
		return on("app", &oapv1.RemoveComponent{Component: pick, DeleteVolumes: r.IntN(2) == 0})
	default:
		// Toggle a route for a web component without a host port.
		c := components[pick].(map[string]any)
		if c["kind"] != "web" || c["host_port"] != nil {
			return on("app", &oapv1.Scale{Component: pick, Instances: 2})
		}
		path := "/" + pick
		patch := map[string]any{"ingress": map[string]any{"domain": "app.example.com",
			"routes": map[string]any{path: map[string]any{"component": pick}}}}
		if ing, ok := app["ingress"].(map[string]any); ok {
			if routes := ing["routes"].(map[string]any); routes[path] != nil && len(routes) > 1 {
				patch = map[string]any{"ingress": map[string]any{"routes": map[string]any{path: nil}}}
			}
		}
		s, _ := structpb.NewStruct(patch)
		return on("app", &oapv1.SpecPatch{MergePatch: s})
	}
}

func effective(t testing.TB, tree any) string {
	b, _ := json.Marshal(parse(t, tree))
	return string(b)
}

func TestPlanProperties(t *testing.T) {
	const runs = 3000
	planned, irreversible := 0, 0
	for seed := uint64(1); seed <= runs; seed++ {
		r := rand.New(rand.NewPCG(seed, 7))
		app := randomApp(r)
		if _, probs := spec.ParseApplication(app); len(probs) > 0 {
			t.Fatalf("seed %d: generator made an invalid app: %v", seed, probs)
		}
		h := history{}
		tree := any(app)
		// Apply a few changes in a row, planning each one.
		for round := 0; round < 3; round++ {
			in, err := compile(t, tree, h, randomIntent(r, tree.(map[string]any), h))
			if err != nil {
				continue
			}
			p := Build(in)
			planned++
			if err := simulate(in, p); err != nil {
				t.Fatalf("seed %d round %d: %v\n%s", seed, round, err, render(p, nil))
			}
			checkReversibility(t, seed, in, p, h)
			if len(p.Irreversible) > 0 {
				irreversible++
			}
			tree = in.NewTree
		}
	}
	if planned < runs || irreversible == 0 {
		t.Fatalf("generator too narrow: %d plans, %d irreversible", planned, irreversible)
	}
	t.Logf("%d plans checked, %d irreversible", planned, irreversible)
}

// checkReversibility checks rules (c) and (d): reversible plans have an undo
// that restores the old desired state; volume deletion and destructive jobs
// make a plan irreversible.
func checkReversibility(t *testing.T, seed uint64, in Input, p *Plan, h history) {
	t.Helper()
	destroys := len(in.DeleteVolumes) > 0
	for _, s := range p.Steps {
		if s.Kind == oapv1.StepKind_STEP_KIND_RUN_JOB && in.New.Components[s.Component].Destructive {
			destroys = true
		}
	}
	if destroys != (len(p.Irreversible) > 0) || destroys != (p.Undo == nil) {
		t.Fatalf("seed %d: rule (d): destroys=%v irreversible=%v undo=%v", seed, destroys, p.Irreversible, p.Undo)
	}
	if p.Undo == nil {
		return
	}
	back, err := intent.Compile(intent.Current{Tree: in.NewTree, History: h.lookup}, p.Undo)
	if err != nil {
		t.Fatalf("seed %d: rule (c): undo doesn't compile: %v\nundo: %v", seed, err, p.Undo)
	}
	if effective(t, back.Tree) != effective(t, in.OldTree) {
		t.Fatalf("seed %d: rule (c): undo doesn't restore the old desired state\nundo: %v", seed, p.Undo)
	}
}
