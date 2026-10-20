// Package engine performs OAP's side effects through ports. The executor runs
// an operation's steps in order and records each step's intent in the
// journal before acting and its outcome after. After a crash it resumes from
// the journal: steps with an outcome are skipped, and a step whose intent was
// recorded without an outcome is observed first, so an effect that already
// happened is never repeated.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

// Journal entry kinds and step states written by the executor.
const (
	KindStep = "step"

	StateStarted   = "started"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
)

// Operation is an applied proposal: its steps and the desired state they
// lead to.
type Operation struct {
	ID          string
	Application string
	Steps       []*oapv1.Step
	// Old is the desired state the plan starts from; New is where it ends.
	Old, New *spec.Application
	NewTree  any
	Author   string
	Message  string
}

// Executor runs operations.
type Executor struct {
	Runtime ports.Runtime
	Edge    ports.Edge
	Journal ports.OpStore
	Desired ports.DesiredRepo
	Clock   ports.Clock

	// ReadyTimeout bounds await-ready when the component sets no readiness
	// timeout; DrainTimeout bounds drain; Poll is the observation interval.
	ReadyTimeout, DrainTimeout, Poll time.Duration

	// crash, if set, is consulted at each crash point; returning true stops
	// the run as if the process died there. Tests use it.
	crash func(step int, point string) bool
}

// Crash points, in the order a step passes them.
const (
	BeforeIntent  = "before-intent"
	AfterIntent   = "after-intent"
	BeforeOutcome = "before-outcome"
)

// ErrCrashed is returned when a test-injected crash stops a run.
var ErrCrashed = errors.New("engine: crashed (injected)")

// StepError reports a step that failed; the operation stops there.
type StepError struct {
	Step   int
	Kind   oapv1.StepKind
	Reason string
}

func (e *StepError) Error() string {
	return fmt.Sprintf("step %d (%s) failed: %s", e.Step, e.Kind, e.Reason)
}

type stepDetail struct {
	Reason     string `json:"reason,omitempty"`
	Generation uint64 `json:"generation,omitempty"`
	Snapshot   string `json:"snapshot,omitempty"`
}

func (x *Executor) defaults() {
	if x.ReadyTimeout == 0 {
		x.ReadyTimeout = 2 * time.Minute
	}
	if x.DrainTimeout == 0 {
		x.DrainTimeout = 30 * time.Second
	}
	if x.Poll == 0 {
		x.Poll = time.Second
	}
}

// Run executes an operation, resuming from whatever the journal recorded.
func (x *Executor) Run(ctx context.Context, op Operation) error {
	x.defaults()
	states, err := x.states(ctx, op.ID)
	if err != nil {
		return err
	}
	for i, s := range op.Steps {
		switch states[i] {
		case StateSucceeded:
			continue
		case StateFailed:
			return &StepError{Step: i, Kind: s.Kind, Reason: "failed earlier"}
		}
		uncertain := states[i] == StateStarted
		if x.crashed(i, BeforeIntent) {
			return ErrCrashed
		}
		if !uncertain {
			if err := x.record(ctx, op.ID, i, StateStarted, stepDetail{}); err != nil {
				return err
			}
		}
		if x.crashed(i, AfterIntent) {
			return ErrCrashed
		}
		if uncertain {
			done, err := x.alreadyDone(ctx, op, s)
			if err != nil {
				return err
			}
			if done {
				if err := x.record(ctx, op.ID, i, StateSucceeded, stepDetail{Reason: "observed after an interrupted attempt"}); err != nil {
					return err
				}
				continue
			}
		}
		detail, err := x.step(ctx, op, i, s)
		var failed *StepError
		if errors.As(err, &failed) {
			if rerr := x.record(ctx, op.ID, i, StateFailed, stepDetail{Reason: failed.Reason}); rerr != nil {
				return rerr
			}
			return err
		}
		if err != nil {
			return err // an infrastructure error: the step stays uncertain
		}
		if x.crashed(i, BeforeOutcome) {
			return ErrCrashed
		}
		if err := x.record(ctx, op.ID, i, StateSucceeded, detail); err != nil {
			return err
		}
	}
	return nil
}

func (x *Executor) crashed(step int, point string) bool {
	return x.crash != nil && x.crash(step, point)
}

