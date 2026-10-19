package sim

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

// Tick is the simulation's time step.
const Tick = 10 * time.Millisecond

// Faults configures what goes wrong in a world.
type Faults struct {
	// ReloadLag is how long a new edge configuration takes to come into
	// effect after Apply returns. Until then the old one keeps routing.
	ReloadLag time.Duration
	// ReloadDropsInFlight makes every request in flight fail when a new
	// configuration comes into effect, as a reload that resets connections.
	ReloadDropsInFlight bool
	// ExitDuringDrain is the chance that an instance exits, failing its
	// in-flight requests, each time the edge is asked about them.
	ExitDuringDrain float64
	// SlowReadiness is the longest extra time an instance takes to become
	// healthy; each instance draws a delay up to this.
	SlowReadiness time.Duration
	// LostResponse is the chance that a mutating call takes effect but
	// returns ErrLostResponse.
	LostResponse float64
}

// ErrLostResponse is returned when a call's effect happened but its response
// was lost.
var ErrLostResponse = errors.New("sim: response lost")

// Traffic describes a steady stream of requests to one route.
type Traffic struct {
	Host, Path string
	// Every is the interval between requests.
	Every time.Duration
	// MinDuration and MaxDuration bound how long a request takes.
	MinDuration, MaxDuration time.Duration
}

// RequestFailure is one failed request.
type RequestFailure struct {
	At     time.Time
	Target string
	Reason string
}

type request struct {
	id     int
	target ops.InstanceRef
	ends   time.Time
}

type generation struct {
	cfg       ops.EdgeConfig
	effective time.Time
}

// World is a deterministic simulated host: clock, runtime, edge and traffic,
// all driven by one seeded random source and the virtual clock.
type World struct {
	clock   *Clock
	rng     *rand.Rand
	faults  Faults
	runtime *Runtime
	edge    *Edge

	gens      map[uint64]generation
	effective uint64 // generation currently routing traffic
	readyAt   map[ops.InstanceRef]time.Time

	traffic   []Traffic
	nextSend  []time.Time
	rr        map[string]int
	inFlight  []request
	requestID int

	Requests int
	Failures []RequestFailure
	trace    []string
}

// NewWorld returns a world starting at start, seeded by seed.
func NewWorld(seed uint64, start time.Time, faults Faults) *World {
	clock := NewClock(start)
	w := &World{clock: clock, rng: rand.New(rand.NewPCG(seed, 0x0a9)), faults: faults,
		runtime: NewRuntime(clock, model.HostObservation{Name: "sim", MemoryTotalMiB: 985, CPUMillisTotal: 1000}),
		edge:    NewEdge(), gens: map[uint64]generation{0: {}}, readyAt: map[ops.InstanceRef]time.Time{}, rr: map[string]int{}}
	return w
}

// Ports.
func (w *World) Clock() ports.Clock     { return worldClock{w} }
func (w *World) Runtime() ports.Runtime { return worldRuntime{w} }
func (w *World) Edge() ports.Edge       { return worldEdge{w} }

// Inner gives tests the underlying fakes, for counters and setup.
func (w *World) Inner() (*Runtime, *Edge) { return w.runtime, w.edge }

// AddTraffic starts a stream of requests now.
func (w *World) AddTraffic(t Traffic) {
	w.traffic = append(w.traffic, t)
	w.nextSend = append(w.nextSend, w.clock.Now())
}

// StopTraffic stops new requests; requests in flight finish.
func (w *World) StopTraffic() { w.traffic, w.nextSend = nil, nil }

// Run advances the world by d.
func (w *World) Run(d time.Duration) {
	for end := w.clock.Now().Add(d); w.clock.Now().Before(end); {
		w.step()
	}
}

// Settle runs until no request is in flight, at most d.
func (w *World) Settle(d time.Duration) {
	for end := w.clock.Now().Add(d); len(w.inFlight) > 0 && w.clock.Now().Before(end); {
		w.step()
	}
}

// Trace returns every event so far, one per line.
func (w *World) Trace() string { return strings.Join(w.trace, "\n") }

func (w *World) log(format string, args ...any) {
	w.trace = append(w.trace, w.clock.Now().Format("15:04:05.000")+" "+fmt.Sprintf(format, args...))
}

