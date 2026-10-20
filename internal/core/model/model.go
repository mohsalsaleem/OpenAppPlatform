// Package model builds the world model (rewrite doc, section 4): one graph of
// host, applications, components, instances and edges, with desired and actual
// state, health, freshness and affordance slots. Build is a pure function of
// desired state, an observation and the time the caller passes in.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Desired is one application's desired state.
type Desired struct {
	App *spec.Application
	// Hash is the content hash of the application's canonical spec.
	Hash string
}

// InstanceHealth is what the runtime reports about one instance.
type InstanceHealth int

const (
	HealthUnknown InstanceHealth = iota
	HealthStarting
	HealthHealthy
	HealthUnhealthy
)

// JobStatus is the state of one job run.
type JobStatus int

const (
	JobRunning JobStatus = iota
	JobSucceeded
	JobFailed
)

// Observation is what the observer last saw on the host. Instances and job
// runs are identified by the labels OAP puts on every resource.
type Observation struct {
	At        time.Time
	Host      HostObservation
	Instances []ObservedInstance
	// Jobs lists job runs, oldest first.
	Jobs []JobRun
}

type HostObservation struct {
	Name              string
	MemoryTotalMiB    int32
	MemoryReservedMiB int32
	CPUMillisTotal    int32
	// MemoryAvailableMiB is nil when the host doesn't report it.
	MemoryAvailableMiB *int32
}

type ObservedInstance struct {
	ID          string
	Application string
	Component   string
	Ordinal     int32
	Release     string
	Image       string
	Health      InstanceHealth
	// Routed is true while the edge sends traffic to this instance.
	Routed bool
}

type JobRun struct {
	Application string
	Component   string
	Release     string
	Status      JobStatus
}

// Options control how observations are judged.
type Options struct {
	// Now is the time the caller considers current. Core never reads the clock.
	Now time.Time
	// MaxAge is how old an observation may be before it counts as stale.
	MaxAge time.Duration
}

// Evidence names used in missing_evidence.
const (
	MissingObservation = "observation"
	MissingFresh       = "fresh observation"
	MissingInstance    = "instance health"
	MissingJobRun      = "job run for the desired release"
	MissingJobResult   = "job result"
)

// Release returns the release identifier of a component spec: a short hash
// of what each instance runs. The instance count is left out, so scaling
// keeps the release and running instances stay current.
func Release(c spec.Component) string {
	c.Instances = 0
	b, err := json.Marshal(c) // field order is fixed and map keys are sorted
	if err != nil {
		panic(err) // a validated spec always marshals
	}
	sum := sha256.Sum256(b)
	return "r-" + hex.EncodeToString(sum[:6])
}

// Build returns the world model at a revision. obs may be nil when the host
// has never been observed.
func Build(revision uint64, desired []Desired, obs *Observation, opts Options) *oapv1.World {
	fresh := freshness(obs, opts)
	w := &oapv1.World{Revision: revision, Host: &oapv1.Host{Freshness: fresh}}
	if obs != nil {
		h := obs.Host
		w.Host.Name = h.Name
		w.Host.Capacity = &oapv1.Capacity{MemoryTotalMib: h.MemoryTotalMiB, MemoryAvailableMib: copyInt32(h.MemoryAvailableMiB),
			MemoryReservedMib: h.MemoryReservedMiB, CpuMillisTotal: h.CPUMillisTotal}
	}
	byApp := map[string]Desired{}
	for _, d := range desired {
		byApp[d.App.Name] = d
	}
	instances := map[string]map[string][]ObservedInstance{}
	jobs := map[string]map[string][]JobRun{}
	names := map[string]bool{}
	for name := range byApp {
		names[name] = true
	}
	if obs != nil {
		for _, in := range obs.Instances {
			if instances[in.Application] == nil {
				instances[in.Application] = map[string][]ObservedInstance{}
			}
			instances[in.Application][in.Component] = append(instances[in.Application][in.Component], in)
			names[in.Application] = true
		}
		for _, j := range obs.Jobs {
			if jobs[j.Application] == nil {
				jobs[j.Application] = map[string][]JobRun{}
			}
			jobs[j.Application][j.Component] = append(jobs[j.Application][j.Component], j)
			names[j.Application] = true
		}
	}
	for _, name := range sortedKeys(names) {
		d, ok := byApp[name]
		b := appBuilder{name: name, obs: obs, fresh: fresh, instances: instances[name], jobs: jobs[name]}
		if ok {
			b.desired = d.App
			b.hash = d.Hash
		}
		w.Applications = append(w.Applications, b.build())
	}
	return w
}

func freshness(obs *Observation, opts Options) *oapv1.Freshness {
	if obs == nil {
		return &oapv1.Freshness{Stale: true}
	}
	return &oapv1.Freshness{ObservedAt: timestamppb.New(obs.At), Stale: opts.Now.Sub(obs.At) > opts.MaxAge}
}

