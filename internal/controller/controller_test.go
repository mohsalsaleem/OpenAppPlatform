package controller

import "testing"

func TestReadinessDoesNotMistakeUnhealthyForHealthy(t *testing.T) {
	for _, s := range []string{"running:unhealthy", "running:unknown", "starting", "exited", "unhealthy"} {
		if Healthy(s) {
			t.Fatalf("%q incorrectly ready", s)
		}
	}
	for _, s := range []string{"running", "running:healthy"} {
		if !Healthy(s) {
			t.Fatalf("%q should be running", s)
		}
	}
}