func (w *World) fail(target ops.InstanceRef, reason string) {
	w.Failures = append(w.Failures, RequestFailure{At: w.clock.Now(), Target: refName(target), Reason: reason})
	w.log("request to %s failed: %s", refName(target), reason)
}

func refName(r ops.InstanceRef) string {
	if r.Component == "" {
		return "-"
	}
	return fmt.Sprintf("%s/%s#%d@%s", r.Application, r.Component, r.Ordinal, r.Release)
}

// step advances one tick: configurations come into effect, requests finish,
// new requests are sent.
func (w *World) step() {
	w.clock.Advance(Tick)
	now := w.clock.Now()
	for g := w.effective + 1; ; g++ {
		gen, ok := w.gens[g]
		if !ok || now.Before(gen.effective) {
			break
		}
		w.effective = g
		w.log("edge generation %d in effect (%d members)", g, len(gen.cfg.Members))
		if w.faults.ReloadDropsInFlight && len(w.inFlight) > 0 {
			for _, r := range w.inFlight {
				w.fail(r.target, "connection reset by edge reload")
			}
			w.inFlight = nil
		}
	}
	kept := w.inFlight[:0]
	for _, r := range w.inFlight {
		if now.Before(r.ends) {
			kept = append(kept, r)
		}
	}
	w.inFlight = kept
	for i, t := range w.traffic {
		for !now.Before(w.nextSend[i]) {
			w.send(t)
			w.nextSend[i] = w.nextSend[i].Add(t.Every)
		}
	}
}

// send routes one request by the configuration in effect, round robin over
// the members of the longest matching path prefix.
func (w *World) send(t Traffic) {
	w.Requests++
	cfg := w.gens[w.effective].cfg
	best := ""
	var members []ops.RouteMember
	for _, m := range cfg.Members {
		if m.Host != t.Host || !strings.HasPrefix(t.Path, m.PathPrefix) || len(m.PathPrefix) < len(best) {
			continue
		}
		if len(m.PathPrefix) > len(best) {
			best, members = m.PathPrefix, nil
		}
		members = append(members, m)
	}
	if len(members) == 0 {
		w.fail(ops.InstanceRef{}, "no route")
		return
	}
	key := t.Host + t.Path
	target := members[w.rr[key]%len(members)].Instance
	w.rr[key]++
	if _, running := w.runtime.instances[target]; !running {
		w.fail(target, "connection refused")
		return
	}
	d := t.MinDuration
	if span := t.MaxDuration - t.MinDuration; span > 0 {
		d += time.Duration(w.rng.Int64N(int64(span)))
	}
	w.requestID++
	w.inFlight = append(w.inFlight, request{id: w.requestID, target: target, ends: w.clock.Now().Add(d)})
}

// dropRequests fails every request in flight to an instance.
func (w *World) dropRequests(ref ops.InstanceRef, reason string) {
	kept := w.inFlight[:0]
	for _, r := range w.inFlight {
		if r.target == ref {
			w.fail(r.target, reason)
		} else {
			kept = append(kept, r)
		}
	}
	w.inFlight = kept
}

func (w *World) lostResponse(call string) error {
	if w.faults.LostResponse > 0 && w.rng.Float64() < w.faults.LostResponse {
		w.log("%s: response lost", call)
		return ErrLostResponse
	}
	return nil
}

type worldClock struct{ w *World }

func (c worldClock) Now() time.Time { return c.w.clock.Now() }

func (c worldClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.w.Run(d)
	return nil
}

type worldRuntime struct{ w *World }

func (r worldRuntime) Start(ctx context.Context, s ops.InstanceSpec) (string, error) {
	w := r.w
	_, existed := w.runtime.instances[s.Ref]
	id, err := w.runtime.Start(ctx, s)
	if err != nil {
		return "", err
	}
	if !existed {
		delay := time.Duration(0)
		if w.faults.SlowReadiness > 0 {
			delay = time.Duration(w.rng.Int64N(int64(w.faults.SlowReadiness)))
		}
		w.readyAt[s.Ref] = w.clock.Now().Add(delay)
		w.log("started %s (ready in %s)", refName(s.Ref), delay)
	}
	return id, w.lostResponse("start " + refName(s.Ref))
}