func (x *Executor) record(ctx context.Context, opID string, step int, state string, d stepDetail) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = x.Journal.Append(ctx, ops.Entry{At: x.Clock.Now(), Kind: KindStep, Operation: opID, Step: step, State: state, Data: data})
	return err
}

// states returns each step's latest recorded state for an operation.
func (x *Executor) states(ctx context.Context, opID string) (map[int]string, error) {
	out := map[int]string{}
	var after uint64
	for {
		page, err := x.Journal.Since(ctx, after, 500)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		for _, e := range page {
			if e.Kind == KindStep && e.Operation == opID {
				out[e.Step] = e.State
			}
			after = e.Revision
		}
	}
}

// alreadyDone observes whether an interrupted step's effect already happened.
// Steps that only wait or are idempotent at the port report false and run again.
func (x *Executor) alreadyDone(ctx context.Context, op Operation, s *oapv1.Step) (bool, error) {
	r := ref(op, s)
	switch s.Kind {
	case oapv1.StepKind_STEP_KIND_START_INSTANCE, oapv1.StepKind_STEP_KIND_STOP, oapv1.StepKind_STEP_KIND_RUN_JOB:
		obs, err := x.Runtime.Observe(ctx)
		if err != nil {
			return false, err
		}
		running := false
		for _, in := range obs.Instances {
			if in.Application == r.Application && in.Component == r.Component && int(in.Ordinal) == r.Ordinal && in.Release == r.Release {
				running = true
			}
		}
		switch s.Kind {
		case oapv1.StepKind_STEP_KIND_START_INSTANCE:
			return running, nil
		case oapv1.StepKind_STEP_KIND_STOP:
			return !running, nil
		}
		for _, j := range obs.Jobs {
			if j.Application == r.Application && j.Component == r.Component && j.Release == r.Release && j.Status == model.JobSucceeded {
				return true, nil
			}
		}
	}
	return false, nil
}

func fail(i int, s *oapv1.Step, format string, args ...any) error {
	return &StepError{Step: i, Kind: s.Kind, Reason: fmt.Sprintf(format, args...)}
}

// ref returns the instance a step acts on.
func ref(op Operation, s *oapv1.Step) ops.InstanceRef {
	return ops.InstanceRef{Application: op.Application, Component: s.Component, Ordinal: int(s.Ordinal), Release: s.Release}
}

// step performs one step. Every effect is either idempotent at the port or
// checked against observed state first, so running a step again after an
// uncertain crash does nothing twice.
func (x *Executor) step(ctx context.Context, op Operation, i int, s *oapv1.Step) (stepDetail, error) {
	r := ref(op, s)
	switch s.Kind {
	case oapv1.StepKind_STEP_KIND_WRITE_DESIRED:
		_, err := x.Desired.Commit(ctx, op.Application, op.NewTree, op.Author, op.Message)
		return stepDetail{}, err

	case oapv1.StepKind_STEP_KIND_START_INSTANCE:
		spec := ops.InstanceSpecFor(op.New, s.Component, int(s.Ordinal))
		if spec.Ref != r {
			return stepDetail{}, fail(i, s, "planned release %s doesn't match the desired spec's %s", s.Release, spec.Ref.Release)
		}
		_, err := x.Runtime.Start(ctx, spec)
		return stepDetail{}, err

	case oapv1.StepKind_STEP_KIND_AWAIT_READY:
		return stepDetail{}, x.awaitReady(ctx, op, i, s, r)

	case oapv1.StepKind_STEP_KIND_ROUTE_ADD, oapv1.StepKind_STEP_KIND_ROUTE_REMOVE:
		cfg, _, err := x.Edge.Config(ctx)
		if err != nil {
			return stepDetail{}, err
		}
		if s.Kind == oapv1.StepKind_STEP_KIND_ROUTE_ADD {
			// Set the instance's routes to the desired ones: adding a new
			// instance, or moving one to a new domain or path, in one change.
			cfg = ops.WithMembers(ops.WithoutInstance(cfg, r), ops.Members(op.New, r)...)
		} else {
			cfg = ops.WithoutInstance(cfg, r)
		}
		gen, err := x.Edge.Apply(ctx, cfg)
		return stepDetail{Generation: gen}, err

	case oapv1.StepKind_STEP_KIND_EDGE_RELOAD_CONFIRMED:
		_, gen, err := x.Edge.Config(ctx)
		if err != nil {
			return stepDetail{}, err
		}
		return stepDetail{Generation: gen}, x.Edge.Confirmed(ctx, gen)

	case oapv1.StepKind_STEP_KIND_DRAIN:
		deadline := x.Clock.Now().Add(x.DrainTimeout)
		for {
			n, err := x.Edge.InFlight(ctx, r)
			if err != nil {
				return stepDetail{}, err
			}
			if n == 0 {
				return stepDetail{}, nil
			}
			if !x.Clock.Now().Before(deadline) {
				return stepDetail{Reason: fmt.Sprintf("%d requests still in flight after %s", n, x.DrainTimeout)}, nil
			}
			if err := x.Clock.Sleep(ctx, x.Poll); err != nil {
				return stepDetail{}, err
			}
		}

	case oapv1.StepKind_STEP_KIND_STOP:
		cfg, _, err := x.Edge.Config(ctx)
		if err != nil {
			return stepDetail{}, err
		}
		if ops.HasInstance(cfg, r) {
			return stepDetail{}, fail(i, s, "refusing to stop %s/%d: it is still a route member", s.Component, s.Ordinal)
		}
		return stepDetail{}, x.Runtime.Stop(ctx, r)

	case oapv1.StepKind_STEP_KIND_RUN_JOB:
		return stepDetail{}, x.runJob(ctx, op, i, s)

	case oapv1.StepKind_STEP_KIND_SNAPSHOT_VOLUME:
		id, err := x.Runtime.SnapshotVolume(ctx, fmt.Sprintf("%s/%d", op.ID, i), op.Application, s.Component, s.Volume)
		return stepDetail{Snapshot: id}, err

	case oapv1.StepKind_STEP_KIND_DELETE_VOLUME:
		return stepDetail{}, x.Runtime.DeleteVolume(ctx, op.Application, s.Component, s.Volume)
	}
	return stepDetail{}, fail(i, s, "unknown step kind")
}

