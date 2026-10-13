package controller

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
	"time"
)

func TestIncidentVerificationSeparatesHealthFreshnessAndBehavior(t *testing.T) {
	now := time.Now().UTC()
	base := Incident{GeneratedAt: now, ValidUntil: now.Add(time.Second), SnapshotConsistent: true, Observations: []IncidentObservation{{Status: "running:healthy"}}, Operations: []IncidentOperation{{State: "succeeded"}}}
	state, criteria := incidentVerification(base, false)
	if state != "observed_healthy" || criteria[3].Status != "missing" || base.ApplicationBehaviorVerified {
		t.Fatal("health became application verification")
	}
	if state, _ = incidentVerification(base, true); state != "inconclusive" {
		t.Fatal("partial healthy sample accepted")
	}
	stale := base
	stale.ValidUntil = now.Add(-time.Second)
	if state, _ = incidentVerification(stale, false); state != "inconclusive" {
		t.Fatal("stale evidence accepted")
	}
	stale = base
	stale.SnapshotConsistent = false
	if state, _ = incidentVerification(stale, false); state != "inconclusive" {
		t.Fatal("inconsistent sample accepted")
	}
	bad := base
	bad.Observations = []IncidentObservation{{Status: "running:unhealthy"}}
	if state, _ = incidentVerification(bad, false); state != "issue" {
		t.Fatal("unhealthy instance ignored")
	}
	missing := base
	missing.Observations = []IncidentObservation{{Status: "unavailable"}}
	if state, _ = incidentVerification(missing, false); state != "inconclusive" {
		t.Fatal("outage treated as healthy or observed failure")
	}
	pending := base
	pending.Operations = []IncidentOperation{{State: "running"}}
	if state, _ = incidentVerification(pending, false); state != "inconclusive" {
		t.Fatal("admission accepted as outcome")
	}
	uncertain := base
	uncertain.Operations = []IncidentOperation{{State: "attention"}}
	if state, _ = incidentVerification(uncertain, false); state != "issue" {
		t.Fatal("uncertain dispatch hidden")
	}
}

func TestIncidentDoesNotReadUnownedNativeLogs(t *testing.T) {
	c := Controller{}
	for _, component := range []domain.Component{{Name: "database", Kind: "database"}, {Name: "linked", Management: "observe"}, {Name: "adopted", ResourceID: "native-id"}} {
		app := domain.Application{Manifest: domain.Manifest{Components: []domain.Component{component}}}
		sample := c.incidentLogs(context.Background(), app, nil, component.Name, 1)
		if sample.Status != "unavailable" || sample.MissingReason != "native_log_redaction_not_owned" || sample.Text != "" {
			t.Fatal("native logs exposed without owned redaction coverage")
		}
	}
}
