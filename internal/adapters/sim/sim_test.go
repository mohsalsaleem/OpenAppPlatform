package sim

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/contract"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)
)

func TestContracts(t *testing.T) {
	t.Run("runtime", func(t *testing.T) {
		var clock *Clock
		contract.Runtime(t, contract.FakeImages, func(t *testing.T) ports.Runtime {
			clock = NewClock(t0)
			return NewRuntime(clock, model.HostObservation{})
		}, func(d time.Duration) { clock.Advance(d) })
	})
	t.Run("world runtime with faults off", func(t *testing.T) {
		var w *World
		contract.Runtime(t, contract.FakeImages, func(t *testing.T) ports.Runtime { w = NewWorld(1, t0, Faults{}); return w.Runtime() },
			func(d time.Duration) { w.Run(d) })
	})
	t.Run("edge", func(t *testing.T) {
		contract.Edge(t, func(t *testing.T) ports.Edge { return NewEdge() })
	})
	t.Run("world edge with reload lag", func(t *testing.T) {
		contract.Edge(t, func(t *testing.T) ports.Edge { return NewWorld(1, t0, Faults{ReloadLag: 2 * time.Second}).Edge() })
	})
	t.Run("journal", func(t *testing.T) {
		contract.OpStore(t, func(t *testing.T) ports.OpStore { return &Journal{} })
	})
	t.Run("desired", func(t *testing.T) {
		contract.Desired(t, func(t *testing.T) ports.DesiredRepo { return NewDesired() }, func(image string) any {
			return map[string]any{"api_version": "oap.dev/v1", "kind": "Application", "name": "contract", "environment": "test",
				"components": map[string]any{"web": map[string]any{"kind": "web", "image": image, "port": int64(80)}}}
		})
	})
	t.Run("secrets", func(t *testing.T) { contract.Secrets(t, Secrets{"db-url": "postgres://x"}) })
	t.Run("clock", func(t *testing.T) { contract.Clock(t, NewClock(t0)) })
	t.Run("world clock", func(t *testing.T) { contract.Clock(t, NewWorld(1, t0, Faults{}).Clock()) })
}

var (
	r1 = ops.InstanceRef{Application: "site", Component: "web", Ordinal: 1, Release: "r-a"}
	r2 = ops.InstanceRef{Application: "site", Component: "web", Ordinal: 2, Release: "r-a"}
)

func member(r ops.InstanceRef) ops.RouteMember {
	return ops.RouteMember{Instance: r, Host: "site.example.com", PathPrefix: "/", Port: 8080}
}

// scaleDown runs a fixed scale-down from two instances to one, by either a
// safe order (remove from route, confirm, drain, stop) or a naive one (stop
// right after removing from the route).
func scaleDown(seed uint64, f Faults, safe bool) *World {
	w := NewWorld(seed, t0, f)
	rt, e := w.Runtime(), w.Edge()
	rt.Start(ctx, ops.InstanceSpec{Ref: r1, Image: "web:1"})
	rt.Start(ctx, ops.InstanceSpec{Ref: r2, Image: "web:1"})
	g, _ := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{member(r1), member(r2)}})
	e.Confirmed(ctx, g)
	w.AddTraffic(Traffic{Host: "site.example.com", Path: "/", Every: 20 * time.Millisecond, MinDuration: 50 * time.Millisecond, MaxDuration: 400 * time.Millisecond})
	w.Run(3 * time.Second)
	g, _ = e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{member(r1)}})
	if safe {
		e.Confirmed(ctx, g)
		for n, _ := e.InFlight(ctx, r2); n > 0; n, _ = e.InFlight(ctx, r2) {
			w.Run(Tick)
		}
	}
	rt.Stop(ctx, r2)
	w.Run(3 * time.Second)
	w.StopTraffic()
	w.Settle(5 * time.Second)
	return w
}

func digest(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))[:16] }

