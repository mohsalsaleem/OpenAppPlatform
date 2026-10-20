package engine

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

var simStart = time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)

// picture renders an observation's instances and their health, sorted.
func picture(obs model.Observation) string {
	var lines []string
	for _, in := range obs.Instances {
		lines = append(lines, fmt.Sprintf("%s/%s#%d@%s=%d", in.Application, in.Component, in.Ordinal, in.Release, in.Health))
	}
	sort.Strings(lines)
	return fmt.Sprint(lines)
}

// churn starts and stops instances at random while the observer polls every
// 100 ms. It returns how long the observer's picture differed from the truth
// at most, and how many times it differed at all.
func churn(t *testing.T, seed uint64, faults sim.Faults, interval time.Duration) (longest time.Duration, divergences int) {
	t.Helper()
	w := sim.NewWorld(seed, simStart, faults)
	rt := w.Runtime()
	o := &Observer{Runtime: rt, Clock: w.Clock(), Interval: interval}
	if err := o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, 9))
	var live []ops.InstanceRef
	var since time.Time
	for step := 0; step < 1200; step++ { // two simulated minutes
		if step%10 == 0 {
			switch {
			case r.IntN(2) == 0 || len(live) == 0:
				ref := ops.InstanceRef{Application: "a", Component: "web", Ordinal: step, Release: "r-1"}
				rt.Start(ctx, ops.InstanceSpec{Ref: ref, Image: "web:1"})
				live = append(live, ref)
			default:
				i := r.IntN(len(live))
				rt.Stop(ctx, live[i])
				live = append(live[:i], live[i+1:]...)
			}
		}
		w.Run(100 * time.Millisecond)
		if err := o.Poll(ctx); err != nil {
			t.Fatal(err)
		}
		truth, _ := rt.Observe(ctx)
		now := w.Clock().Now()
		if picture(o.Snapshot()) == picture(truth) {
			since = time.Time{}
			continue
		}
		if since.IsZero() {
			since = now
			divergences++
		}
		if d := now.Sub(since); d > longest {
			longest = d
		}
	}
	return longest, divergences
}

func TestObserverHealsMissedEventsWithinOneInterval(t *testing.T) {
	const interval = 5 * time.Second
	// 20 seeds take seconds normally and minutes under the race detector, so
	// race builds run 4; make rewrite-check also runs all 20 without race.
	seeds := uint64(20)
	if raceBuild {
		seeds = 4
	}
	total := 0
	for seed := uint64(1); seed <= seeds; seed++ {
		longest, n := churn(t, seed, sim.Faults{MissedEvents: 0.5, SlowReadiness: 2 * time.Second}, interval)
		if longest > interval {
			t.Fatalf("seed %d: the picture stayed wrong for %s, longer than one interval (%s)", seed, longest, interval)
		}
		total += n
	}
	if total == 0 {
		t.Fatal("no events were missed; the test proves nothing")
	}
}

func TestObserverFollowsEventsBetweenResyncs(t *testing.T) {
	// With no missed events and a long interval, events alone keep the picture right.
	seeds := uint64(5)
	if raceBuild {
		seeds = 2
	}
	for seed := uint64(1); seed <= seeds; seed++ {
		if _, n := churn(t, seed, sim.Faults{SlowReadiness: 2 * time.Second}, time.Hour); n != 0 {
			t.Fatalf("seed %d: picture diverged %d times without any missed event", seed, n)
		}
	}
}

func TestFreshnessOnEveryNode(t *testing.T) {
	w := sim.NewWorld(1, simStart, sim.Faults{})
	app := &spec.Application{Name: "a", Components: map[string]spec.Component{
		"web": {Kind: spec.KindWeb, Image: "web:1", Port: 80, Instances: 2},
	}}
	for i := 1; i <= 2; i++ {
		w.Runtime().Start(ctx, ops.InstanceSpecFor(app, "web", i))
	}
	o := &Observer{Runtime: w.Runtime(), Clock: w.Clock(), Interval: time.Second}
	if err := o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	snap := o.Snapshot()
	world := model.Build(1, []model.Desired{{App: app, Hash: "sha256:x"}}, &snap, model.Options{Now: w.Clock().Now(), MaxAge: time.Minute})
	fresh := func(what string, f *oapv1.Freshness) {
		if f == nil || f.ObservedAt == nil || f.Stale {
			t.Errorf("%s: freshness %v", what, f)
		}
	}
	fresh("host", world.Host.Freshness)
	nodes := 0
	for _, a := range world.Applications {
		fresh("application "+a.Name, a.Health.Freshness)
		for _, c := range a.Components {
			fresh("component "+c.Name, c.Health.Freshness)
			for _, in := range c.Instances {
				fresh(fmt.Sprintf("instance %s#%d", c.Name, in.Ordinal), in.Health.Freshness)
				nodes++
			}
		}
	}
	if nodes != 2 {
		t.Fatalf("%d instances", nodes)
	}
	// Events keep the picture current but don't refresh it: freshness is the
	// last full observation.
	w.Run(2 * time.Minute)
	o.Poll(ctx)
	if got := o.Snapshot().At; !got.Equal(w.Clock().Now()) {
		t.Fatalf("a due resync should refresh the snapshot time: %s vs %s", got, w.Clock().Now())
	}
}
