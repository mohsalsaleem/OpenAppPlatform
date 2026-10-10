package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
	"time"
)

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

func TestStrictReadinessUsesReportedHealthAndLegacyDefaults(t *testing.T) {
	strict := domain.Component{Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 30}}
	for _, status := range []string{"running", "running:unknown", "running:unhealthy", "starting", "exited"} {
		if releaseReady(strict, status) {
			t.Fatal("strict policy accepted", status)
		}
	}
	if !releaseReady(strict, "running:healthy") || !releaseReady(domain.Component{}, "running") {
		t.Fatal("readiness compatibility failed")
	}
	if observationTimeout(strict) != 30*time.Second || observationTimeout(domain.Component{}) != 15*time.Minute {
		t.Fatal("timeout compatibility failed")
	}
}
