package domain

import "testing"

func TestNativeHealthCheckValidationRejectsUnsafePathsAndForeignOwnership(t *testing.T) {
	for _, path := range []string{"https://example.invalid/health", "//example.invalid/health", "/ready?token=x", "/ready#x", "/ready;touch", "/ready\nfoo", "/ready'"} {
		h := HealthCheck{Mode: "http", Path: path}
		if h.Validate() == nil {
			t.Fatalf("unsafe path accepted %q", path)
		}
	}
	h := HealthCheck{Mode: "http", Path: "/health/ready"}
	if err := h.Validate(); err != nil || h.IntervalSeconds != 10 || h.TimeoutSeconds != 3 || h.Retries != 3 {
		t.Fatal("defaults", err, h)
	}
	h.TimeoutSeconds = 31
	if h.Validate() == nil {
		t.Fatal("unbounded timeout accepted")
	}
	m := Manifest{Name: "test", Environment: "staging", TargetID: "fixture", Components: []Component{{Name: "web", Kind: "web", Image: "image:v1", Port: 8080, ResourceID: "native", HealthCheck: &HealthCheck{Mode: "http", Path: "/ready"}}}}
	if m.Validate() == nil {
		t.Fatal("adopted native configuration overwritten")
	}
}
