package engine

import (
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// VPS-H-001: on the 15 October VPS run, 2 of 277 requests failed while
// scaling 3 → 1, near a Caddy reload (docs/evidence/release-journey/
// 2026-10-15-11d5b85/findings.json). These tests run the real executor on a
// simulated host under steady traffic.

// scaleDownFaults draws a seed's edge reload lag: Caddy applies a new
// configuration some time after the admin API returns, up to a second here.
func scaleDownFaults(seed uint64) sim.Faults {
	return sim.Faults{ReloadLag: time.Duration(seed*7919%101) * sim.Tick}
}

// scaleDownEvery draws a seed's interval between requests on the main
// route, from 20 ms to 500 ms. Dense traffic keeps a removed instance busy
// until the reload takes effect; sparse traffic leaves gaps in which it is
// idle while the old configuration still routes to it.
func scaleDownEvery(seed uint64) time.Duration {
	return time.Duration(2+seed*104729%49) * sim.Tick
}

// naive drops the steps that make a removal safe, leaving route-remove
// followed straight by stop: the order a reconciler uses when it doesn't
// wait for the reload to take effect or for requests to finish.
func naive(steps []*oapv1.Step) []*oapv1.Step {
	var out []*oapv1.Step
	for _, s := range steps {
		if s.Kind != oapv1.StepKind_STEP_KIND_EDGE_RELOAD_CONFIRMED && s.Kind != oapv1.StepKind_STEP_KIND_DRAIN {
			out = append(out, s)
		}
	}
	return out
}

// scaleDown runs one 3 → 1 scale-down with the given steps under traffic
// and returns the world, with every request and failure recorded.
func scaleDown(t testing.TB, sc scenario, seed uint64, steps []*oapv1.Step) *sim.World {
	t.Helper()
	w := sim.NewWorld(seed, time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC), scaleDownFaults(seed))
	rt, edge := w.Runtime(), w.Edge()
	desired := sim.NewDesired()
	if _, err := desired.Commit(ctx, sc.op.Application, sc.oldTree, "owner", "initial"); err != nil {
		t.Fatal(err)
	}
	old := sc.op.Old
	cfg := ops.EdgeConfig{}
	for i := 1; i <= old.Components["web"].Instances; i++ {
		s := ops.InstanceSpecFor(old, "web", i)
		if _, err := rt.Start(ctx, s); err != nil {
			t.Fatal(err)
		}
		cfg = ops.WithMembers(cfg, ops.Members(old, s.Ref)...)
	}
	gen, _ := edge.Apply(ctx, cfg)
	if err := edge.Confirmed(ctx, gen); err != nil {
		t.Fatal(err)
	}
	w.AddTraffic(sim.Traffic{Host: "site.example.com", Path: "/", Every: scaleDownEvery(seed),
		MinDuration: 50 * time.Millisecond, MaxDuration: 400 * time.Millisecond})
	w.AddTraffic(sim.Traffic{Host: "site.example.com", Path: "/api/items", Every: 50 * time.Millisecond,
		MinDuration: 20 * time.Millisecond, MaxDuration: 150 * time.Millisecond})
	w.Run(2 * time.Second)

	op := sc.op
	op.Steps = steps
	x := &Executor{Runtime: rt, Edge: edge, Journal: &sim.Journal{}, Desired: desired, Clock: w.Clock(),
		Poll: 100 * time.Millisecond}
	if err := x.Run(ctx, op); err != nil {
		t.Fatalf("seed %d: %v", seed, err)
	}
	w.Run(2 * time.Second)
	w.StopTraffic()
	w.Settle(5 * time.Second)
	if inner, _ := w.Inner(); len(inner.Running()) != 1 {
		t.Fatalf("seed %d: %d instances running after scaling to 1", seed, len(inner.Running()))
	}
	return w
}

func scaleThreeToOne(t testing.TB) scenario {
	for _, sc := range scenarios(t) {
		if sc.name == "scale 3 to 1" {
			return sc
		}
	}
	t.Fatal("no scale 3 to 1 scenario")
	return scenario{}
}

// TestScaleDownNaiveOrderLosesRequests reproduces VPS-H-001: removing an
// instance from the route and stopping it without a confirmed reload and a
// drain loses requests, as refused connections (the instance is stopped
// while the old configuration still routes to it) and resets (requests in
// flight when it stops). The VPS run saw one of each: URLError and
// RemoteDisconnected.
func TestScaleDownNaiveOrderLosesRequests(t *testing.T) {
	sc := scaleThreeToOne(t)
	seeds := uint64(1000)
	if raceBuild {
		seeds = 50
	}
	lost, refused, reset := 0, 0, 0
	requests, failed := 0, 0
	for seed := uint64(1); seed <= seeds; seed++ {
		w := scaleDown(t, sc, seed, naive(sc.op.Steps))
		requests += w.Requests
		failed += len(w.Failures)
		if len(w.Failures) > 0 {
			lost++
		}
		for _, f := range w.Failures {
			switch f.Reason {
			case "connection refused":
				refused++
			case "connection reset: instance stopped":
				reset++
			default:
				t.Fatalf("seed %d: unexpected failure %q", seed, f.Reason)
			}
		}
	}
	t.Logf("naive order: %d of %d seeds lost requests; %d of %d requests failed (%d refused, %d reset)",
		lost, seeds, failed, requests, refused, reset)
	if uint64(lost) != seeds || refused == 0 || reset == 0 {
		t.Fatalf("want losses on every seed, both refused and reset; got %d of %d seeds, %d refused, %d reset",
			lost, seeds, refused, reset)
	}
}

// TestScaleDownPlannerOrderLosesNothing is the fix for VPS-H-001: with the
// planner's traffic-safety rule (route-remove → edge-reload-confirmed →
// drain → stop), the same scale-down loses no request on any seed.
func TestScaleDownPlannerOrderLosesNothing(t *testing.T) {
	sc := scaleThreeToOne(t)
	seeds := uint64(1000)
	if raceBuild {
		seeds = 50
	}
	requests := 0
	for seed := uint64(1); seed <= seeds; seed++ {
		w := scaleDown(t, sc, seed, sc.op.Steps)
		requests += w.Requests
		if len(w.Failures) > 0 {
			t.Fatalf("seed %d (reload lag %s, a request every %s): %d of %d requests failed: %v\n%s", seed,
				scaleDownFaults(seed).ReloadLag, scaleDownEvery(seed), len(w.Failures), w.Requests, w.FailureReasons(), w.Trace())
		}
	}
	t.Logf("planner order: 0 of %d requests failed over %d seeds", requests, seeds)
}
