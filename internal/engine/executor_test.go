package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/plan"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

var ctx = context.Background()

const web = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "site", "environment": "production",
  "ingress": {"domain": "site.example.com", "routes": {"/": {"component": "web"}, "/api": {"component": "web", "strip_prefix": true}}},
  "components": {"web": {"kind": "web", "image": "reg/web:1", "port": 8080}}}`

const shop = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "shop", "environment": "production",
  "ingress": {"domain": "shop.example.com", "routes": {"/": {"component": "api"}}},
  "components": {
    "migrate": {"kind": "job", "image": "reg/migrations:7"},
    "api": {"kind": "web", "image": "reg/api:7", "port": 8080, "instances": 2, "after": ["migrate"],
            "uses": {"db": {"type": "database"}}},
    "worker": {"kind": "worker", "image": "reg/api:7", "uses": {"db": {"type": "database"}}},
    "db": {"kind": "data", "image": "postgres:17", "port": 5432, "volumes": {"pgdata": "/var/lib/postgresql/data"}}}}`

func tree(t testing.TB, s string) any {
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
	case *oapv1.RemoveComponent:
		in.Change = &oapv1.Intent_RemoveComponent{RemoveComponent: c}
	case *oapv1.SpecPatch:
		in.Change = &oapv1.Intent_SpecPatch{SpecPatch: c}
	}
	return in
}

// scenario is an operation together with the desired state it starts from.
type scenario struct {
	name    string
	oldTree any
	op      Operation
}

func compile(t testing.TB, name string, cur any, history map[string]any, in *oapv1.Intent) scenario {
	t.Helper()
	res, err := intent.Compile(intent.Current{Tree: cur, History: func(h string) (any, bool) { v, ok := history[h]; return v, ok }}, in)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := spec.ParseApplication(cur)
	oldHash, _ := spec.Hash(cur)
	history[oldHash], history[res.Hash] = cur, res.Tree
	p := plan.Build(plan.Input{Application: res.App.Name, Old: old, OldTree: cur, OldHash: oldHash, New: res.App,
		NewTree: res.Tree, Rollback: res.Rollback, DeleteVolumes: res.DeleteVolumes, Intent: in})
	return scenario{name: name, oldTree: cur, op: Operation{ID: "op-" + strings.ReplaceAll(name, " ", "-"),
		Application: res.App.Name, Steps: p.Steps, Old: old, New: res.App, NewTree: res.Tree, Author: "agent:test"}}
}

// scenarios mirrors the planner's golden plans.
func scenarios(t testing.TB) []scenario {
	h := map[string]any{}
	up := compile(t, "scale 1 to 3", tree(t, web), h, on("site", &oapv1.Scale{Component: "web", Instances: 3}))
	down := compile(t, "scale 3 to 1", up.op.NewTree, h, on("site", &oapv1.Scale{Component: "web", Instances: 1}))
	deploy := compile(t, "deploy with job", tree(t, shop), h, on("shop", &oapv1.Deploy{Images: map[string]string{
		"api": "reg/api:8", "worker": "reg/api:8", "migrate": "reg/migrations:8"}}))
	oldHash, _ := spec.Hash(deploy.oldTree)
	back := compile(t, "rollback", deploy.op.NewTree, h, on("shop", &oapv1.Rollback{ToRelease: oldHash}))
	worker := compile(t, "remove worker", tree(t, shop), h, on("shop", &oapv1.RemoveComponent{Component: "worker"}))
	patch, _ := structpb.NewStruct(map[string]any{"components": map[string]any{"api": map[string]any{"uses": nil}}})
	cut := compile(t, "cut db", worker.op.NewTree, h, on("shop", &oapv1.SpecPatch{MergePatch: patch}))
	db := compile(t, "remove db with volume", cut.op.NewTree, h, on("shop", &oapv1.RemoveComponent{Component: "db", DeleteVolumes: true}))
	return []scenario{up, down, deploy, back, worker, db}
}

type world struct {
	clock   *sim.Clock
	runtime *sim.Runtime
	edge    *sim.Edge
	journal *sim.Journal
	desired *sim.Desired
}

