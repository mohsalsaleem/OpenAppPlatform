// Package sim implements the ports in memory for deterministic simulation:
// a manual clock, a runtime, an edge, the journal, the queue and the
// desired-state repository. Everything is driven by the caller's clock, and
// every fake counts the effects it performs so tests can prove nothing
// happened twice.
package sim

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// Clock is a manual clock. Sleep advances it instead of waiting.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock(start time.Time) *Clock { return &Clock{now: start} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Advance(d)
	return nil
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type instance struct {
	id     string
	spec   ops.InstanceSpec
	health model.InstanceHealth
}

// Runtime is an in-memory runtime. Instances become healthy when started
// unless Unhealthy names their component; jobs finish at once with the
// outcome JobFails sets.
type Runtime struct {
	mu        sync.Mutex
	clock     *Clock
	host      model.HostObservation
	instances map[ops.InstanceRef]*instance
	jobs      []model.JobRun
	snapshots map[string]string
	volumes   map[string]bool // "app/component/volume" -> exists
	nextID    int

	// Unhealthy lists components whose instances never become healthy.
	Unhealthy map[string]bool
	// JobFails lists job components whose runs fail.
	JobFails map[string]bool
	// Effect counters.
	Starts    map[ops.InstanceRef]int
	Stops     map[ops.InstanceRef]int
	JobRuns   map[ops.InstanceRef]int
	Snapshots int
	Deletes   int
}

func NewRuntime(clock *Clock, host model.HostObservation) *Runtime {
	return &Runtime{clock: clock, host: host, instances: map[ops.InstanceRef]*instance{},
		snapshots: map[string]string{}, volumes: map[string]bool{},
		Unhealthy: map[string]bool{}, JobFails: map[string]bool{},
		Starts: map[ops.InstanceRef]int{}, Stops: map[ops.InstanceRef]int{}, JobRuns: map[ops.InstanceRef]int{}}
}

func (r *Runtime) Start(ctx context.Context, s ops.InstanceSpec) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if in, ok := r.instances[s.Ref]; ok {
		return in.id, nil
	}
	r.nextID++
	in := &instance{id: fmt.Sprintf("c%04d", r.nextID), spec: s, health: model.HealthHealthy}
	if r.Unhealthy[s.Ref.Component] {
		in.health = model.HealthUnhealthy
	}
	r.instances[s.Ref] = in
	r.Starts[s.Ref]++
	for v := range s.Volumes {
		r.volumes[s.Ref.Application+"/"+s.Ref.Component+"/"+v] = true
	}
	return in.id, nil
}

func (r *Runtime) Stop(ctx context.Context, ref ops.InstanceRef) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.instances[ref]; ok {
		delete(r.instances, ref)
		r.Stops[ref]++
	}
	return nil
}

func (r *Runtime) RunJob(ctx context.Context, s ops.JobSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		if j.Application == s.Ref.Application && j.Component == s.Ref.Component && j.Release == s.Ref.Release {
			return nil // a run for this release exists
		}
	}
	status := model.JobSucceeded
	if r.JobFails[s.Ref.Component] {
		status = model.JobFailed
	}
	r.jobs = append(r.jobs, model.JobRun{Application: s.Ref.Application, Component: s.Ref.Component, Release: s.Ref.Release, Status: status})
	r.JobRuns[s.Ref]++
	return nil
}

func (r *Runtime) Observe(ctx context.Context) (model.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	obs := model.Observation{At: r.clock.Now(), Host: r.host, Jobs: append([]model.JobRun(nil), r.jobs...)}
	for ref, in := range r.instances {
		obs.Instances = append(obs.Instances, model.ObservedInstance{ID: in.id, Application: ref.Application,
			Component: ref.Component, Ordinal: int32(ref.Ordinal), Release: ref.Release, Image: in.spec.Image, Health: in.health})
	}
	sort.Slice(obs.Instances, func(i, j int) bool { return obs.Instances[i].ID < obs.Instances[j].ID })
	return obs, nil
}