func (x *Executor) awaitReady(ctx context.Context, op Operation, i int, s *oapv1.Step, r ops.InstanceRef) error {
	timeout := x.ReadyTimeout
	if c, ok := op.New.Components[s.Component]; ok && c.Readiness != nil && c.Readiness.TimeoutSeconds > 0 {
		timeout = time.Duration(c.Readiness.TimeoutSeconds) * time.Second
	}
	deadline := x.Clock.Now().Add(timeout)
	for {
		obs, err := x.Runtime.Observe(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, in := range obs.Instances {
			if in.Application == r.Application && in.Component == r.Component && int(in.Ordinal) == r.Ordinal && in.Release == r.Release {
				found = true
				if in.Health == model.HealthHealthy {
					return nil
				}
			}
		}
		if !found {
			return fail(i, s, "instance %s/%d is not running", s.Component, s.Ordinal)
		}
		if !x.Clock.Now().Before(deadline) {
			return fail(i, s, "instance %s/%d not healthy after %s", s.Component, s.Ordinal, timeout)
		}
		if err := x.Clock.Sleep(ctx, x.Poll); err != nil {
			return err
		}
	}
}

// runJob runs a job once per release. A run that already exists for the
// release, from before a crash, is waited on rather than started again.
func (x *Executor) runJob(ctx context.Context, op Operation, i int, s *oapv1.Step) error {
	js := ops.JobSpecFor(op.New, s.Component)
	if err := x.Runtime.RunJob(ctx, js); err != nil {
		return err
	}
	deadline := x.Clock.Now().Add(js.Timeout)
	for {
		obs, err := x.Runtime.Observe(ctx)
		if err != nil {
			return err
		}
		var last *model.JobRun
		for k := range obs.Jobs {
			j := obs.Jobs[k]
			if j.Application == op.Application && j.Component == s.Component && j.Release == s.Release {
				last = &j
			}
		}
		switch {
		case last != nil && last.Status == model.JobSucceeded:
			return nil
		case last != nil && last.Status == model.JobFailed:
			return fail(i, s, "job %s failed", s.Component)
		case !x.Clock.Now().Before(deadline):
			return fail(i, s, "job %s did not finish within %s", s.Component, js.Timeout)
		}
		if err := x.Clock.Sleep(ctx, x.Poll); err != nil {
			return err
		}
	}
}
