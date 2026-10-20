// Package contract holds one test suite per port. The simulation fakes pass
// them now; the real adapters must pass the same suites, which is what keeps
// the simulation honest.
package contract

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

var ctx = context.Background()

func ref(component string, ordinal int, release string) ops.InstanceRef {
	return ops.InstanceRef{Application: "contract", Component: component, Ordinal: ordinal, Release: release}
}

func running(t *testing.T, rt ports.Runtime, r ops.InstanceRef) (model.ObservedInstance, bool) {
	t.Helper()
	obs, err := rt.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range obs.Instances {
		if in.Application == r.Application && in.Component == r.Component && int(in.Ordinal) == r.Ordinal && in.Release == r.Release {
			return in, true
		}
	}
	return model.ObservedInstance{}, false
}

// Images names the images the runtime suite starts. Fakes accept any name;
// a real runtime needs images it can pull: Web1 and Web2 are two different
// long-running servers, DB a long-running image that may mount a volume, and
// Job one that exits 0.
type Images struct{ Web1, Web2, DB, Job string }

// FakeImages are names for runtimes that never pull.
var FakeImages = Images{Web1: "contract/web:1", Web2: "contract/web:2", DB: "contract/db:1", Job: "contract/migrate:1"}

// Runtime checks a Runtime. newRuntime returns a runtime with nothing
// OAP-labelled on it; wait lets time pass for runtimes that need it.
func Runtime(t *testing.T, img Images, newRuntime func(t *testing.T) ports.Runtime, wait func(time.Duration)) {
	t.Run("start is idempotent by ref and labels the instance", func(t *testing.T) {
		rt := newRuntime(t)
		s := ops.InstanceSpec{Ref: ref("web", 1, "r-1"), Image: img.Web1, Port: 8080}
		id1, err := rt.Start(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		id2, err := rt.Start(ctx, s)
		if err != nil || id1 != id2 || id1 == "" {
			t.Fatalf("second start: %q %q %v", id1, id2, err)
		}
		in, ok := running(t, rt, s.Ref)
		if !ok || in.ID != id1 || in.Image != s.Image {
			t.Fatalf("observed %+v %v", in, ok)
		}
	})
	t.Run("different releases are different instances", func(t *testing.T) {
		rt := newRuntime(t)
		a, _ := rt.Start(ctx, ops.InstanceSpec{Ref: ref("web", 1, "r-1"), Image: img.Web1})
		b, _ := rt.Start(ctx, ops.InstanceSpec{Ref: ref("web", 1, "r-2"), Image: img.Web2})
		if a == b {
			t.Fatal("same id for two releases")
		}
	})
	t.Run("stop is idempotent and stopping nothing succeeds", func(t *testing.T) {
		rt := newRuntime(t)
		r := ref("web", 1, "r-1")
		if err := rt.Stop(ctx, r); err != nil {
			t.Fatalf("stopping an absent instance: %v", err)
		}
		rt.Start(ctx, ops.InstanceSpec{Ref: r, Image: img.Web1})
		if err := rt.Stop(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := rt.Stop(ctx, r); err != nil {
			t.Fatal(err)
		}
		if _, ok := running(t, rt, r); ok {
			t.Fatal("still running after stop")
		}
	})
	t.Run("a job runs once per release and reports its result", func(t *testing.T) {
		rt := newRuntime(t)
		j := ops.JobSpec{Ref: ref("migrate", 1, "r-1"), Image: img.Job, Timeout: time.Minute}
		if err := rt.RunJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		if err := rt.RunJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		var runs []model.JobRun
		for deadline := 0; deadline < 60; deadline++ {
			obs, err := rt.Observe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			runs = nil
			for _, r := range obs.Jobs {
				if r.Application == "contract" && r.Component == "migrate" && r.Release == "r-1" {
					runs = append(runs, r)
				}
			}
			if len(runs) == 1 && runs[0].Status != model.JobRunning {
				break
			}
			wait(time.Second)
		}
		if len(runs) != 1 || runs[0].Status != model.JobSucceeded {
			t.Fatalf("job runs %+v", runs)
		}
	})
	t.Run("snapshots are idempotent by key; deleting a missing volume succeeds", func(t *testing.T) {
		rt := newRuntime(t)
		rt.Start(ctx, ops.InstanceSpec{Ref: ref("db", 1, "r-1"), Image: img.DB, Volumes: map[string]string{"data": "/data"}})
		a, err := rt.SnapshotVolume(ctx, "k1", "contract", "db", "data")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := rt.SnapshotVolume(ctx, "k1", "contract", "db", "data")
		c, _ := rt.SnapshotVolume(ctx, "k2", "contract", "db", "data")
		if a != b || a == c || a == "" {
			t.Fatalf("snapshots %q %q %q", a, b, c)
		}
		if err := rt.DeleteVolume(ctx, "contract", "db", "nope"); err != nil {
			t.Fatal(err)
		}
	})
}

// Edge checks an Edge.
func Edge(t *testing.T, newEdge func(t *testing.T) ports.Edge) {
	member := func(release string) ops.RouteMember {
		return ops.RouteMember{Instance: ref("web", 1, release), Host: "contract.example.com", PathPrefix: "/", Port: 8080}
	}
	t.Run("apply returns a new generation only for a new config", func(t *testing.T) {
		e := newEdge(t)
		_, g0, err := e.Config(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cfg := ops.EdgeConfig{Members: []ops.RouteMember{member("r-1")}}
		g1, err := e.Apply(ctx, cfg)
		if err != nil || g1 <= g0 {
			t.Fatalf("apply: %d after %d, %v", g1, g0, err)
		}
		if again, _ := e.Apply(ctx, cfg); again != g1 {
			t.Fatalf("same config gave generation %d, want %d", again, g1)
		}
		got, g, _ := e.Config(ctx)
		if g != g1 || len(got.Members) != 1 || got.Members[0] != member("r-1") {
			t.Fatalf("config %+v at %d", got, g)
		}
		g2, _ := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{member("r-2")}})
		if g2 <= g1 {
			t.Fatalf("generation went from %d to %d", g1, g2)
		}
	})
	t.Run("confirmed returns once a generation is in effect", func(t *testing.T) {
		e := newEdge(t)
		g, err := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{member("r-1")}})
		if err != nil {
			t.Fatal(err)
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := e.Confirmed(cctx, g); err != nil {
			t.Fatal(err)
		}
		if err := e.Confirmed(cctx, g+100); err == nil {
			t.Fatal("confirming a generation never applied must fail")
		}
	})
	t.Run("in-flight counts are never negative", func(t *testing.T) {
		e := newEdge(t)
		if n, err := e.InFlight(ctx, ref("web", 9, "r-x")); err != nil || n < 0 {
			t.Fatalf("in flight %d %v", n, err)
		}
	})
}