func (r worldRuntime) Stop(ctx context.Context, ref ops.InstanceRef) error {
	w := r.w
	if _, running := w.runtime.instances[ref]; running {
		w.dropRequests(ref, "connection reset: instance stopped")
		w.log("stopped %s", refName(ref))
	}
	if err := w.runtime.Stop(ctx, ref); err != nil {
		return err
	}
	return w.lostResponse("stop " + refName(ref))
}

func (r worldRuntime) RunJob(ctx context.Context, s ops.JobSpec) error {
	if err := r.w.runtime.RunJob(ctx, s); err != nil {
		return err
	}
	r.w.log("job %s ran", refName(s.Ref))
	return r.w.lostResponse("run job " + refName(s.Ref))
}

func (r worldRuntime) Observe(ctx context.Context) (model.Observation, error) {
	obs, err := r.w.runtime.Observe(ctx)
	for i, in := range obs.Instances {
		ref := ops.InstanceRef{Application: in.Application, Component: in.Component, Ordinal: int(in.Ordinal), Release: in.Release}
		if ready, ok := r.w.readyAt[ref]; ok && in.Health == model.HealthHealthy && r.w.clock.Now().Before(ready) {
			obs.Instances[i].Health = model.HealthStarting
		}
	}
	return obs, err
}

func (r worldRuntime) Events(ctx context.Context) (<-chan ops.RuntimeEvent, error) {
	return r.w.runtime.Events(ctx)
}

func (r worldRuntime) SnapshotVolume(ctx context.Context, key, app, component, volume string) (string, error) {
	return r.w.runtime.SnapshotVolume(ctx, key, app, component, volume)
}

func (r worldRuntime) DeleteVolume(ctx context.Context, app, component, volume string) error {
	return r.w.runtime.DeleteVolume(ctx, app, component, volume)
}

type worldEdge struct{ w *World }

func (e worldEdge) Config(ctx context.Context) (ops.EdgeConfig, uint64, error) {
	return e.w.edge.Config(ctx)
}

func (e worldEdge) Apply(ctx context.Context, cfg ops.EdgeConfig) (uint64, error) {
	w := e.w
	before := w.edge.generation
	gen, err := w.edge.Apply(ctx, cfg)
	if err != nil {
		return 0, err
	}
	if gen != before {
		w.gens[gen] = generation{cfg: copyConfig(cfg), effective: w.clock.Now().Add(w.faults.ReloadLag)}
		w.log("edge generation %d applied (%d members)", gen, len(cfg.Members))
		if w.faults.ReloadLag == 0 {
			w.effective = gen
			w.log("edge generation %d in effect (%d members)", gen, len(cfg.Members))
			if w.faults.ReloadDropsInFlight && len(w.inFlight) > 0 {
				for _, r := range w.inFlight {
					w.fail(r.target, "connection reset by edge reload")
				}
				w.inFlight = nil
			}
		}
	}
	return gen, w.lostResponse(fmt.Sprintf("apply generation %d", gen))
}

// Confirmed waits, in virtual time, until the generation is in effect.
func (e worldEdge) Confirmed(ctx context.Context, gen uint64) error {
	w := e.w
	if _, ok := w.gens[gen]; !ok {
		return fmt.Errorf("sim: generation %d was never applied", gen)
	}
	for w.effective < gen {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.step()
	}
	return nil
}

func (e worldEdge) InFlight(ctx context.Context, ref ops.InstanceRef) (int, error) {
	w := e.w
	n := 0
	for _, r := range w.inFlight {
		if r.target == ref {
			n++
		}
	}
	if n > 0 && w.faults.ExitDuringDrain > 0 && w.rng.Float64() < w.faults.ExitDuringDrain {
		w.log("%s exited while draining", refName(ref))
		w.dropRequests(ref, "connection reset: instance exited during drain")
		delete(w.runtime.instances, ref)
		return 0, nil
	}
	return n, nil
}

// FailureReasons counts failures by reason, sorted for stable output.
func (w *World) FailureReasons() []string {
	counts := map[string]int{}
	for _, f := range w.Failures {
		counts[f.Reason]++
	}
	var out []string
	for r, n := range counts {
		out = append(out, fmt.Sprintf("%d× %s", n, r))
	}
	sort.Strings(out)
	return out
}