func (r *Runtime) Events(ctx context.Context) (<-chan ops.RuntimeEvent, error) {
	ch := make(chan ops.RuntimeEvent)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

func (r *Runtime) SnapshotVolume(ctx context.Context, key, app, component, volume string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.snapshots[key]; ok {
		return id, nil
	}
	r.Snapshots++
	id := fmt.Sprintf("snap-%d", r.Snapshots)
	r.snapshots[key] = id
	return id, nil
}

func (r *Runtime) DeleteVolume(ctx context.Context, app, component, volume string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := app + "/" + component + "/" + volume
	if r.volumes[k] {
		delete(r.volumes, k)
		r.Deletes++
	}
	return nil
}

// Running returns the refs of running instances, sorted.
func (r *Runtime) Running() []ops.InstanceRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ops.InstanceRef
	for ref := range r.instances {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out
}

// Edge is an in-memory edge. Applying a configuration takes effect at once.
type Edge struct {
	mu         sync.Mutex
	cfg        ops.EdgeConfig
	generation uint64
	inFlight   map[ops.InstanceRef]int

	Applies int
}

func NewEdge() *Edge { return &Edge{inFlight: map[ops.InstanceRef]int{}} }

func (e *Edge) Config(ctx context.Context) (ops.EdgeConfig, uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return copyConfig(e.cfg), e.generation, nil
}

func (e *Edge) Apply(ctx context.Context, cfg ops.EdgeConfig) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if sameConfig(cfg, e.cfg) {
		return e.generation, nil
	}
	e.cfg = copyConfig(cfg)
	e.generation++
	e.Applies++
	return e.generation, nil
}

func (e *Edge) Confirmed(ctx context.Context, generation uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if generation > e.generation {
		return fmt.Errorf("sim: generation %d was never applied", generation)
	}
	return nil
}

func (e *Edge) InFlight(ctx context.Context, ref ops.InstanceRef) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inFlight[ref], nil
}

func copyConfig(c ops.EdgeConfig) ops.EdgeConfig {
	return ops.EdgeConfig{Members: append([]ops.RouteMember(nil), c.Members...)}
}

func sameConfig(a, b ops.EdgeConfig) bool {
	if len(a.Members) != len(b.Members) {
		return false
	}
	for i := range a.Members {
		if a.Members[i] != b.Members[i] {
			return false
		}
	}
	return true
}

// Journal is an in-memory OpStore.
type Journal struct {
	mu      sync.Mutex
	entries []ops.Entry
}

func (j *Journal) Append(ctx context.Context, entries ...ops.Entry) (uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, e := range entries {
		e.Revision = uint64(len(j.entries) + 1)
		j.entries = append(j.entries, e)
	}
	return uint64(len(j.entries)), nil
}

func (j *Journal) Since(ctx context.Context, revision uint64, limit int) ([]ops.Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if revision >= uint64(len(j.entries)) {
		return nil, nil
	}
	out := j.entries[revision:]
	if len(out) > limit {
		out = out[:limit]
	}
	return append([]ops.Entry(nil), out...), nil
}

func (j *Journal) Head(ctx context.Context) (uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return uint64(len(j.entries)), nil
}

// Desired is an in-memory DesiredRepo.
type Desired struct {
	mu       sync.Mutex
	current  map[string]any
	byHash   map[string]any
	envelope any
	Commits  int
}

func NewDesired() *Desired { return &Desired{current: map[string]any{}, byHash: map[string]any{}} }

func (d *Desired) Get(ctx context.Context, app string) (any, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.current[app]
	if !ok {
		return nil, "", fmt.Errorf("sim: %w: %q", ops.ErrNoApplication, app)
	}
	h, err := spec.Hash(t)
	return spec.Clone(t), h, err
}

func (d *Desired) ByHash(ctx context.Context, hash string) (any, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.byHash[hash]
	return spec.Clone(t), ok, nil
}

func (d *Desired) Commit(ctx context.Context, app string, tree any, author, message string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	h, err := spec.Hash(tree)
	if err != nil {
		return "", err
	}
	if cur, ok := d.current[app]; ok && spec.Equal(cur, tree) {
		return h, nil
	}
	d.current[app] = spec.Clone(tree)
	d.byHash[h] = spec.Clone(tree)
	d.Commits++
	return h, nil
}

func (d *Desired) Envelope(ctx context.Context) (any, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	h, err := spec.Hash(d.envelope)
	return d.envelope, h, err
}

func (d *Desired) Applications(ctx context.Context) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.current))
	for a := range d.current {
		out = append(out, a)
	}
	sort.Strings(out)
	return out, nil
}

// SetEnvelope stores the envelope tree.
func (d *Desired) SetEnvelope(tree any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.envelope = tree
}

// Secrets is an in-memory secret store.
type Secrets map[string]string

func (s Secrets) Resolve(ctx context.Context, refs map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for variable, ref := range refs {
		v, ok := s[ref]
		if !ok {
			return nil, fmt.Errorf("sim: no secret %q", ref)
		}
		out[variable] = v
	}
	return out, nil
}
