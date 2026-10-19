package ports

import (
	"context"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// Clock is the only source of time for the engine. Simulations control it.
type Clock interface {
	Now() time.Time
	// Sleep waits for d, or returns ctx's error if it ends first.
	Sleep(ctx context.Context, d time.Duration) error
}

// Runtime runs instances and jobs. Every call is idempotent by instance ref:
// starting an instance that already runs returns it, and stopping one that
// is gone succeeds.
type Runtime interface {
	Start(ctx context.Context, spec ops.InstanceSpec) (id string, err error)
	Stop(ctx context.Context, ref ops.InstanceRef) error
	RunJob(ctx context.Context, spec ops.JobSpec) error
	// Observe returns everything OAP-labelled the runtime knows about now.
	Observe(ctx context.Context) (model.Observation, error)
	// Events streams changes until ctx ends. Events can be missed; Observe
	// is the source of truth.
	Events(ctx context.Context) (<-chan ops.RuntimeEvent, error)
	// SnapshotVolume is idempotent by key: a second call with the same key
	// returns the first snapshot.
	SnapshotVolume(ctx context.Context, key, application, component, volume string) (snapshot string, err error)
	DeleteVolume(ctx context.Context, application, component, volume string) error
}

// Edge routes traffic. Apply replaces the whole configuration and returns its
// generation; Confirmed returns once that generation is in effect. Applying
// the configuration already in place returns its generation unchanged.
type Edge interface {
	// Config returns the configuration last applied and its generation.
	Config(ctx context.Context) (cfg ops.EdgeConfig, generation uint64, err error)
	Apply(ctx context.Context, cfg ops.EdgeConfig) (generation uint64, err error)
	Confirmed(ctx context.Context, generation uint64) error
	// InFlight returns the requests the edge is still serving on an instance.
	InFlight(ctx context.Context, instance ops.InstanceRef) (int, error)
}

// Builder turns a source commit into an image.
type Builder interface {
	Build(ctx context.Context, req ops.BuildRequest) (image string, err error)
}

// DesiredRepo stores desired state: one tree per application, the envelope,
// and every earlier version by content hash.
type DesiredRepo interface {
	Get(ctx context.Context, application string) (tree any, hash string, err error)
	ByHash(ctx context.Context, hash string) (tree any, ok bool, err error)
	Commit(ctx context.Context, application string, tree any, author, message string) (commit string, err error)
	Envelope(ctx context.Context) (tree any, version string, err error)
	// Applications lists the applications that have desired state.
	Applications(ctx context.Context) ([]string, error)
}

// OpStore is the operational journal: append-only entries with monotonic
// revisions.
type OpStore interface {
	// Append stores entries atomically and returns the last revision assigned.
	Append(ctx context.Context, entries ...ops.Entry) (revision uint64, err error)
	// Since returns up to limit entries after revision, oldest first.
	Since(ctx context.Context, revision uint64, limit int) ([]ops.Entry, error)
	Head(ctx context.Context) (revision uint64, err error)
}

// Queue holds durable work. A job is leased to one worker at a time, until a
// given time. Only the current holder of an unexpired lease may renew or
// complete it, so a worker that falls behind is fenced off as soon as its
// lease expires or another worker takes the job. Callers pass the time from
// their Clock.
type Queue interface {
	// Enqueue adds a job unless one with the same key exists.
	Enqueue(ctx context.Context, job ops.Job) (added bool, err error)
	// Lease returns the oldest job that is not leased or whose lease ended by
	// now, leased to worker until the given time; nil if there is none.
	Lease(ctx context.Context, worker string, now, until time.Time) (*ops.Job, error)
	// Heartbeat extends a lease the worker still holds and that hasn't ended.
	Heartbeat(ctx context.Context, key, worker string, now, until time.Time) error
	// Complete removes a job the worker still holds under an unexpired lease.
	Complete(ctx context.Context, key, worker string, now time.Time) error
	// Release returns a job the worker holds to the queue.
	Release(ctx context.Context, key, worker string) error
}

// Secrets resolves secret references to values for the runtime adapter. The
// engine and agents never see the values.
type Secrets interface {
	Resolve(ctx context.Context, refs map[string]string) (map[string]string, error)
}

// FactSource reports facts about a code change, for example from GitHub.
type FactSource interface {
	Facts(ctx context.Context, repository, commit string) (*oapv1.ChangeFacts, error)
}

// Notifier delivers messages to the owner.
type Notifier interface {
	Notify(ctx context.Context, n ops.Notification) error
}
