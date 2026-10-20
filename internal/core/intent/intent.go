// Package intent compiles an agent's intent into a new desired state for one
// application. It has no side effects: the result is a validated tree, its
// hash and the changes from the current desired state. The planner (R1.5)
// turns those changes into steps.
package intent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// Kind names an intent, as used in envelope rules and affordances.
type Kind string

const (
	KindScale           Kind = "scale"
	KindDeploy          Kind = "deploy"
	KindRollback        Kind = "rollback"
	KindConfigure       Kind = "configure"
	KindAddComponent    Kind = "add-component"
	KindRemoveComponent Kind = "remove-component"
	KindSpecPatch       Kind = "spec-patch"
)

// Stable why codes for intent errors.
const (
	WhyMissingChange    = "intent.missing_change"
	WhyWrongApplication = "intent.wrong_application"
	WhyUnknownComponent = "intent.unknown_component"
	WhyComponentExists  = "intent.component_exists"
	WhyComponentInUse   = "intent.component_in_use"
	WhyUnknownRelease   = "intent.unknown_release"
	WhyNoChange         = "intent.no_change"
	WhyImmutableField   = "intent.immutable_field"
	WhyIncomplete       = "intent.incomplete"
	WhyBadValue         = "intent.bad_value"
	WhyInvalidResult    = "intent.invalid_result"
)

// Error explains why an intent can't be compiled, with a remedy when there is one.
type Error struct {
	Why      string
	Message  string
	Remedy   *oapv1.Remedy
	Problems []spec.Problem // validation problems in the resulting spec, if any
}

func (e *Error) Error() string { return e.Why + ": " + e.Message }

// Detail returns the error as the API's error detail.
func (e *Error) Detail() *oapv1.ErrorDetail {
	return &oapv1.ErrorDetail{Why: e.Why, Message: e.Message, Remedy: e.Remedy}
}

// fail reports a problem with the intent's input; the remedy names the field.
func fail(why, path, format string, args ...any) *Error {
	msg := fmt.Sprintf(format, args...)
	return &Error{Why: why, Message: msg, Remedy: fix(spec.Problem{Path: path, Why: why, Message: msg})}
}

func fix(problems ...spec.Problem) *oapv1.Remedy {
	f := &oapv1.InputFix{}
	for _, p := range problems {
		f.Fields = append(f.Fields, &oapv1.FieldProblem{Path: p.Path, Why: p.Why, Message: p.Message})
	}
	return &oapv1.Remedy{Action: &oapv1.Remedy_Fix{Fix: f}}
}

var refresh = &oapv1.Remedy{Action: &oapv1.Remedy_Refresh{Refresh: true}}

// Current is the application's desired state the intent applies to.
type Current struct {
	Tree any // the desired-state tree
	// History looks up an earlier desired-state tree by its spec hash, for rollback.
	History func(hash string) (any, bool)
}

// Result is a compiled intent.
type Result struct {
	Kind    Kind
	Tree    any
	App     *spec.Application
	Hash    string
	Changes []spec.Change
	// Rollback is set for rollbacks: jobs must not re-run.
	Rollback bool
	// DeleteVolumes lists volumes the change deletes. Irreversible.
	DeleteVolumes []string
}

// KindOf returns the kind of an intent, or "" if it names no change.
func KindOf(in *oapv1.Intent) Kind {
	switch in.GetChange().(type) {
	case *oapv1.Intent_Scale:
		return KindScale
	case *oapv1.Intent_Deploy:
		return KindDeploy
	case *oapv1.Intent_Rollback:
		return KindRollback
	case *oapv1.Intent_Configure:
		return KindConfigure
	case *oapv1.Intent_AddComponent:
		return KindAddComponent
	case *oapv1.Intent_RemoveComponent:
		return KindRemoveComponent
	case *oapv1.Intent_SpecPatch:
		return KindSpecPatch
	case *oapv1.Intent_InstallExtension:
		return KindInstallExtension
	case *oapv1.Intent_RemoveExtension:
		return KindRemoveExtension
	case *oapv1.Intent_Build:
		return KindBuild
	}
	return ""
}

