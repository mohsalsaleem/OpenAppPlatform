// Package ops holds the plain data the engine exchanges with the outside world
// through ports: what to start or run, edge configuration, journal entries and
// queued jobs. It has no behavior beyond small pure helpers.
package ops

import (
	"errors"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
)

// ErrNoApplication is what a DesiredRepo returns for an application with
// no desired state.
var ErrNoApplication = errors.New("no such application")

// InstanceRef identifies an instance by the labels OAP puts on it. Two
// instances with the same ref are the same instance.
type InstanceRef struct {
	Application string
	Component   string
	Ordinal     int
	Release     string
}

// InstanceSpec is everything the runtime needs to start one instance.
type InstanceSpec struct {
	Ref   InstanceRef
	Image string
	Env   map[string]string
	// SecretRefs maps variables to secret references; the runtime adapter
	// resolves them, so values never pass through the engine.
	SecretRefs map[string]string
	Port       int
	HostPort   int
	Volumes    map[string]string // volume name -> mount path
	MemoryMiB  int
	CPUMillis  int
	Health     *HealthCheck
	Security   Security
}

type HealthCheck struct {
	HTTPPath        string // empty: the image's own check
	IntervalSeconds int
	TimeoutSeconds  int
	Retries         int
	StartPeriod     time.Duration
}

// JobSpec is everything the runtime needs to run a job to completion.
type JobSpec struct {
	Ref        InstanceRef
	Image      string
	Env        map[string]string
	SecretRefs map[string]string
	Timeout    time.Duration
	MemoryMiB  int
	CPUMillis  int
	Security   Security
}

// Security is how far a container may depart from OAP's default hardening
// (ADR 0003): every capability dropped, no privilege escalation, a
// read-only root with a writable /tmp, and a process limit. The zero value
// is the default.
type Security struct {
	// User runs the container as a numeric uid[:gid]; empty keeps the image's
	// user, which must then not be root unless RunAsRoot is set.
	User                     string
	RunAsRoot                bool
	AllowPrivilegeEscalation bool
	// Capabilities to keep, from Docker's default set.
	Capabilities []string
	WritableRoot bool
	// Pids limits processes and threads; 0 means the runtime's default.
	Pids int
}

// RouteMember is one instance receiving traffic for one route.
type RouteMember struct {
	Instance    InstanceRef
	Host        string
	PathPrefix  string
	StripPrefix bool
	Port        int
}

// EdgeConfig is the edge's complete desired routing configuration.
type EdgeConfig struct {
	Members []RouteMember
}

// RuntimeEvent reports a change the runtime noticed.
type RuntimeEvent struct {
	At       time.Time
	Instance InstanceRef
	ID       string
	Kind     RuntimeEventKind
	Health   model.InstanceHealth
}

type RuntimeEventKind int

const (
	EventStarted RuntimeEventKind = iota + 1
	EventStopped
	EventHealth
	EventJobFinished
)

// Entry is one record in the operational journal. Revision is assigned by the
// store when the entry is appended; Data holds the serialized detail.
type Entry struct {
	Revision  uint64
	At        time.Time
	Kind      string
	Operation string
	Step      int
	State     string
	Data      []byte
}

// Job is a unit of queued work. Key makes enqueueing idempotent.
type Job struct {
	Key       string
	Kind      string
	Payload   []byte
	Attempts  int
	LeasedBy  string
	LeaseEnds time.Time
}

// Notification is a message for the owner, for example an ask or a digest.
type Notification struct {
	Kind    string
	Subject string
	Body    string
}

// BuildRequest asks a builder for an image from a source commit.
type BuildRequest struct {
	Application string
	Component   string
	Repository  string
	Commit      string
	// Context is the build context's directory in the repository, and
	// Dockerfile the file in it.
	Context, Dockerfile string
	// Image is the name to give the result, without a tag.
	Image string
	// MemoryMiB is the memory the build may use.
	MemoryMiB int
	// Builder names the builder extension; empty for the only one.
	Builder string
	// Started, when set, is called when the build leaves the queue and
	// starts.
	Started func()
}

// ErrNoFactSource is a fact source with nothing installed to ask.
var ErrNoFactSource = errors.New("no fact source is installed")

// FactsRequest asks a fact source about a commit.
type FactsRequest struct {
	Repository, Commit string
	PullRequest        int
}

// BuildResult is what a build produced.
type BuildResult struct {
	Image   string // the full reference, with its tag
	Commit  string // the full commit built
	LogTail string
}

// Refusal is a runtime's permanent refusal to run something as specified,
// for example an image that runs as root without the spec allowing it.
// Retrying can't help, so the step fails with Reason.
type Refusal struct {
	Why    string
	Reason string
}

func (r *Refusal) Error() string { return r.Why + ": " + r.Reason }
