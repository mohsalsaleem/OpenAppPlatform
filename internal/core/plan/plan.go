// Package plan turns a desired-state change into typed steps (rewrite doc,
// section 12): what to start, route, drain, stop, run and snapshot, in an
// order that keeps traffic safe. It also computes blast radius, reversibility
// and the undo intent. Plans assume the runtime matches the old desired state;
// the engine observes before acting and handles any difference.
package plan

import (
	"fmt"
	"sort"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

// Input is a compiled change to one application.
type Input struct {
	Application string
	// Old and OldTree are nil when the application is new.
	Old     *spec.Application
	OldTree any
	OldHash string
	New     *spec.Application
	NewTree any
	// Rollback means jobs are not re-run.
	Rollback bool
	// DeleteVolumes lists volumes of removed components to delete.
	DeleteVolumes []string
	// Intent is the intent that produced the change, used to shape the undo.
	Intent *oapv1.Intent
}

// Plan is the planner's output.
type Plan struct {
	Steps       []*oapv1.Step
	BlastRadius *oapv1.BlastRadius
	// Undo is nil when the plan is irreversible.
	Undo *oapv1.Intent
	// Irreversible lists why the plan can't be undone; empty when it can.
	Irreversible []string
}

// Downtime values for blast radius.
const (
	DowntimeNone    = "none-expected"
	DowntimePartial = "partial"
)

type builder struct {
	in       Input
	steps    []*oapv1.Step
	touched  map[string]bool
	downtime bool
}

// add appends a step that runs after the given steps and returns its index.
func (b *builder) add(s *oapv1.Step, after ...int) int {
	seen := map[int]bool{}
	for _, a := range after {
		if a >= 0 && !seen[a] {
			s.After = append(s.After, int32(a))
			seen[a] = true
		}
	}
	sort.Slice(s.After, func(i, j int) bool { return s.After[i] < s.After[j] })
	b.steps = append(b.steps, s)
	if s.Component != "" {
		b.touched[s.Component] = true
	}
	return len(b.steps) - 1
}

func step(kind oapv1.StepKind, component string, ordinal int, release, image string) *oapv1.Step {
	return &oapv1.Step{Kind: kind, Component: component, Ordinal: int32(ordinal), Release: release, Image: image}
}

// Build plans a change.
func Build(in Input) *Plan {
	b := &builder{in: in, touched: map[string]bool{}}
	write := b.add(&oapv1.Step{Kind: oapv1.StepKind_STEP_KIND_WRITE_DESIRED})
	ready := map[string]int{} // component -> step after which it is ready in this plan
	for _, name := range in.New.Order() {
		var deps []int
		deps = append(deps, write)
		for _, dep := range in.New.Dependencies(name) {
			if i, ok := ready[dep]; ok {
				deps = append(deps, i)
			}
		}
		if last := b.component(name, deps); last >= 0 {
			ready[name] = last
		}
	}
	b.removals(write)
	p := &Plan{Steps: b.steps}
	p.BlastRadius = &oapv1.BlastRadius{Components: sortedKeys(b.touched), Downtime: DowntimeNone}
	if b.downtime {
		p.BlastRadius.Downtime = DowntimePartial
	}
	p.Irreversible = b.irreversible()
	if len(p.Irreversible) == 0 {
		p.Undo = undo(in)
	}
	return p
}

func (b *builder) old(name string) (spec.Component, bool) {
	if b.in.Old == nil {
		return spec.Component{}, false
	}
	c, ok := b.in.Old.Components[name]
	return c, ok
}

func routed(app *spec.Application, name string) bool {
	if app == nil || app.Ingress == nil {
		return false
	}
	for _, r := range app.Ingress.Routes {
		if r.Component == name {
			return true
		}
	}
	return false
}

// stopFirst reports whether old instances must stop before new ones start:
// volumes and host ports can't be shared by two instances.
func stopFirst(c spec.Component) bool {
	return len(c.Volumes) > 0 || c.HostPort != 0 || c.Kind == spec.KindData
}

// component plans one component of the new spec and returns the step after
// which it is ready, or -1 if the plan doesn't touch it.
func (b *builder) component(name string, deps []int) int {
	nc := b.in.New.Components[name]
	oc, existed := b.old(name)
	newRel := model.Release(nc)
	oldRel := model.Release(oc)
	if nc.Kind == spec.KindJob {
		if (existed && oldRel == newRel) || b.in.Rollback {
			return -1
		}
		return b.add(step(oapv1.StepKind_STEP_KIND_RUN_JOB, name, 1, newRel, nc.Image), deps...)
	}
	isRouted, wasRouted := routed(b.in.New, name), routed(b.in.Old, name)
	n, o := nc.Instances, 0
	if existed {
		o = oc.Instances
	}
	last := -1
	prev := deps
	chain := func(i int) []int { return append(append([]int{}, deps...), i) }
	switch {
	case !existed || oldRel != newRel:
		replaceFirst := existed && stopFirst(nc)
		for i := 1; i <= max(n, o); i++ {
			switch {
			case i <= o && i <= n && replaceFirst:
				b.downtime = true
				end := b.removeInstance(name, i, oc, wasRouted, prev)
				last = b.startInstance(name, i, nc, newRel, isRouted, chain(end))
			case i <= o && i <= n:
				last = b.replaceInstance(name, i, nc, newRel, oldRel, isRouted, wasRouted, prev)
			case i <= n:
				last = b.startInstance(name, i, nc, newRel, isRouted, prev)
			default:
				last = b.removeInstance(name, i, oc, wasRouted, prev)
			}
			prev = chain(last)
		}
	default:
		for i := o + 1; i <= n; i++ {
			last = b.startInstance(name, i, nc, newRel, isRouted, prev)
			prev = chain(last)
		}
		for i := o; i > n; i-- {
			last = b.removeInstance(name, i, oc, wasRouted, prev)
			prev = chain(last)
		}
		if isRouted != wasRouted {
			kind := oapv1.StepKind_STEP_KIND_ROUTE_ADD
			if wasRouted {
				kind = oapv1.StepKind_STEP_KIND_ROUTE_REMOVE
			}
			var routes []int
			for i := 1; i <= min(n, o); i++ {
				routes = append(routes, b.add(step(kind, name, i, newRel, ""), prev...))
			}
			if len(routes) > 0 {
				last = b.add(step(oapv1.StepKind_STEP_KIND_EDGE_RELOAD_CONFIRMED, name, 0, "", ""), routes...)
			}
		}
	}
	return last
}

// startInstance starts an instance, waits for it and routes it if needed.
func (b *builder) startInstance(name string, i int, c spec.Component, release string, isRouted bool, after []int) int {
	s := b.add(step(oapv1.StepKind_STEP_KIND_START_INSTANCE, name, i, release, c.Image), after...)
	last := b.add(step(oapv1.StepKind_STEP_KIND_AWAIT_READY, name, i, release, ""), s)
	if isRouted {
		r := b.add(step(oapv1.StepKind_STEP_KIND_ROUTE_ADD, name, i, release, ""), last)
		last = b.add(step(oapv1.StepKind_STEP_KIND_EDGE_RELOAD_CONFIRMED, name, i, release, ""), r)
	}
	return last
}

// replaceInstance starts the new instance before the old one leaves. The new
// instance joins the route before the old one is removed, one confirmed
// reload covers both, and only then is the old instance drained and stopped.
func (b *builder) replaceInstance(name string, i int, c spec.Component, newRel, oldRel string, isRouted, wasRouted bool, after []int) int {
	s := b.add(step(oapv1.StepKind_STEP_KIND_START_INSTANCE, name, i, newRel, c.Image), after...)
	last := b.add(step(oapv1.StepKind_STEP_KIND_AWAIT_READY, name, i, newRel, ""), s)
	var routeChanges []int
	if isRouted {
		routeChanges = append(routeChanges, b.add(step(oapv1.StepKind_STEP_KIND_ROUTE_ADD, name, i, newRel, ""), last))
	}
	if wasRouted {
		routeChanges = append(routeChanges, b.add(step(oapv1.StepKind_STEP_KIND_ROUTE_REMOVE, name, i, oldRel, ""), last))
	}
	if len(routeChanges) > 0 {
		last = b.add(step(oapv1.StepKind_STEP_KIND_EDGE_RELOAD_CONFIRMED, name, i, newRel, ""), routeChanges...)
	}
	if wasRouted {
		last = b.add(step(oapv1.StepKind_STEP_KIND_DRAIN, name, i, oldRel, ""), last)
	}
	return b.add(step(oapv1.StepKind_STEP_KIND_STOP, name, i, oldRel, ""), last)
}

// removeInstance takes an instance out of service: snapshot its volumes, then
// route-remove, confirmed reload and drain if it was routed, then stop.
func (b *builder) removeInstance(name string, i int, c spec.Component, wasRouted bool, after []int) int {
	release := model.Release(c)
	last := -1
	prev := after
	for _, v := range sortedKeys(c.Volumes) {
		last = b.add(&oapv1.Step{Kind: oapv1.StepKind_STEP_KIND_SNAPSHOT_VOLUME, Component: name, Volume: v}, prev...)
		prev = []int{last}
	}
	if wasRouted {
		last = b.add(step(oapv1.StepKind_STEP_KIND_ROUTE_REMOVE, name, i, release, ""), prev...)
		last = b.add(step(oapv1.StepKind_STEP_KIND_EDGE_RELOAD_CONFIRMED, name, i, release, ""), last)
		last = b.add(step(oapv1.StepKind_STEP_KIND_DRAIN, name, i, release, ""), last)
		prev = []int{last}
	}
	return b.add(step(oapv1.StepKind_STEP_KIND_STOP, name, i, release, ""), prev...)
}

// removals stops components that are no longer desired, dependents first, and
// deletes volumes only when asked.
func (b *builder) removals(write int) {
	if b.in.Old == nil {
		return
	}
	order := b.in.Old.Order()
	deleting := map[string]bool{}
	for _, v := range b.in.DeleteVolumes {
		deleting[v] = true
	}
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		if _, kept := b.in.New.Components[name]; kept {
			continue
		}
		oc := b.in.Old.Components[name]
		b.touched[name] = true
		prev := []int{write}
		if oc.Kind != spec.KindJob {
			for ordinal := oc.Instances; ordinal >= 1; ordinal-- {
				prev = []int{b.removeInstance(name, ordinal, oc, routed(b.in.Old, name), prev)}
			}
		}
		for _, v := range sortedKeys(oc.Volumes) {
			if deleting[v] {
				b.add(&oapv1.Step{Kind: oapv1.StepKind_STEP_KIND_DELETE_VOLUME, Component: name, Volume: v}, prev...)
			}
		}
	}
}