// Compile applies an intent to the current desired state.
func Compile(cur Current, in *oapv1.Intent) (*Result, *Error) {
	tree, ok := spec.Clone(cur.Tree).(map[string]any)
	if !ok {
		return nil, fail(WhyBadValue, "", "current desired state is not an application")
	}
	name, _ := tree["name"].(string)
	if in.GetApplication() != name {
		return nil, &Error{Why: WhyWrongApplication, Remedy: refresh,
			Message: fmt.Sprintf("intent is for %q, but this is %q", in.GetApplication(), name)}
	}
	components, _ := tree["components"].(map[string]any)
	res := &Result{Kind: KindOf(in)}
	var err *Error
	switch c := in.GetChange().(type) {
	case *oapv1.Intent_Scale:
		err = scale(components, c.Scale)
	case *oapv1.Intent_Deploy:
		err = deploy(components, c.Deploy)
	case *oapv1.Intent_Rollback:
		res.Rollback = true
		err = rollback(components, c.Rollback, cur.History)
	case *oapv1.Intent_Configure:
		err = configure(components, c.Configure)
	case *oapv1.Intent_AddComponent:
		err = addComponent(components, c.AddComponent)
	case *oapv1.Intent_RemoveComponent:
		res.DeleteVolumes, err = removeComponent(tree, components, c.RemoveComponent)
	case *oapv1.Intent_SpecPatch:
		tree, err = specPatch(tree, c.SpecPatch)
	default:
		return nil, fail(WhyMissingChange, "change", "name one change: scale, deploy, rollback, configure, add_component, remove_component or spec_patch")
	}
	if err != nil {
		return nil, err
	}
	app, problems := spec.ParseApplication(tree)
	if len(problems) > 0 {
		return nil, invalid(in, problems)
	}
	// Compare effective state, with defaults applied: writing a default
	// explicitly (instances: 1) is not a change.
	if before, _ := spec.ParseApplication(cur.Tree); before != nil && sameApp(before, app) {
		return nil, &Error{Why: WhyNoChange, Message: "the desired state already matches this intent", Remedy: refresh}
	}
	res.Changes = spec.Diff(cur.Tree, tree)
	hash, herr := spec.Hash(tree)
	if herr != nil {
		return nil, fail(WhyBadValue, "", "%v", herr)
	}
	res.Tree, res.App, res.Hash = tree, app, hash
	return res, nil
}

// invalid reports validation problems in the resulting spec, suggesting a
// corrected intent where one is obvious.
func invalid(in *oapv1.Intent, problems []spec.Problem) *Error {
	parts := make([]string, len(problems))
	for i, p := range problems {
		parts[i] = p.String()
	}
	e := &Error{Why: WhyInvalidResult, Problems: problems, Remedy: fix(problems...),
		Message: "the change would make the spec invalid: " + strings.Join(parts, "; ")}
	if s := in.GetScale(); s != nil {
		for _, p := range problems {
			if p.Why == spec.WhyRange && strings.HasSuffix(p.Path, ".instances") {
				fixed := proto.Clone(in).(*oapv1.Intent)
				fixed.GetScale().Instances = clamp(s.Instances, 1, 4)
				e.Remedy = &oapv1.Remedy{Action: &oapv1.Remedy_Intent{Intent: fixed}}
			}
		}
	}
	return e
}

func clamp(v, lo, hi int32) int32 {
	return max(lo, min(hi, v))
}

func component(components map[string]any, name string) (map[string]any, *Error) {
	c, ok := components[name].(map[string]any)
	if !ok {
		return nil, &Error{Why: WhyUnknownComponent, Remedy: refresh,
			Message: fmt.Sprintf("no component %q; components are %s", name, strings.Join(sortedKeys(components), ", "))}
	}
	return c, nil
}

