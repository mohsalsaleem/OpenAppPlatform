package runtimeinputs

import (
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
	"testing"
)

func TestExactGrantsAndNoArbitraryProcessCredentials(t *testing.T) {
	b := Binding{"database-v1", "fixture", strings.Repeat("a", 32), "api", "DATABASE_URL", "OAP_SECRET_DATABASE_V1"}
	raw, _ := json.Marshal([]Binding{b})
	calls := 0
	inputs, err := Parse(raw, func(key string) string {
		calls++
		if key != b.ValueEnv {
			t.Fatal("unexpected lookup")
		}
		return "private-test-value"
	})
	if err != nil {
		t.Fatal(err)
	}
	c := domain.Component{Name: "api", SecretRefs: map[string]string{"DATABASE_URL": "database-v1"}, Env: map[string]string{"MODE": "test"}}
	resolved, err := inputs.Resolve(b.TargetID, b.ApplicationID, c)
	if err != nil || resolved.Env["DATABASE_URL"] != "private-test-value" || c.Env["DATABASE_URL"] != "" {
		t.Fatal("resolution or immutable definition failed")
	}
	for _, scope := range []struct{ target, app, component, key, ref string }{
		{"other", b.ApplicationID, "api", "DATABASE_URL", "database-v1"}, {b.TargetID, strings.Repeat("b", 32), "api", "DATABASE_URL", "database-v1"},
		{b.TargetID, b.ApplicationID, "web", "DATABASE_URL", "database-v1"}, {b.TargetID, b.ApplicationID, "api", "TOKEN", "database-v1"}, {b.TargetID, b.ApplicationID, "api", "DATABASE_URL", "other"},
	} {
		c.Name = scope.component
		c.SecretRefs = map[string]string{scope.key: scope.ref}
		if _, err = inputs.Resolve(scope.target, scope.app, c); err == nil {
			t.Fatal("cross-scope resolution")
		}
	}
	b.ValueEnv = "DATABASE_URL"
	raw, _ = json.Marshal([]Binding{b})
	if _, err = Parse(raw, func(string) string { t.Fatal("unauthorized process lookup"); return "" }); err == nil {
		t.Fatal("arbitrary credentials allowed")
	}
	if calls != 1 {
		t.Fatal("values should be loaded once")
	}
}
func TestMalformedMissingAndRedaction(t *testing.T) {
	b := Binding{"token-v1", "fixture", strings.Repeat("a", 32), "api", "TOKEN", "OAP_SECRET_TOKEN_V1"}
	raw, _ := json.Marshal([]Binding{b})
	for _, bad := range []string{string(raw) + "{}", `[{"value":"leak"}]`, string(raw[:len(raw)-1]) + "," + string(raw[1:])} {
		if _, err := Parse([]byte(bad), func(string) string { return "private-test-value" }); err == nil {
			t.Fatal("bad configuration accepted")
		}
	}
	i, err := Parse(raw, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	c := domain.Component{Name: "api", SecretRefs: map[string]string{"TOKEN": "token-v1"}}
	if _, err = i.Resolve("fixture", b.ApplicationID, c); err == nil {
		t.Fatal("missing input accepted")
	}
	i, err = Parse(raw, func(string) string { return "private-test-value" })
	if err != nil {
		t.Fatal(err)
	}
	if got := i.Redact("TOKEN=private-test-value\nordinary log"); got != "TOKEN=[REDACTED]\nordinary log" {
		t.Fatal("redaction failed")
	}
	resolved, _ := i.Resolve("fixture", b.ApplicationID, c)
	first := Fingerprint(resolved, "release-one")
	resolved.Env["TOKEN"] = "changed-test-value"
	if first == Fingerprint(resolved, "release-one") || first == Fingerprint(resolved, "release-two") {
		t.Fatal("fingerprint did not bind value and release")
	}
}
