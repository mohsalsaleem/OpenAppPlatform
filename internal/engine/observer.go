package engine

import (
	"context"
	"sync"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

// Observer keeps the latest picture of the runtime. Events update it as they
// arrive; a full Observe every Interval replaces it, so events that were
// missed are healed within one interval. The snapshot's time is the last full
// observation, so freshness never overstates what was actually seen.
type Observer struct {
	Runtime  ports.Runtime
	Clock    ports.Clock
	Interval time.Duration

	mu         sync.Mutex
	snap       model.Observation
	lastResync time.Time
	events     <-chan ops.RuntimeEvent
	resyncs    int
}

// Start subscribes to events and takes the first full observation.
func (o *Observer) Start(ctx context.Context) error {
	if o.Interval == 0 {
		o.Interval = 30 * time.Second
	}
	events, err := o.Runtime.Events(ctx)
	if err != nil {
		return err
	}
	o.events = events
	return o.Resync(ctx)
}

// Resync replaces the snapshot with a full observation.
func (o *Observer) Resync(ctx context.Context) error {
	obs, err := o.Runtime.Observe(ctx)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.snap, o.lastResync = obs, obs.At
	o.resyncs++
	return nil
}

// Poll applies pending events and resyncs if an interval has passed since
// the last full observation. A finished job also triggers a resync, since
// events don't carry job results.
func (o *Observer) Poll(ctx context.Context) error {
	resync := !o.Clock.Now().Before(o.lastResync.Add(o.Interval))
	for {
		select {
		case e, ok := <-o.events:
			if !ok {
				return o.maybeResync(ctx, true)
			}
			if e.Kind == ops.EventJobFinished {
				resync = true
			}
			o.apply(e)
			continue
		default:
		}
		break
	}
	return o.maybeResync(ctx, resync)
}

func (o *Observer) maybeResync(ctx context.Context, due bool) error {
	if !due {
		return nil
	}
	return o.Resync(ctx)
}

// Run polls until ctx ends, sleeping between polls.
func (o *Observer) Run(ctx context.Context, every time.Duration) error {
	for {
		if err := o.Poll(ctx); err != nil {
			return err
		}
		if err := o.Clock.Sleep(ctx, every); err != nil {
			return err
		}
	}
}

func (o *Observer) apply(e ops.RuntimeEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := e.Instance
	idx := -1
	for i, in := range o.snap.Instances {
		if in.Application == r.Application && in.Component == r.Component && int(in.Ordinal) == r.Ordinal && in.Release == r.Release {
			idx = i
		}
	}
	switch e.Kind {
	case ops.EventStarted:
		in := model.ObservedInstance{ID: e.ID, Application: r.Application, Component: r.Component,
			Ordinal: int32(r.Ordinal), Release: r.Release, Health: e.Health}
		if idx >= 0 {
			in.Image = o.snap.Instances[idx].Image
			o.snap.Instances[idx] = in
		} else {
			o.snap.Instances = append(o.snap.Instances, in)
		}
	case ops.EventStopped:
		if idx >= 0 {
			o.snap.Instances = append(o.snap.Instances[:idx], o.snap.Instances[idx+1:]...)
		}
	case ops.EventHealth:
		if idx >= 0 {
			o.snap.Instances[idx].Health = e.Health
		}
	}
}

// Snapshot returns a copy of the current observation.
func (o *Observer) Snapshot() model.Observation {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.snap
	s.Instances = append([]model.ObservedInstance(nil), o.snap.Instances...)
	s.Jobs = append([]model.JobRun(nil), o.snap.Jobs...)
	return s
}

// Resyncs reports how many full observations were taken.
func (o *Observer) Resyncs() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.resyncs
}