func scale(components map[string]any, s *oapv1.Scale) *Error {
	c, err := component(components, s.Component)
	if err != nil {
		return err
	}
	c["instances"] = int64(s.Instances)
	return nil
}

func deploy(components map[string]any, d *oapv1.Deploy) *Error {
	if len(d.Images) == 0 {
		return fail(WhyIncomplete, "change.deploy.images", "deploy needs at least one component image")
	}
	for _, name := range sortedKeys(d.Images) {
		c, err := component(components, name)
		if err != nil {
			return err
		}
		c["image"] = d.Images[name]
	}
	return nil
}

func rollback(components map[string]any, r *oapv1.Rollback, history func(string) (any, bool)) *Error {
	var earlier any
	ok := false
	if history != nil && r.ToRelease != "" {
		earlier, ok = history(r.ToRelease)
	}
	if !ok {
		return &Error{Why: WhyUnknownRelease, Remedy: refresh,
			Message: fmt.Sprintf("no earlier desired state with hash %q", r.ToRelease)}
	}
	old, _ := earlier.(map[string]any)["components"].(map[string]any)
	for _, name := range sortedKeys(components) {
		was, ok := old[name].(map[string]any)
		if !ok {
			continue // added since; keeps its current image
		}
		if image, ok := was["image"].(string); ok {
			components[name].(map[string]any)["image"] = image
		}
	}
	return nil
}

func configure(components map[string]any, cfg *oapv1.Configure) *Error {
	c, err := component(components, cfg.Component)
	if err != nil {
		return err
	}
	env, _ := c["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	for _, k := range sortedKeys(cfg.SetEnv) {
		env[k] = cfg.SetEnv[k]
	}
	for _, k := range cfg.UnsetEnv {
		if _, ok := env[k]; !ok {
			return fail(WhyBadValue, "change.configure.unset_env", "%s is not set on %s", k, cfg.Component)
		}
		delete(env, k)
	}
	if len(env) == 0 {
		delete(c, "env")
	} else {
		c["env"] = env
	}
	if cfg.MemoryLimitMib != nil || cfg.CpuMillis != nil {
		res, _ := c["resources"].(map[string]any)
		if res == nil {
			if cfg.MemoryLimitMib == nil || cfg.CpuMillis == nil {
				return fail(WhyIncomplete, "change.configure", "%s has no resources yet; set both memory_limit_mib and cpu_millis", cfg.Component)
			}
			res = map[string]any{}
		}
		if cfg.MemoryLimitMib != nil {
			res["memory_mib"] = int64(*cfg.MemoryLimitMib)
		}
		if cfg.CpuMillis != nil {
			res["cpu_millis"] = int64(*cfg.CpuMillis)
		}
		c["resources"] = res
	}
	return nil
}

func addComponent(components map[string]any, a *oapv1.AddComponent) *Error {
	if a.Name == "" || a.Spec == nil {
		return fail(WhyIncomplete, "change.add_component", "add_component needs a name and a spec")
	}
	if _, exists := components[a.Name]; exists {
		return &Error{Why: WhyComponentExists, Remedy: refresh, Message: fmt.Sprintf("%s already exists", a.Name)}
	}
	v, err := fromStruct(structpb.NewStructValue(a.Spec), "spec")
	if err != nil {
		return err
	}
	components[a.Name] = v
	return nil
}

