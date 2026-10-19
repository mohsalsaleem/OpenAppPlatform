package spec

import (
	"strings"
	"testing"
)

func mustJSON(t *testing.T, s string) any {
	t.Helper()
	tree, probs := FromJSON([]byte(s))
	if len(probs) > 0 {
		t.Fatalf("FromJSON: %v", probs)
	}
	return tree
}

func TestEmbeddedSchemaCompiles(t *testing.T) {
	if _, err := applicationSchema(); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaRejectsUnsupportedKeywordsAndRefs(t *testing.T) {
	for _, doc := range []string{
		`{"type": "object", "oneOf": []}`,
		`{"type": "number"}`,
		`{"$ref": "https://example.com/x"}`,
		`{"properties": {"a": {"$ref": "#/$defs/missing"}}}`,
	} {
		if _, err := CompileSchema([]byte(doc)); err == nil {
			t.Errorf("%s: want an error", doc)
		}
	}
}

func TestFromJSONRejectsFloatsNullsAndDuplicates(t *testing.T) {
	cases := map[string]string{
		`{"a": 1.5}`:        WhyFloat,
		`{"a": 1e3}`:        WhyFloat,
		`{"a": null}`:       WhyNull,
		`{"a": 1, "a": 2}`:  WhyDuplicateKey,
		`{"a": [1, null]}`:  WhyNull,
		`{"a": 1} {"b": 2}`: WhyJSONSyntax,
	}
	for in, want := range cases {
		_, probs := FromJSON([]byte(in))
		if len(probs) != 1 || probs[0].Why != want {
			t.Errorf("%s: got %v, want %s", in, probs, want)
		}
	}
}

func TestCanonicalAndHash(t *testing.T) {
	a := mustJSON(t, `{"b": [1, true, "x"], "a": {"z": "<&>", "y": -3}}`)
	b := mustJSON(t, `{"a": {"y": -3, "z": "<&>"}, "b": [1, true, "x"]}`)
	ca, _ := Canonical(a)
	if string(ca) != `{"a":{"y":-3,"z":"<&>"},"b":[1,true,"x"]}` {
		t.Fatalf("canonical: %s", ca)
	}
	ha, _ := Hash(a)
	hb, _ := Hash(b)
	if ha != hb || !strings.HasPrefix(ha, "sha256:") || len(ha) != 71 {
		t.Fatalf("hashes %s %s", ha, hb)
	}
	c := mustJSON(t, `{"a": {"y": -3, "z": "<&>"}, "b": [true, 1, "x"]}`)
	if hc, _ := Hash(c); hc == ha {
		t.Fatal("list order must change the hash")
	}
	if _, err := Canonical(map[string]any{"x": 1.5}); err == nil {
		t.Fatal("floats are not tree values")
	}
}

const full = `{
  "api_version": "oap.dev/v1",
  "kind": "Application",
  "name": "shop",
  "environment": "production",
  "ingress": {
    "domain": "shop.example.com",
    "https": true,
    "routes": {"/": {"component": "web"}, "/api": {"component": "api", "strip_prefix": true}}
  },
  "components": {
    "web": {"kind": "web", "image": "ghcr.io/acme/web@sha256:` + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + `", "port": 8080,
            "instances": 2, "uses": {"api": {"type": "http", "env": "API_URL"}},
            "health": {"mode": "http", "path": "/healthz"}},
    "api": {"kind": "web", "image": "ghcr.io/acme/api:1.4", "port": 9000,
            "env": {"LOG_LEVEL": "info"}, "secrets": {"DATABASE_URL": "db-url"},
            "uses": {"db": {"type": "database"}, "jobs": {"type": "queue"}}, "after": ["migrate"],
            "resources": {"memory_mib": 256, "cpu_millis": 500},
            "readiness": {"require_healthy": true, "timeout_seconds": 120}},
    "worker": {"kind": "worker", "image": "ghcr.io/acme/api:1.4", "uses": {"jobs": {"type": "queue"}}},
    "migrate": {"kind": "job", "image": "ghcr.io/acme/api:1.4", "secrets": {"DATABASE_URL": "db-url"}, "after": ["db"]},
    "db": {"kind": "data", "image": "postgres:17-alpine", "port": 5432, "volumes": {"pgdata": "/var/lib/postgresql/data"}},
    "jobs": {"kind": "data", "image": "redis:7-alpine", "port": 6379, "volumes": {"redis": "/data"}}
  }
}`

func TestParseApplicationAppliesDefaults(t *testing.T) {
	app, probs := ParseApplication(mustJSON(t, full))
	if len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	if got := app.Components["api"].Instances; got != 1 {
		t.Errorf("instances default: %d", got)
	}
	if h := app.Components["worker"].Health; h == nil || h.Mode != "image" {
		t.Errorf("worker health default: %+v", h)
	}
	if h := app.Components["web"].Health; h.IntervalSeconds != 10 || h.TimeoutSeconds != 3 || h.Retries != 3 {
		t.Errorf("http health defaults: %+v", h)
	}
	order := strings.Join(app.Order(), ",")
	if order != "db,jobs,migrate,api,web,worker" {
		t.Errorf("order: %s", order)
	}
}

// edit returns the full example with one change applied at a dotted path.
// A nil value deletes the key.
func edit(t *testing.T, path string, value any) any {
	t.Helper()
	tree := mustJSON(t, full)
	parts := strings.Split(path, ".")
	m := tree.(map[string]any)
	for _, p := range parts[:len(parts)-1] {
		m = m[p].(map[string]any)
	}
	last := parts[len(parts)-1]
	if value == nil {
		delete(m, last)
	} else {
		m[last] = value
	}
	return tree
}

func obj(t *testing.T, s string) any { return mustJSON(t, s) }

func TestParseApplicationRejections(t *testing.T) {
	cases := []struct {
		name, path string
		value      any
		wantPath   string
		wantWhy    string
	}{
		{"missing name", "name", nil, "name", WhyRequired},
		{"bad api version", "api_version", "oap.dev/v1alpha1", "api_version", WhyEnum},
		{"unknown top-level field", "target_id", "x", "target_id", WhyUnknownField},
		{"unknown component field", "components.api.strategy", "standard", "components.api.strategy", WhyUnknownField},
		{"bad component name", "components.Bad_Name", obj(t, `{"kind":"web","image":"x","port":1}`), "components.Bad_Name", WhyPattern},
		{"bad kind", "components.api.kind", "database", "components.api.kind", WhyEnum},
		{"too many instances", "components.api.instances", int64(5), "components.api.instances", WhyRange},
		{"string instances", "components.api.instances", "2", "components.api.instances", WhyType},
		{"low memory", "components.api.resources", obj(t, `{"memory_mib": 32, "cpu_millis": 100}`), "components.api.resources.memory_mib", WhyRange},
		{"web without port", "components.api.port", nil, "components.api.port", WhyKindRule},
		{"worker with port", "components.worker.port", int64(80), "components.worker", WhyKindRule},
		{"job with volume", "components.migrate.volumes", obj(t, `{"x": "/x"}`), "components.migrate", WhyKindRule},
		{"data without volume", "components.db.volumes", nil, "components.db.volumes", WhyKindRule},
		{"two instances with host port", "components.web.host_port", int64(8080), "components.web.host_port", WhyConflict},
		{"use of unknown component", "components.api.uses", obj(t, `{"nope": {"type": "http"}}`), "components.api.uses.nope", WhyUnknownComponent},
		{"http use of data", "components.api.uses", obj(t, `{"db": {"type": "http"}}`), "components.api.uses.db", WhyKindRule},
		{"env on database use", "components.api.uses", obj(t, `{"db": {"type": "database", "env": "DB"}}`), "components.api.uses.db.env", WhyKindRule},
		{"variable set twice", "components.api.env", obj(t, `{"DATABASE_URL": "x"}`), "components.api", WhyConflict},
		{"route to worker", "ingress.routes", obj(t, `{"/": {"component": "worker"}}`), "ingress.routes./.component", WhyKindRule},
		{"strip root", "ingress.routes", obj(t, `{"/": {"component": "web", "strip_prefix": true}}`), "ingress.routes./.strip_prefix", WhyConflict},
		{"ip domain", "ingress.domain", "10.0.0.1", "ingress.domain", WhyPattern},
		{"bad route path", "ingress.routes", obj(t, `{"/a//b": {"component": "web"}}`), "ingress.routes./a//b", WhyPattern},
		{"image health with path", "components.worker.health", obj(t, `{"mode": "image", "path": "/x"}`), "components.worker.health", WhyConflict},
		{"escalation without reason", "components.api.security", obj(t, `{"allow_privilege_escalation": true}`), "components.api.security.exception_reason", WhyRequired},
		{"bad digest", "components.api.image", "ghcr.io/acme/api@sha256:abc", "components.api.image", WhyPattern},
		{"after unknown", "components.web.after", []any{"nope"}, "components.web.after[0]", WhyUnknownComponent},
		{"after twice", "components.web.after", []any{"db", "db"}, "components.web.after[1]", WhyConflict},
		{"after and uses", "components.web.after", []any{"api"}, "components.web.after[0]", WhyConflict},
		{"after cycle", "components.db.after", []any{"migrate"}, "components", WhyCycle},
		{"cycle", "components.db.uses", obj(t, `{"api": {"type": "http"}}`), "components", WhyCycle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, probs := ParseApplication(edit(t, c.path, c.value))
			for _, p := range probs {
				if p.Path == c.wantPath && p.Why == c.wantWhy {
					return
				}
			}
			t.Fatalf("want %s at %s, got %v", c.wantWhy, c.wantPath, probs)
		})
	}
}

func TestJoinQuotesDottedKeys(t *testing.T) {
	if got := Join(Join("ingress.routes", "/a.b"), "component"); got != `ingress.routes."/a.b".component` {
		t.Fatal(got)
	}
}