func TestSameSeedSameTrace(t *testing.T) {
	f := Faults{ReloadLag: 300 * time.Millisecond, SlowReadiness: time.Second, ExitDuringDrain: 0.05}
	for seed := uint64(1); seed <= 20; seed++ {
		a, b := scaleDown(seed, f, false), scaleDown(seed, f, false)
		if a.Trace() != b.Trace() || a.Requests != b.Requests || len(a.Failures) != len(b.Failures) {
			t.Fatalf("seed %d: traces differ\n%s\n---\n%s", seed, digest(a.Trace()), digest(b.Trace()))
		}
	}
	if scaleDown(1, f, false).Trace() == scaleDown(2, f, false).Trace() {
		t.Fatal("different seeds should give different traces")
	}
}

func TestSafeScaleDownLosesNothing(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		w := scaleDown(seed, Faults{ReloadLag: 300 * time.Millisecond}, true)
		if len(w.Failures) != 0 || w.Requests < 200 {
			t.Fatalf("seed %d: %d of %d failed: %v", seed, len(w.Failures), w.Requests, w.FailureReasons())
		}
	}
}

func TestFaults(t *testing.T) {
	t.Run("stopping before the reload takes effect refuses requests", func(t *testing.T) {
		w := scaleDown(1, Faults{ReloadLag: 300 * time.Millisecond}, false)
		if !has(w, "connection refused") {
			t.Fatalf("want refused requests, got %v", w.FailureReasons())
		}
	})
	t.Run("stopping with requests in flight resets them", func(t *testing.T) {
		w := scaleDown(1, Faults{}, false)
		if !has(w, "connection reset: instance stopped") {
			t.Fatalf("want reset requests, got %v", w.FailureReasons())
		}
	})
	t.Run("a reload can drop in-flight requests even in a safe order", func(t *testing.T) {
		w := scaleDown(1, Faults{ReloadDropsInFlight: true}, true)
		if !has(w, "connection reset by edge reload") {
			t.Fatalf("want reload resets, got %v", w.FailureReasons())
		}
	})
	t.Run("an instance can exit while draining", func(t *testing.T) {
		w := scaleDown(3, Faults{ExitDuringDrain: 1}, true)
		if !has(w, "connection reset: instance exited during drain") {
			t.Fatalf("want drain exits, got %v", w.FailureReasons())
		}
	})
	t.Run("slow readiness reports starting until ready", func(t *testing.T) {
		w := NewWorld(4, t0, Faults{SlowReadiness: 10 * time.Second})
		w.Runtime().Start(ctx, ops.InstanceSpec{Ref: r1, Image: "web:1"})
		obs, _ := w.Runtime().Observe(ctx)
		first := obs.Instances[0].Health
		w.Run(10 * time.Second)
		obs, _ = w.Runtime().Observe(ctx)
		if first != model.HealthStarting || obs.Instances[0].Health != model.HealthHealthy {
			t.Fatalf("health %v then %v", first, obs.Instances[0].Health)
		}
	})
	t.Run("lost responses still take effect", func(t *testing.T) {
		w := NewWorld(5, t0, Faults{LostResponse: 1})
		_, err := w.Runtime().Start(ctx, ops.InstanceSpec{Ref: r1, Image: "web:1"})
		obs, _ := w.Runtime().Observe(ctx)
		if err != ErrLostResponse || len(obs.Instances) != 1 {
			t.Fatalf("err %v, %d instances", err, len(obs.Instances))
		}
	})
	t.Run("no route fails requests", func(t *testing.T) {
		w := NewWorld(6, t0, Faults{})
		w.AddTraffic(Traffic{Host: "site.example.com", Path: "/", Every: 100 * time.Millisecond, MinDuration: Tick, MaxDuration: Tick})
		w.Run(time.Second)
		if !has(w, "no route") {
			t.Fatal("want no-route failures")
		}
	})
}

func has(w *World, reason string) bool {
	for _, r := range w.FailureReasons() {
		if strings.HasSuffix(r, " "+reason) {
			return true
		}
	}
	return false
}