func removeComponent(tree, components map[string]any, r *oapv1.RemoveComponent) ([]string, *Error) {
	c, err := component(components, r.Component)
	if err != nil {
		return nil, err
	}
	var refs []spec.Problem
	ref := func(path string) {
		refs = append(refs, spec.Problem{Path: path, Why: WhyComponentInUse, Message: "still refers to " + r.Component})
	}
	for _, name := range sortedKeys(components) {
		other, _ := components[name].(map[string]any)
		at := spec.Join("components", name)
		if uses, _ := other["uses"].(map[string]any); uses[r.Component] != nil {
			ref(spec.Join(spec.Join(at, "uses"), r.Component))
		}
		if after, _ := other["after"].([]any); containsValue(after, r.Component) {
			ref(spec.Join(at, "after"))
		}
	}
	if ingress, _ := tree["ingress"].(map[string]any); ingress != nil {
		routes, _ := ingress["routes"].(map[string]any)
		for _, path := range sortedKeys(routes) {
			if route, _ := routes[path].(map[string]any); route["component"] == r.Component {
				ref(spec.Join(spec.Join("ingress.routes", path), "component"))
			}
		}
	}
	if len(refs) > 0 {
		paths := make([]string, len(refs))
		for i, p := range refs {
			paths[i] = p.Path
		}
		return nil, &Error{Why: WhyComponentInUse, Remedy: fix(refs...),
			Message: fmt.Sprintf("%s is still referenced by %s; change those first", r.Component, strings.Join(paths, ", "))}
	}
	var volumes []string
	if r.DeleteVolumes {
		vols, _ := c["volumes"].(map[string]any)
		volumes = sortedKeys(vols)
	}
	delete(components, r.Component)
	return volumes, nil
}

var immutable = []string{"api_version", "kind", "name"}

func specPatch(tree map[string]any, p *oapv1.SpecPatch) (map[string]any, *Error) {
	if p.MergePatch == nil {
		return nil, fail(WhyIncomplete, "change.spec_patch.merge_patch", "spec_patch needs a merge_patch")
	}
	for _, field := range immutable {
		if _, touched := p.MergePatch.Fields[field]; touched {
			return nil, fail(WhyImmutableField, "change.spec_patch.merge_patch."+field, "%s can't be changed by a patch", field)
		}
	}
	patched, err := mergePatch(tree, structpb.NewStructValue(p.MergePatch), "")
	if err != nil {
		return nil, err
	}
	out, _ := patched.(map[string]any)
	return out, nil
}

// mergePatch applies an RFC 7386 JSON merge patch: objects merge key by key,
// null removes a key, and anything else replaces the target.
func mergePatch(target any, patch *structpb.Value, path string) (any, *Error) {
	obj := patch.GetStructValue()
	if obj == nil {
		return fromStruct(patch, path)
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	}
	for _, k := range sortedKeys(obj.Fields) {
		v := obj.Fields[k]
		if _, isNull := v.GetKind().(*structpb.Value_NullValue); isNull {
			delete(t, k)
			continue
		}
		merged, err := mergePatch(t[k], v, spec.Join(path, k))
		if err != nil {
			return nil, err
		}
		t[k] = merged
	}
	return t, nil
}

// fromStruct converts a protobuf JSON value to a tree value. Numbers must be
// whole; nulls are not allowed.
func fromStruct(v *structpb.Value, path string) (any, *Error) {
	switch k := v.GetKind().(type) {
	case *structpb.Value_StringValue:
		return k.StringValue, nil
	case *structpb.Value_BoolValue:
		return k.BoolValue, nil
	case *structpb.Value_NumberValue:
		n := k.NumberValue
		if n != math.Trunc(n) || math.Abs(n) > 1<<53 {
			return nil, fail(WhyBadValue, path, "numbers must be whole")
		}
		return int64(n), nil
	case *structpb.Value_ListValue:
		out := make([]any, 0, len(k.ListValue.Values))
		for i, e := range k.ListValue.Values {
			ev, err := fromStruct(e, spec.Index(path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
		return out, nil
	case *structpb.Value_StructValue:
		out := map[string]any{}
		for key, e := range k.StructValue.Fields {
			ev, err := fromStruct(e, spec.Join(path, key))
			if err != nil {
				return nil, err
			}
			out[key] = ev
		}
		return out, nil
	}
	return nil, fail(WhyBadValue, path, "null is not allowed; omit the field")
}

func sameApp(a, b *spec.Application) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func containsValue(list []any, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
