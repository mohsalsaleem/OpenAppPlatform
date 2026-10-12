package runtimeinputs

import (
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
	"testing"
)

func TestRegistryExactScopeHostAndRecovery(t *testing.T) {
	b := RegistryBinding{"read-v1", "docker-local", strings.Repeat("a", 32), "web", "localhost:5000", "OAP_SECRET_REGISTRY_USER", "OAP_SECRET_REGISTRY_PASSWORD"}
	raw, _ := json.Marshal([]RegistryBinding{b})
	inputs, err := AddRegistries(nil, raw, func(key string) string {
		if key == b.UsernameEnv {
			return "reader"
		}
		return "private-fixture-password"
	})
	if err != nil {
		t.Fatal(err)
	}
	c := domain.Component{Name: "web", Image: "localhost:5000/team/private:v1", RegistryCredentialRef: "read-v1"}
	resolved, err := inputs.Resolve(b.TargetID, b.ApplicationID, c)
	if err != nil || resolved.RegistryAuth == nil || c.RegistryAuth != nil {
		t.Fatal("resolution failed", err)
	}
	serialized, _ := json.Marshal(resolved)
	if strings.Contains(string(serialized), "private-fixture-password") || strings.Contains(string(serialized), "serveraddress") {
		t.Fatal("credentials serialized")
	}
	first := Fingerprint(resolved, "release")
	resolved.RegistryAuth.Password = "changed-fixture-password"
	if Fingerprint(resolved, "release") == first {
		t.Fatal("changed credential not pinned")
	}
	for _, change := range []func(*domain.Component){func(c *domain.Component) { c.Name = "other" }, func(c *domain.Component) { c.Image = "evil.example/team/private:v1" }, func(c *domain.Component) { c.RegistryCredentialRef = "other" }} {
		next := c
		change(&next)
		if _, err := inputs.Resolve(b.TargetID, b.ApplicationID, next); err == nil {
			t.Fatal("unauthorized credential dispatch")
		}
	}
	for _, scope := range [][2]string{{"other", b.ApplicationID}, {b.TargetID, strings.Repeat("b", 32)}} {
		if _, err := inputs.Resolve(scope[0], scope[1], c); err == nil {
			t.Fatal("cross scope")
		}
	}
	for _, secret := range registryRedactions(inputs.registries[0].auth) {
		if inputs.Redact("message:"+secret) != "message:[REDACTED]" {
			t.Fatal("credential encoding leaked")
		}
	}
	missing, _ := AddRegistries(nil, raw, func(string) string { return "" })
	if _, err := missing.Resolve(b.TargetID, b.ApplicationID, c); err == nil {
		t.Fatal("missing accepted")
	}
	b.PasswordEnv = "HOME"
	raw, _ = json.Marshal([]RegistryBinding{b})
	if _, err := AddRegistries(nil, raw, func(string) string { t.Fatal("arbitrary env lookup"); return "" }); err == nil {
		t.Fatal("arbitrary process env")
	}
}