func (b *builder) irreversible() []string {
	var out []string
	for _, s := range b.steps {
		switch s.Kind {
		case oapv1.StepKind_STEP_KIND_DELETE_VOLUME:
			out = append(out, fmt.Sprintf("deletes volume %s of %s", s.Volume, s.Component))
		case oapv1.StepKind_STEP_KIND_RUN_JOB:
			if b.in.New.Components[s.Component].Destructive {
				out = append(out, fmt.Sprintf("runs %s, a destructive job", s.Component))
			}
		}
	}
	return out
}

// undo returns an intent that restores the old desired state. Image-only
// changes undo as a rollback, so jobs don't re-run.
func undo(in Input) *oapv1.Intent {
	if in.Old == nil {
		return nil // a new application is undone by deleting it, which is not an intent
	}
	u := &oapv1.Intent{Application: in.Application, Because: "undo"}
	switch c := in.Intent.GetChange().(type) {
	case *oapv1.Intent_Scale:
		was := in.Old.Components[c.Scale.Component].Instances
		u.Change = &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: c.Scale.Component, Instances: int32(was)}}
		return u
	case *oapv1.Intent_Deploy, *oapv1.Intent_Rollback:
		u.Change = &oapv1.Intent_Rollback{Rollback: &oapv1.Rollback{ToRelease: in.OldHash}}
		return u
	case *oapv1.Intent_AddComponent:
		u.Change = &oapv1.Intent_RemoveComponent{RemoveComponent: &oapv1.RemoveComponent{Component: c.AddComponent.Name}}
		return u
	}
	from, _ := in.NewTree.(map[string]any)
	to, _ := in.OldTree.(map[string]any)
	patch, err := structpb.NewStruct(toProtoJSON(spec.MergePatch(from, to)).(map[string]any))
	if err != nil {
		return nil
	}
	u.Change = &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: patch}}
	return u
}

// toProtoJSON converts tree values to the types structpb accepts.
func toProtoJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = toProtoJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = toProtoJSON(e)
		}
		return out
	case int64:
		return float64(t)
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