type appBuilder struct {
	name      string
	desired   *spec.Application // nil when only observed
	hash      string
	obs       *Observation
	fresh     *oapv1.Freshness
	instances map[string][]ObservedInstance
	jobs      map[string][]JobRun
}

func (b appBuilder) build() *oapv1.Application {
	app := &oapv1.Application{Name: b.name, SpecHash: b.hash}
	seen := map[string]bool{}
	if b.desired != nil {
		for _, name := range b.desired.Names() {
			seen[name] = true
			app.Components = append(app.Components, b.component(name, b.desired.Components[name]))
		}
		app.Edges = edges(b.desired)
	}
	var orphans []string
	for name := range b.instances {
		if !seen[name] {
			orphans = append(orphans, name)
			seen[name] = true
		}
	}
	for name := range b.jobs {
		if !seen[name] {
			orphans = append(orphans, name)
			seen[name] = true
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		app.Components = append(app.Components, b.orphan(name))
	}
	app.Health = &oapv1.Health{Status: oapv1.HealthStatus_HEALTH_STATUS_HEALTHY, Freshness: b.fresh}
	for _, c := range app.Components {
		if rank(c.Health.Status) > rank(app.Health.Status) {
			app.Health.Status = c.Health.Status
		}
	}
	if len(app.Components) == 0 {
		app.Health.Status = oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN
	}
	return app
}

func (b appBuilder) component(name string, c spec.Component) *oapv1.Component {
	release := Release(c)
	out := &oapv1.Component{
		Name:    name,
		Kind:    kind(c.Kind),
		Desired: b.desiredState(name, c, release),
	}
	observed := b.instances[name]
	out.Instances = instanceNodes(observed, b.fresh)
	if c.Kind == spec.KindJob {
		b.judgeJob(out, release)
		return out
	}
	if len(observed) > 0 {
		out.Actual = actualState(observed)
	}
	out.Converged = !b.fresh.Stale && out.Actual != nil && int(out.Actual.Instances) == c.Instances &&
		allRelease(observed, release) && (len(out.Desired.Routes) == 0 || allRouted(observed))
	out.Health = b.health(observed, c.Instances)
	return out
}

func (b appBuilder) judgeJob(out *oapv1.Component, release string) {
	runs := b.jobs[out.Name]
	out.Health = &oapv1.Health{Status: oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN, Freshness: b.fresh}
	if missing := b.observationGap(); missing != "" {
		out.Health.MissingEvidence = []string{missing}
		return
	}
	var last *JobRun
	for i := range runs {
		if runs[i].Release == release {
			last = &runs[i]
		}
	}
	if last != nil {
		out.Actual = &oapv1.ComponentState{Release: last.Release}
	}
	switch {
	case last == nil:
		out.Health.MissingEvidence = []string{MissingJobRun}
	case last.Status == JobRunning:
		out.Health.MissingEvidence = []string{MissingJobResult}
	case last.Status == JobSucceeded:
		out.Health.Status = oapv1.HealthStatus_HEALTH_STATUS_HEALTHY
		out.Converged = true
	default:
		out.Health.Status = oapv1.HealthStatus_HEALTH_STATUS_UNHEALTHY
	}
}

func (b appBuilder) orphan(name string) *oapv1.Component {
	observed := b.instances[name]
	out := &oapv1.Component{Name: name, Instances: instanceNodes(observed, b.fresh)}
	if len(observed) > 0 {
		out.Actual = actualState(observed)
	}
	if runs := b.jobs[name]; len(runs) > 0 && out.Actual == nil {
		out.Actual = &oapv1.ComponentState{Release: runs[len(runs)-1].Release}
	}
	// Running but not desired: never converged, health judged as for any component.
	out.Health = b.health(observed, len(observed))
	return out
}

// observationGap names the missing evidence when there is no usable observation.
func (b appBuilder) observationGap() string {
	switch {
	case b.obs == nil:
		return MissingObservation
	case b.fresh.Stale:
		return MissingFresh
	}
	return ""
}

func (b appBuilder) health(observed []ObservedInstance, want int) *oapv1.Health {
	h := &oapv1.Health{Freshness: b.fresh}
	if missing := b.observationGap(); missing != "" {
		h.Status = oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN
		h.MissingEvidence = []string{missing}
		return h
	}
	healthy, undecided := 0, 0
	for _, in := range observed {
		switch in.Health {
		case HealthHealthy:
			healthy++
		case HealthStarting, HealthUnknown:
			undecided++
		}
	}
	switch {
	case want > 0 && healthy >= want:
		h.Status = oapv1.HealthStatus_HEALTH_STATUS_HEALTHY
	case healthy > 0:
		h.Status = oapv1.HealthStatus_HEALTH_STATUS_DEGRADED
	case undecided > 0:
		h.Status = oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN
		h.MissingEvidence = []string{MissingInstance}
	default:
		h.Status = oapv1.HealthStatus_HEALTH_STATUS_UNHEALTHY
	}
	return h
}

func (b appBuilder) desiredState(name string, c spec.Component, release string) *oapv1.ComponentState {
	s := &oapv1.ComponentState{Image: c.Image, Release: release, Instances: int32(c.Instances)}
	if r := c.Resources; r != nil {
		s.Resources = &oapv1.Resources{MemoryLimitMib: int32(r.MemoryMiB), CpuMillis: int32(r.CPUMillis)}
	}
	if in := b.desired.Ingress; in != nil {
		for _, path := range sortedKeys(in.Routes) {
			if in.Routes[path].Component == name {
				s.Routes = append(s.Routes, &oapv1.Route{Host: in.Domain, PathPrefix: path, Port: int32(c.Port)})
			}
		}
	}
	for _, v := range sortedKeys(c.Volumes) {
		s.Volumes = append(s.Volumes, &oapv1.Volume{Name: v, MountPath: c.Volumes[v]})
	}
	return s
}

func actualState(observed []ObservedInstance) *oapv1.ComponentState {
	s := &oapv1.ComponentState{Instances: int32(len(observed))}
	s.Image, s.Release = observed[0].Image, observed[0].Release
	for _, in := range observed[1:] {
		if in.Image != s.Image {
			s.Image = "" // mixed: a rollout is in progress or stuck
		}
		if in.Release != s.Release {
			s.Release = ""
		}
	}
	return s
}

func instanceNodes(observed []ObservedInstance, fresh *oapv1.Freshness) []*oapv1.Instance {
	sorted := append([]ObservedInstance(nil), observed...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Ordinal != sorted[j].Ordinal {
			return sorted[i].Ordinal < sorted[j].Ordinal
		}
		return sorted[i].ID < sorted[j].ID
	})
	var out []*oapv1.Instance
	for _, in := range sorted {
		h := &oapv1.Health{Freshness: fresh, Status: instanceStatus(in.Health)}
		if fresh.Stale {
			h.Status = oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN
			h.MissingEvidence = []string{MissingFresh}
		}
		out = append(out, &oapv1.Instance{Id: in.ID, Ordinal: in.Ordinal, Release: in.Release, Health: h, Routed: in.Routed})
	}
	return out
}