// newWorld builds a simulated host already running the scenario's old
// desired state, with effect counters reset.
func newWorld(t testing.TB, sc scenario) *world {
	t.Helper()
	clock := sim.NewClock(time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC))
	w := &world{clock: clock, runtime: sim.NewRuntime(clock, model.HostObservation{Name: "sim"}), edge: sim.NewEdge(),
		journal: &sim.Journal{}, desired: sim.NewDesired()}
	old := sc.op.Old
	if _, err := w.desired.Commit(ctx, old.Name, sc.oldTree, "owner", "initial"); err != nil {
		t.Fatal(err)
	}
	cfg := ops.EdgeConfig{}
	for _, name := range old.Names() {
		c := old.Components[name]
		if c.Kind == spec.KindJob {
			w.runtime.RunJob(ctx, ops.JobSpecFor(old, name))
			continue
		}
		for i := 1; i <= c.Instances; i++ {
			s := ops.InstanceSpecFor(old, name, i)
			w.runtime.Start(ctx, s)
			cfg = ops.WithMembers(cfg, ops.Members(old, s.Ref)...)
		}
	}
	w.edge.Apply(ctx, cfg)
	w.runtime.Starts, w.runtime.JobRuns, w.runtime.Stops = map[ops.InstanceRef]int{}, map[ops.InstanceRef]int{}, map[ops.InstanceRef]int{}
	w.desired.Commits = 0
	return w
}

func (w *world) executor() *Executor {
	return &Executor{Runtime: w.runtime, Edge: w.edge, Journal: w.journal, Desired: w.desired, Clock: w.clock}
}

// check verifies the end state matches the new desired state and that no
// effect happened more than once.
func (w *world) check(t testing.TB, sc scenario) {
	t.Helper()
	newApp := sc.op.New
	want := map[ops.InstanceRef]bool{}
	cfg := ops.EdgeConfig{}
	for _, name := range newApp.Names() {
		c := newApp.Components[name]
		if c.Kind == spec.KindJob {
			continue
		}
		for i := 1; i <= c.Instances; i++ {
			r := ops.Ref(newApp.Name, name, c, i)
			want[r] = true
			cfg = ops.WithMembers(cfg, ops.Members(newApp, r)...)
		}
	}
	running := w.runtime.Running()
	if len(running) != len(want) {
		t.Fatalf("running %v, want %d instances", running, len(want))
	}
	for _, r := range running {
		if !want[r] {
			t.Fatalf("%v still running", r)
		}
	}
	got, _, _ := w.edge.Config(ctx)
	if fmt.Sprint(got) != fmt.Sprint(cfg) {
		t.Fatalf("edge config\n got %v\nwant %v", got, cfg)
	}
	for r, n := range w.runtime.Starts {
		if n > 1 {
			t.Fatalf("%v started %d times", r, n)
		}
	}
	for r, n := range w.runtime.Stops {
		if n > 1 {
			t.Fatalf("%v stopped %d times", r, n)
		}
	}
	for r, n := range w.runtime.JobRuns {
		if n > 1 {
			t.Fatalf("job %v ran %d times", r, n)
		}
	}
	snapshots := 0
	for _, s := range sc.op.Steps {
		if s.Kind == oapv1.StepKind_STEP_KIND_SNAPSHOT_VOLUME {
			snapshots++
		}
	}
	if w.runtime.Snapshots != snapshots {
		t.Fatalf("%d snapshots for %d snapshot steps", w.runtime.Snapshots, snapshots)
	}
	if w.desired.Commits != 1 {
		t.Fatalf("desired state committed %d times", w.desired.Commits)
	}
	if _, h, _ := w.desired.Get(ctx, newApp.Name); h != hash(t, sc.op.NewTree) {
		t.Fatal("desired state is not the new tree")
	}
}

