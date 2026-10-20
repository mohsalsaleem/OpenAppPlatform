package model

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	now  = time.Date(2026, 10, 19, 14, 5, 0, 0, time.UTC)
	opts = Options{Now: now, MaxAge: time.Minute}
	host = HostObservation{Name: "vps-1", MemoryTotalMiB: 985, MemoryAvailableMiB: proto.Int32(412), MemoryReservedMiB: 448, CPUMillisTotal: 1000}
)

func load(t *testing.T, name string) Desired {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "apps", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	tree, probs := spec.FromJSON(b)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	app, probs := spec.ParseApplication(tree)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	hash, err := spec.Hash(tree)
	if err != nil {
		t.Fatal(err)
	}
	return Desired{App: app, Hash: hash}
}

func release(d Desired, component string) string { return Release(d.App.Components[component]) }

func golden(t *testing.T, name string, w *oapv1.World) {
	t.Helper()
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := json.Indent(&got, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	got.WriteByte('\n')
	path := filepath.Join("testdata", "worlds", name+".json")
	if *update {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	// protojson varies whitespace, so compare after normalizing both sides.
	var a, b bytes.Buffer
	_ = json.Compact(&a, got.Bytes())
	_ = json.Compact(&b, want)
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("%s differs from its golden file:\n%s", name, got.String())
	}
}

func find(w *oapv1.World, app, component string) *oapv1.Component {
	for _, a := range w.Applications {
		if a.Name == app {
			for _, c := range a.Components {
				if c.Name == component {
					return c
				}
			}
		}
	}
	return nil
}

func TestTwoComponentsConvergedAndHealthy(t *testing.T) {
	d := load(t, "two-components")
	obs := &Observation{At: now.Add(-10 * time.Second), Host: host, Instances: []ObservedInstance{
		{ID: "c-api-1", Application: "two-components", Component: "api", Ordinal: 1, Release: release(d, "api"), Image: "nginx:1.27-alpine", Health: HealthHealthy},
		{ID: "c-fe-1", Application: "two-components", Component: "frontend", Ordinal: 1, Release: release(d, "frontend"), Image: "nginx:1.27-alpine", Health: HealthHealthy},
	}}
	w := Build(42, []Desired{d}, obs, opts)
	golden(t, "two-components", w)
	app := w.Applications[0]
	if app.Health.Status != oapv1.HealthStatus_HEALTH_STATUS_HEALTHY {
		t.Fatalf("app health %v", app.Health.Status)
	}
	for _, c := range app.Components {
		if !c.Converged {
			t.Errorf("%s should be converged", c.Name)
		}
	}
}

func topologyMismatch(d Desired) *Observation {
	return &Observation{At: now.Add(-5 * time.Second), Host: host, Instances: []ObservedInstance{
		// The web instance still runs the previous release; no worker is running.
		{ID: "c-web-1", Application: "topology-example", Component: "web", Ordinal: 1, Release: "r-previous", Image: "example/web:1", Health: HealthHealthy},
	}}
}

func TestTopologyMismatch(t *testing.T) {
	d := load(t, "topology")
	w := Build(43, []Desired{d}, topologyMismatch(d), opts)
	golden(t, "topology-mismatch", w)
	web, worker := find(w, "topology-example", "web"), find(w, "topology-example", "worker")
	if web.Converged || web.Actual.Release != "r-previous" || web.Health.Status != oapv1.HealthStatus_HEALTH_STATUS_HEALTHY {
		t.Errorf("web: healthy but not converged expected, got %v", web)
	}
	if worker.Converged || worker.Actual != nil || worker.Health.Status != oapv1.HealthStatus_HEALTH_STATUS_UNHEALTHY {
		t.Errorf("worker: missing instance expected, got %v", worker)
	}
	if w.Applications[0].Health.Status != oapv1.HealthStatus_HEALTH_STATUS_UNHEALTHY {
		t.Errorf("app health should be the worst component's")
	}
}

func TestTopologyStale(t *testing.T) {
	d := load(t, "topology")
	obs := topologyMismatch(d)
	obs.At = now.Add(-10 * time.Minute)
	w := Build(44, []Desired{d}, obs, opts)
	golden(t, "topology-stale", w)
	if !w.Host.Freshness.Stale {
		t.Error("host freshness should be stale")
	}
	for _, c := range w.Applications[0].Components {
		if c.Converged || c.Health.Status != oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN || c.Health.MissingEvidence[0] != MissingFresh {
			t.Errorf("%s: stale observations must not claim health or convergence: %v", c.Name, c.Health)
		}
	}
}

func TestNeverObserved(t *testing.T) {
	d := load(t, "topology")
	w := Build(1, []Desired{d}, nil, opts)
	golden(t, "topology-never-observed", w)
	if c := find(w, "topology-example", "web"); c.Health.MissingEvidence[0] != MissingObservation || c.Actual != nil {
		t.Errorf("web: %v", c)
	}
}

func TestPreDeployJobFailedWithOrphan(t *testing.T) {
	d := load(t, "pre-deploy-jobs")
	api, migrate, db := release(d, "api"), release(d, "migrate"), release(d, "db")
	obs := &Observation{At: now.Add(-3 * time.Second), Host: host,
		Instances: []ObservedInstance{
			{ID: "c-api-1", Application: "shop", Component: "api", Ordinal: 1, Release: "r-old", Image: "registry.example/api:6", Health: HealthHealthy, Routed: true},
			{ID: "c-api-2", Application: "shop", Component: "api", Ordinal: 2, Release: "r-old", Image: "registry.example/api:6", Health: HealthHealthy, Routed: true},
			{ID: "c-db-1", Application: "shop", Component: "db", Ordinal: 1, Release: db, Image: "postgres:17-alpine", Health: HealthHealthy},
			// Left over from a component removed from the spec.
			{ID: "c-legacy-1", Application: "shop", Component: "legacy-worker", Ordinal: 1, Release: "r-gone", Image: "registry.example/legacy:1", Health: HealthUnhealthy},
		},
		Jobs: []JobRun{
			{Application: "shop", Component: "migrate", Release: "r-old", Status: JobSucceeded},
			{Application: "shop", Component: "migrate", Release: migrate, Status: JobFailed},
		},
	}
	w := Build(57, []Desired{d}, obs, opts)
	golden(t, "pre-deploy-jobs-failed", w)
	if c := find(w, "shop", "migrate"); c.Converged || c.Health.Status != oapv1.HealthStatus_HEALTH_STATUS_UNHEALTHY {
		t.Errorf("migrate failed for the desired release: %v", c)
	}
	if c := find(w, "shop", "api"); c.Converged || c.Actual.Release != "r-old" || c.Desired.Release != api {
		t.Errorf("api still runs the old release: %v", c)
	}
	if c := find(w, "shop", "db"); !c.Converged {
		t.Errorf("db should be converged: %v", c)
	}
	if c := find(w, "shop", "legacy-worker"); c == nil || c.Desired != nil || c.Converged {
		t.Errorf("leftover component must appear without desired state: %v", c)
	}
	app := w.Applications[0]
	if len(app.Edges) != 2 || app.Edges[0].Type != oapv1.EdgeType_EDGE_TYPE_DATABASE || app.Edges[1].Type != oapv1.EdgeType_EDGE_TYPE_ROUTE {
		t.Errorf("edges: %v", app.Edges)
	}
}

func TestJobStates(t *testing.T) {
	d := load(t, "pre-deploy-jobs")
	migrate := release(d, "migrate")
	for _, c := range []struct {
		runs    []JobRun
		status  oapv1.HealthStatus
		missing string
		done    bool
	}{
		{nil, oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN, MissingJobRun, false},
		{[]JobRun{{Application: "shop", Component: "migrate", Release: migrate, Status: JobRunning}}, oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN, MissingJobResult, false},
		{[]JobRun{{Application: "shop", Component: "migrate", Release: migrate, Status: JobFailed},
			{Application: "shop", Component: "migrate", Release: migrate, Status: JobSucceeded}}, oapv1.HealthStatus_HEALTH_STATUS_HEALTHY, "", true},
		{[]JobRun{{Application: "shop", Component: "migrate", Release: "r-other", Status: JobSucceeded}}, oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN, MissingJobRun, false},
	} {
		w := Build(1, []Desired{d}, &Observation{At: now, Host: host, Jobs: c.runs}, opts)
		got := find(w, "shop", "migrate")
		missing := ""
		if len(got.Health.MissingEvidence) > 0 {
			missing = got.Health.MissingEvidence[0]
		}
		if got.Health.Status != c.status || missing != c.missing || got.Converged != c.done {
			t.Errorf("runs %v: got %v %q converged=%v", c.runs, got.Health.Status, missing, got.Converged)
		}
	}
}

func TestReleaseChangesWithAnyComponentChange(t *testing.T) {
	d := load(t, "pre-deploy-jobs")
	c := d.App.Components["api"]
	base := Release(c)
	c.Env = map[string]string{"X": "1"}
	if Release(c) == base {
		t.Fatal("env change must change the release")
	}
	c = d.App.Components["api"]
	c.Instances = 3
	if Release(c) != base {
		t.Fatal("scaling must keep the release, or every scale would replace running instances")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	d := load(t, "pre-deploy-jobs")
	obs := &Observation{At: now, Host: host, Instances: []ObservedInstance{
		{ID: "b", Application: "shop", Component: "api", Ordinal: 2, Release: "r1", Health: HealthHealthy},
		{ID: "a", Application: "shop", Component: "api", Ordinal: 1, Release: "r1", Health: HealthStarting},
		{ID: "z", Application: "other", Component: "x", Ordinal: 1, Release: "r1"},
	}}
	first, _ := protojson.Marshal(Build(9, []Desired{d}, obs, opts))
	obs.Instances[0], obs.Instances[2] = obs.Instances[2], obs.Instances[0]
	second, _ := protojson.Marshal(Build(9, []Desired{d}, obs, opts))
	var a, b bytes.Buffer
	_ = json.Compact(&a, first)
	_ = json.Compact(&b, second)
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("observation order must not change the world model")
	}
}
