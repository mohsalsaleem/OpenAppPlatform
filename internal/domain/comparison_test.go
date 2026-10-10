package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReleaseComparisonHidesAllRuntimeMapValues(t *testing.T) {
	a := Deployment{Manifest: Manifest{Components: []Component{{Name: "web", Env: map[string]string{"TOKEN": "secret-a"}, Services: map[string]string{"API": "old-private"}, ServiceEndpoints: map[string]string{"api": "https://private-a.test"}}}}}
	b := Deployment{Manifest: Manifest{Components: []Component{{Name: "web", Env: map[string]string{"TOKEN": "secret-b"}, Services: map[string]string{"API": "new-private"}, ServiceEndpoints: map[string]string{"api": "https://private-b.test"}, Readiness: &ReadinessPolicy{TimeoutSeconds: 900}}}}}
	comparison := CompareReleases(a, b)
	raw, _ := json.Marshal(comparison)
	for _, value := range []string{"secret-a", "secret-b", "old-private", "new-private", "https://private-a.test", "https://private-b.test"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("runtime map value leaked")
		}
	}
	if len(comparison.Changes) != 3 {
		t.Fatal("default readiness produced a false change")
	}
	b.Manifest.Components[0] = a.Manifest.Components[0]
	if len(CompareReleases(a, b).Changes) != 0 {
		t.Fatal("equal snapshots differ")
	}
}