func hash(t testing.TB, tree any) string {
	h, err := spec.Hash(tree)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestRunWithoutCrash(t *testing.T) {
	for _, sc := range scenarios(t) {
		t.Run(sc.name, func(t *testing.T) {
			w := newWorld(t, sc)
			if err := w.executor().Run(ctx, sc.op); err != nil {
				t.Fatal(err)
			}
			w.check(t, sc)
			// Running a finished operation again does nothing.
			if err := w.executor().Run(ctx, sc.op); err != nil {
				t.Fatal(err)
			}
			w.check(t, sc)
		})
	}
}

// TestCrashAtEveryPoint crashes each scenario at every step and crash point,
// then resumes with a fresh executor that has only the journal and the
// simulated host, and checks the end state and that nothing happened twice.
func TestCrashAtEveryPoint(t *testing.T) {
	cases := 0
	for _, sc := range scenarios(t) {
		for i := range sc.op.Steps {
			for _, point := range []string{BeforeIntent, AfterIntent, BeforeOutcome} {
				cases++
				w := newWorld(t, sc)
				x := w.executor()
				x.crash = func(step int, p string) bool { return step == i && p == point }
				if err := x.Run(ctx, sc.op); !errors.Is(err, ErrCrashed) {
					t.Fatalf("%s step %d %s: want a crash, got %v", sc.name, i, point, err)
				}
				if err := w.executor().Run(ctx, sc.op); err != nil {
					t.Fatalf("%s step %d %s: resume: %v", sc.name, i, point, err)
				}
				t.Run(fmt.Sprintf("%s/step%02d/%s", sc.name, i, point), func(t *testing.T) { w.check(t, sc) })
			}
		}
	}
	t.Logf("%d crash points", cases)
}

// TestRepeatedCrashes crashes the same operation several times in a row at
// increasing steps before letting it finish.
func TestRepeatedCrashes(t *testing.T) {
	for _, sc := range scenarios(t) {
		w := newWorld(t, sc)
		for i := 0; i < len(sc.op.Steps); i += 3 {
			x := w.executor()
			point := []string{BeforeIntent, AfterIntent, BeforeOutcome}[i%3]
			x.crash = func(step int, p string) bool { return step == i && p == point }
			x.Run(ctx, sc.op)
		}
		if err := w.executor().Run(ctx, sc.op); err != nil {
			t.Fatalf("%s: %v", sc.name, err)
		}
		w.check(t, sc)
	}
}

func TestFailedStepsStopTheOperation(t *testing.T) {
	sc := scenarios(t)[2] // deploy with job
	w := newWorld(t, sc)
	w.runtime.JobFails["migrate"] = true
	err := w.executor().Run(ctx, sc.op)
	var se *StepError
	if !errors.As(err, &se) || se.Kind != oapv1.StepKind_STEP_KIND_RUN_JOB {
		t.Fatalf("want the job step to fail, got %v", err)
	}
	for _, r := range w.runtime.Running() {
		if r.Component == "api" && r.Release == model.Release(sc.op.New.Components["api"]) {
			t.Fatal("api must not start after its migration failed")
		}
	}
	// The failure is recorded; a second run reports it without retrying.
	runs := w.runtime.JobRuns
	err = w.executor().Run(ctx, sc.op)
	if !errors.As(err, &se) || len(w.runtime.JobRuns) != len(runs) {
		t.Fatalf("failed step must stay failed: %v", err)
	}
}

func TestUnhealthyInstanceTimesOut(t *testing.T) {
	sc := scenarios(t)[0] // scale up
	w := newWorld(t, sc)
	w.runtime.Unhealthy["web"] = true
	err := w.executor().Run(ctx, sc.op)
	var se *StepError
	if !errors.As(err, &se) || se.Kind != oapv1.StepKind_STEP_KIND_AWAIT_READY || !strings.Contains(se.Reason, "not healthy") {
		t.Fatalf("want await-ready to time out, got %v", err)
	}
	cfg, _, _ := w.edge.Config(ctx)
	for _, m := range cfg.Members {
		if m.Instance.Ordinal == 2 {
			t.Fatal("an unhealthy instance must not be routed")
		}
	}
}

// The executor refuses to stop an instance the edge still routes to, even if
// a plan asked for it.
func TestStopRefusesRoutedInstance(t *testing.T) {
	sc := scenarios(t)[1] // scale down
	var steps []*oapv1.Step
	for _, s := range sc.op.Steps {
		if s.Kind != oapv1.StepKind_STEP_KIND_ROUTE_REMOVE {
			steps = append(steps, s)
		}
	}
	sc.op.Steps = steps
	w := newWorld(t, sc)
	err := w.executor().Run(ctx, sc.op)
	var se *StepError
	if !errors.As(err, &se) || se.Kind != oapv1.StepKind_STEP_KIND_STOP || !strings.Contains(se.Reason, "route member") {
		t.Fatalf("want the stop to be refused, got %v", err)
	}
}

func TestJournalRecordsIntentBeforeEffect(t *testing.T) {
	sc := scenarios(t)[0]
	w := newWorld(t, sc)
	if err := w.executor().Run(ctx, sc.op); err != nil {
		t.Fatal(err)
	}
	entries, _ := w.journal.Since(ctx, 0, 1000)
	var seq []string
	for _, e := range entries {
		seq = append(seq, fmt.Sprintf("%d:%s", e.Step, e.State))
	}
	for i := range sc.op.Steps {
		started := indexOf(seq, fmt.Sprintf("%d:%s", i, StateStarted))
		done := indexOf(seq, fmt.Sprintf("%d:%s", i, StateSucceeded))
		if started < 0 || done < started {
			t.Fatalf("step %d: started at %d, succeeded at %d in %v", i, started, done, seq)
		}
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
