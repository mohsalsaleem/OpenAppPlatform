package intent

import (
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const shop = `{
  "api_version": "oap.dev/v1", "kind": "Application", "name": "shop", "environment": "production",
  "ingress": {"domain": "shop.example.com", "routes": {"/": {"component": "api"}}},
  "components": {
    "migrate": {"kind": "job", "image": "reg/migrations:7"},
    "api": {"kind": "web", "image": "reg/api:7", "port": 8080, "after": ["migrate"],
            "env": {"LOG_LEVEL": "info"}, "uses": {"db": {"type": "database"}}},
    "worker": {"kind": "worker", "image": "reg/api:7"},
    "db": {"kind": "data", "image": "postgres:17", "port": 5432, "volumes": {"pgdata": "/var/lib/postgresql/data"}}
  }
}`

// v6 is an earlier desired state, before worker existed.
const v6 = `{
  "api_version": "oap.dev/v1", "kind": "Application", "name": "shop", "environment": "production",
  "components": {
    "migrate": {"kind": "job", "image": "reg/migrations:6"},
    "api": {"kind": "web", "image": "reg/api:6", "port": 8080},
    "db": {"kind": "data", "image": "postgres:16", "port": 5432, "volumes": {"pgdata": "/var/lib/postgresql/data"}}
  }
}`

func tree(t *testing.T, s string) any {
	t.Helper()
	v, probs := spec.FromJSON([]byte(s))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return v
}

func current(t *testing.T) Current {
	old := tree(t, v6)
	oldHash, _ := spec.Hash(old)
	return Current{Tree: tree(t, shop), History: func(h string) (any, bool) {
		if h == oldHash {
			return old, true
		}
		return nil, false
	}}
}

func intent(change any) *oapv1.Intent {
	in := &oapv1.Intent{Application: "shop"}
	switch c := change.(type) {
	case *oapv1.Scale:
		in.Change = &oapv1.Intent_Scale{Scale: c}
	case *oapv1.Deploy:
		in.Change = &oapv1.Intent_Deploy{Deploy: c}
	case *oapv1.Rollback:
		in.Change = &oapv1.Intent_Rollback{Rollback: c}
	case *oapv1.Configure:
		in.Change = &oapv1.Intent_Configure{Configure: c}
	case *oapv1.AddComponent:
		in.Change = &oapv1.Intent_AddComponent{AddComponent: c}
	case *oapv1.RemoveComponent:
		in.Change = &oapv1.Intent_RemoveComponent{RemoveComponent: c}
	case *oapv1.SpecPatch:
		in.Change = &oapv1.Intent_SpecPatch{SpecPatch: c}
	}
	return in
}

func object(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// changes renders a result's changes compactly, for example
// "replace components.api.instances 1→3".
func changes(r *Result) string {
	var parts []string
	for _, c := range r.Changes {
		s := string(c.Op) + " " + c.Path
		if c.Op == spec.OpReplace {
			s += " " + render(c.Before) + "→" + render(c.After)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

func render(v any) string {
	b, _ := spec.Canonical(v)
	return string(b)
}

func TestCompile(t *testing.T) {
	oldHash, _ := spec.Hash(tree(t, v6))
	cases := []struct {
		name    string
		in      *oapv1.Intent
		changes string
		check   func(*testing.T, *Result)
	}{
		{"scale", intent(&oapv1.Scale{Component: "api", Instances: 3}),
			"add components.api.instances", nil},
		{"deploy two images", intent(&oapv1.Deploy{Images: map[string]string{"api": "reg/api:8", "migrate": "reg/migrations:8"}}),
			`replace components.api.image "reg/api:7"→"reg/api:8"; replace components.migrate.image "reg/migrations:7"→"reg/migrations:8"`, nil},
		{"rollback keeps added components and config", intent(&oapv1.Rollback{ToRelease: oldHash}),
			`replace components.api.image "reg/api:7"→"reg/api:6"; replace components.db.image "postgres:17"→"postgres:16"; replace components.migrate.image "reg/migrations:7"→"reg/migrations:6"`,
			func(t *testing.T, r *Result) {
				if !r.Rollback {
					t.Error("rollback must be marked so jobs don't re-run")
				}
			}},
		{"configure env and resources", intent(&oapv1.Configure{Component: "api", SetEnv: map[string]string{"FEATURE_X": "on"},
			UnsetEnv: []string{"LOG_LEVEL"}, MemoryLimitMib: proto.Int32(256), CpuMillis: proto.Int32(500)}),
			`add components.api.env.FEATURE_X; remove components.api.env.LOG_LEVEL; add components.api.resources`, nil},
		{"configure unsets the last variable", intent(&oapv1.Configure{Component: "api", UnsetEnv: []string{"LOG_LEVEL"}}),
			"remove components.api.env", nil},
		{"add component", intent(&oapv1.AddComponent{Name: "mailer", Spec: object(t, map[string]any{
			"kind": "worker", "image": "reg/mailer:1", "after": []any{"migrate"}})}),
			"add components.mailer", nil},
		{"remove component keeps volumes", intent(&oapv1.RemoveComponent{Component: "worker"}),
			"remove components.worker",
			func(t *testing.T, r *Result) {
				if len(r.DeleteVolumes) != 0 {
					t.Error("no volumes")
				}
			}},
		{"spec patch merges and deletes", intent(&oapv1.SpecPatch{MergePatch: object(t, map[string]any{
			"components": map[string]any{"api": map[string]any{"env": nil, "instances": 2}},
			"ingress":    map[string]any{"https": true}})}),
			"remove components.api.env; add components.api.instances; add ingress.https", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur := current(t)
			before, _ := spec.Canonical(cur.Tree)
			r, err := Compile(cur, c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := changes(r); got != c.changes {
				t.Errorf("changes:\n got %s\nwant %s", got, c.changes)
			}
			if r.Kind != KindOf(c.in) || r.App == nil || !strings.HasPrefix(r.Hash, "sha256:") {
				t.Errorf("incomplete result: %+v", r)
			}
			if after, _ := spec.Canonical(cur.Tree); string(after) != string(before) {
				t.Error("Compile must not modify the current tree")
			}
			if c.check != nil {
				c.check(t, r)
			}
		})
	}
}

func TestCompileRemoveDataWithVolumes(t *testing.T) {
	cur := current(t)
	// db is in use by api; drop the connection first, then remove with volumes.
	step, err := Compile(cur, intent(&oapv1.SpecPatch{MergePatch: object(t, map[string]any{
		"components": map[string]any{"api": map[string]any{"uses": nil}}})}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Compile(Current{Tree: step.Tree}, intent(&oapv1.RemoveComponent{Component: "db", DeleteVolumes: true}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.DeleteVolumes, ",") != "pgdata" {
		t.Fatalf("volumes: %v", r.DeleteVolumes)
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name   string
		in     *oapv1.Intent
		why    string
		remedy string // "refresh", "intent" or "fix"
	}{
		{"no change named", &oapv1.Intent{Application: "shop"}, WhyMissingChange, "fix"},
		{"wrong application", &oapv1.Intent{Application: "blog", Change: &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "api", Instances: 2}}}, WhyWrongApplication, "refresh"},
		{"unknown component", intent(&oapv1.Scale{Component: "apii", Instances: 2}), WhyUnknownComponent, "refresh"},
		{"scale too far", intent(&oapv1.Scale{Component: "api", Instances: 9}), WhyInvalidResult, "intent"},
		{"scale a job", intent(&oapv1.Scale{Component: "migrate", Instances: 2}), WhyInvalidResult, "fix"},
		{"same scale", intent(&oapv1.Scale{Component: "api", Instances: 1}), WhyNoChange, "refresh"},
		{"same image", intent(&oapv1.Deploy{Images: map[string]string{"api": "reg/api:7"}}), WhyNoChange, "refresh"},
		{"empty deploy", intent(&oapv1.Deploy{}), WhyIncomplete, "fix"},
		{"bad image", intent(&oapv1.Deploy{Images: map[string]string{"api": "reg/api@sha256:short"}}), WhyInvalidResult, "fix"},
		{"unknown release", intent(&oapv1.Rollback{ToRelease: "sha256:nope"}), WhyUnknownRelease, "refresh"},
		{"unset missing variable", intent(&oapv1.Configure{Component: "api", UnsetEnv: []string{"NOPE"}}), WhyBadValue, "fix"},
		{"half resources", intent(&oapv1.Configure{Component: "api", MemoryLimitMib: proto.Int32(256)}), WhyIncomplete, "fix"},
		{"env clashes with secret-free rules", intent(&oapv1.Configure{Component: "api", SetEnv: map[string]string{"BAD-NAME": "x"}}), WhyInvalidResult, "fix"},
		{"add existing", intent(&oapv1.AddComponent{Name: "api", Spec: &structpb.Struct{}}), WhyComponentExists, "refresh"},
		{"add nameless", intent(&oapv1.AddComponent{}), WhyIncomplete, "fix"},
		{"add invalid", intent(&oapv1.AddComponent{Name: "mailer", Spec: &structpb.Struct{Fields: map[string]*structpb.Value{
			"kind": structpb.NewStringValue("worker")}}}), WhyInvalidResult, "fix"},
		{"add fractional", intent(&oapv1.AddComponent{Name: "mailer", Spec: &structpb.Struct{Fields: map[string]*structpb.Value{
			"instances": structpb.NewNumberValue(1.5)}}}), WhyBadValue, "fix"},
		{"remove in use", intent(&oapv1.RemoveComponent{Component: "db"}), WhyComponentInUse, "fix"},
		{"remove routed", intent(&oapv1.RemoveComponent{Component: "api"}), WhyComponentInUse, "fix"},
		{"remove ordered", intent(&oapv1.RemoveComponent{Component: "migrate"}), WhyComponentInUse, "fix"},
		{"patch name", intent(&oapv1.SpecPatch{MergePatch: &structpb.Struct{Fields: map[string]*structpb.Value{
			"name": structpb.NewStringValue("other")}}}), WhyImmutableField, "fix"},
		{"patch to invalid", intent(&oapv1.SpecPatch{MergePatch: &structpb.Struct{Fields: map[string]*structpb.Value{
			"surprise": structpb.NewBoolValue(true)}}}), WhyInvalidResult, "fix"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Compile(current(t), c.in)
			if err == nil {
				t.Fatalf("want %s, got result %+v", c.why, r)
			}
			if err.Why != c.why {
				t.Fatalf("want %s, got %v", c.why, err)
			}
			remedy := ""
			switch err.Remedy.GetAction().(type) {
			case *oapv1.Remedy_Refresh:
				remedy = "refresh"
			case *oapv1.Remedy_Intent:
				remedy = "intent"
			case *oapv1.Remedy_Fix:
				remedy = "fix"
				if len(err.Remedy.GetFix().GetFields()) == 0 {
					t.Fatal("a fix remedy must name at least one field")
				}
			}
			if remedy != c.remedy {
				t.Fatalf("remedy: want %q, got %q (%v)", c.remedy, remedy, err.Remedy)
			}
			if d := err.Detail(); d.Why != c.why || d.Message == "" {
				t.Fatalf("detail: %v", d)
			}
		})
	}
}

func TestComponentInUseNamesEveryReference(t *testing.T) {
	_, err := Compile(current(t), intent(&oapv1.RemoveComponent{Component: "api"}))
	var paths []string
	for _, f := range err.Remedy.GetFix().GetFields() {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "ingress.routes./.component" {
		t.Fatalf("paths: %v", paths)
	}
	_, err = Compile(current(t), intent(&oapv1.RemoveComponent{Component: "migrate"}))
	if f := err.Remedy.GetFix().GetFields(); len(f) != 1 || f[0].Path != "components.api.after" {
		t.Fatalf("fields: %v", f)
	}
}

func TestScaleRemedyClampsToTheLimit(t *testing.T) {
	_, err := Compile(current(t), intent(&oapv1.Scale{Component: "api", Instances: 9}))
	if got := err.Remedy.GetIntent().GetScale().GetInstances(); got != 4 {
		t.Fatalf("remedy should scale to the maximum of 4, got %d", got)
	}
	if len(err.Problems) == 0 || err.Problems[0].Path != "components.api.instances" {
		t.Fatalf("problems: %v", err.Problems)
	}
}

func TestDiff(t *testing.T) {
	a := tree(t, `{"a": {"x": 1, "y": [1, 2]}, "b": "s", "c": true}`)
	b := tree(t, `{"a": {"x": 2, "y": [1, 2], "z": "new"}, "c": true, "d": {}}`)
	var got []string
	for _, c := range spec.Diff(a, b) {
		got = append(got, string(c.Op)+" "+c.Path)
	}
	want := "replace a.x; add a.z; remove b; add d"
	if strings.Join(got, "; ") != want {
		t.Fatalf("got %s", strings.Join(got, "; "))
	}
	if len(spec.Diff(a, spec.Clone(a))) != 0 {
		t.Fatal("a clone has no differences")
	}
}
