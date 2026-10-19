// Package ops holds the plain data the engine exchanges with the outside world
// through ports: what to start or run, edge configuration, journal entries and
// queued jobs. It has no behavior beyond small pure helpers.
package ops

import (
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
)

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
}