func instanceStatus(h InstanceHealth) oapv1.HealthStatus {
	switch h {
	case HealthHealthy:
		return oapv1.HealthStatus_HEALTH_STATUS_HEALTHY
	case HealthUnhealthy:
		return oapv1.HealthStatus_HEALTH_STATUS_UNHEALTHY
	}
	return oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN
}

func edges(app *spec.Application) []*oapv1.Edge {
	var out []*oapv1.Edge
	types := map[string]oapv1.EdgeType{"http": oapv1.EdgeType_EDGE_TYPE_HTTP,
		"database": oapv1.EdgeType_EDGE_TYPE_DATABASE, "queue": oapv1.EdgeType_EDGE_TYPE_QUEUE}
	for _, name := range app.Names() {
		uses := app.Components[name].Uses
		for _, target := range sortedKeys(uses) {
			out = append(out, &oapv1.Edge{From: name, To: target, Type: types[uses[target].Type]})
		}
	}
	if in := app.Ingress; in != nil {
		routed := map[string]bool{}
		for _, r := range in.Routes {
			routed[r.Component] = true
		}
		for _, name := range sortedKeys(routed) {
			out = append(out, &oapv1.Edge{From: "edge", To: name, Type: oapv1.EdgeType_EDGE_TYPE_ROUTE})
		}
	}
	return out
}

func allRelease(observed []ObservedInstance, release string) bool {
	for _, in := range observed {
		if in.Release != release {
			return false
		}
	}
	return true
}

func allRouted(observed []ObservedInstance) bool {
	for _, in := range observed {
		if !in.Routed {
			return false
		}
	}
	return true
}

func kind(k string) oapv1.ComponentKind {
	return map[string]oapv1.ComponentKind{spec.KindWeb: oapv1.ComponentKind_COMPONENT_KIND_WEB,
		spec.KindWorker: oapv1.ComponentKind_COMPONENT_KIND_WORKER, spec.KindJob: oapv1.ComponentKind_COMPONENT_KIND_JOB,
		spec.KindData: oapv1.ComponentKind_COMPONENT_KIND_DATA}[k]
}

// rank orders health from best to worst, for rolling component health up.
func rank(s oapv1.HealthStatus) int {
	switch s {
	case oapv1.HealthStatus_HEALTH_STATUS_HEALTHY:
		return 1
	case oapv1.HealthStatus_HEALTH_STATUS_UNKNOWN:
		return 2
	case oapv1.HealthStatus_HEALTH_STATUS_DEGRADED:
		return 3
	case oapv1.HealthStatus_HEALTH_STATUS_UNHEALTHY:
		return 4
	}
	return 2
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func copyInt32(p *int32) *int32 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