// OpStore checks an OpStore.
func OpStore(t *testing.T, newStore func(t *testing.T) ports.OpStore) {
	at := time.Date(2026, 10, 19, 14, 5, 0, 0, time.UTC)
	e := func(kind string) ops.Entry {
		return ops.Entry{At: at, Kind: kind, Operation: "op", Step: 1, State: "started", Data: []byte(kind)}
	}
	t.Run("revisions are gapless and ordered", func(t *testing.T) {
		s := newStore(t)
		if h, _ := s.Head(ctx); h != 0 {
			t.Fatalf("head %d", h)
		}
		r1, _ := s.Append(ctx, e("a"))
		r3, _ := s.Append(ctx, e("b"), e("c"))
		if r1 != 1 || r3 != 3 {
			t.Fatalf("revisions %d %d", r1, r3)
		}
		got, err := s.Since(ctx, 0, 10)
		if err != nil || len(got) != 3 {
			t.Fatalf("since %v %v", got, err)
		}
		for i, g := range got {
			if g.Revision != uint64(i+1) || !g.At.Equal(at) || g.Operation != "op" || g.Step != 1 || g.State != "started" {
				t.Fatalf("entry %d: %+v", i, g)
			}
		}
		if page, _ := s.Since(ctx, 1, 1); len(page) != 1 || page[0].Revision != 2 || string(page[0].Data) != "b" {
			t.Fatalf("page %+v", page)
		}
		if h, _ := s.Head(ctx); h != 3 {
			t.Fatalf("head %d", h)
		}
	})
}

// Desired checks a DesiredRepo.
func Desired(t *testing.T, newRepo func(t *testing.T) ports.DesiredRepo, app func(image string) any) {
	t.Run("commit, get and look up earlier versions", func(t *testing.T) {
		d := newRepo(t)
		if _, _, err := d.Get(ctx, "contract"); !errors.Is(err, ops.ErrNoApplication) {
			t.Fatalf("a missing application must be ops.ErrNoApplication, got %v", err)
		}
		if apps, err := d.Applications(ctx); err != nil || len(apps) != 0 {
			t.Fatalf("applications before any commit: %v %v", apps, err)
		}
		v1, v2 := app("contract/web:1"), app("contract/web:2")
		h1, err := d.Commit(ctx, "contract", v1, "owner", "first")
		if err != nil {
			t.Fatal(err)
		}
		if again, _ := d.Commit(ctx, "contract", v1, "owner", "same"); again != h1 {
			t.Fatal("committing the same tree must keep its hash")
		}
		h2, _ := d.Commit(ctx, "contract", v2, "agent:x", "second")
		if apps, err := d.Applications(ctx); err != nil || len(apps) != 1 || apps[0] != "contract" {
			t.Fatalf("applications: %v %v", apps, err)
		}
		_, cur, err := d.Get(ctx, "contract")
		if err != nil || cur != h2 || h1 == h2 {
			t.Fatalf("get: %s, want %s", cur, h2)
		}
		if _, ok, _ := d.ByHash(ctx, h1); !ok {
			t.Fatal("earlier version not found by hash")
		}
		if _, ok, _ := d.ByHash(ctx, "sha256:missing"); ok {
			t.Fatal("unknown hash found")
		}
		env := map[string]any{"agents": map[string]any{"a": map[string]any{"may": []any{"scale"}}}}
		v, err := d.SetEnvelope(ctx, env, "owner", "envelope")
		if err != nil {
			t.Fatal(err)
		}
		got, cur, err := d.Envelope(ctx)
		if err != nil || cur != v || fmt.Sprint(got) != fmt.Sprint(env) {
			t.Fatalf("envelope after SetEnvelope: %v %s (want %s) %v", got, cur, v, err)
		}
	})
}

// Secrets checks a Secrets port that knows the ref "db-url".
func Secrets(t *testing.T, s ports.Secrets) {
	got, err := s.Resolve(ctx, map[string]string{"DATABASE_URL": "db-url"})
	if err != nil || got["DATABASE_URL"] == "" {
		t.Fatalf("resolve: %v %v", got, err)
	}
	if _, err := s.Resolve(ctx, map[string]string{"X": "missing-ref"}); err == nil {
		t.Fatal("an unknown reference must be an error")
	}
}

// Clock checks a Clock.
func Clock(t *testing.T, c ports.Clock) {
	before := c.Now()
	if err := c.Sleep(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if !c.Now().After(before) {
		t.Fatal("time did not advance across Sleep")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Sleep(cancelled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep with a cancelled context: %v", err)
	}
}
