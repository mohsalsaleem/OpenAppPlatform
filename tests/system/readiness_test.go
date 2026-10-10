package system_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/riverqueue/river"
)

func TestFrozenReadinessSurvivesConfigurationChangesRestartTimeoutAndRecovery(t *testing.T) {
	adapter := &observationAdapter{fake: &fake{resources: map[string]operator.Resource{}}, status: operator.DeploymentStatus{State: "succeeded", ResourceStatus: "running"}}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].Readiness = &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 30}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	release, err := c.Enqueue(ctx, app.ID, "frozen-readiness")
	if err != nil {
		t.Fatal(err)
	}
	changed := app.Manifest
	changed.Components[0].Readiness = nil
	if _, err = c.UpdateApplication(ctx, app.ID, changed, app.Version); err != nil {
		t.Fatal(err)
	}
	advance := func(c *controller.Controller, id string) {
		t.Helper()
		err := c.Advance(ctx, id)
		var snooze *river.JobSnoozeError
		if err != nil && !errors.As(err, &snooze) {
			t.Fatal(err)
		}
	}
	advance(c, release.ID) // prepare
	advance(c, release.ID) // exactly one dispatch
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	advance(restarted, release.ID)
	waiting, _ := c.Store.Deployment(ctx, release.ID)
	if waiting.Steps[0].Phase != "observing" || !waiting.Manifest.Components[0].Readiness.RequireHealthy || adapter.deploys != 1 {
		t.Fatal("running-only status passed frozen health policy or dispatch was repeated")
	}
	// Persist an expired observation to exercise timeout without a wall-clock sleep.
	expired := time.Now().Add(-31 * time.Second)
	waiting.Steps[0].ObservationStartedAt = &expired
	if err = c.Store.SaveDeployment(ctx, waiting); err != nil {
		t.Fatal(err)
	}
	advance(restarted, release.ID)
	timedOut, _ := c.Store.Deployment(ctx, release.ID)
	if timedOut.State != "attention" || timedOut.Steps[0].RecoveryPhase != "observing" || adapter.deploys != 1 {
		t.Fatal("configured timeout did not hold release")
	}
	request := controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: timedOut.UpdatedAt}
	if _, err = restarted.Recover(ctx, release.ID, request); err == nil {
		t.Fatal("recovery bypassed frozen healthy requirement")
	}
	adapter.status.ResourceStatus = "running:healthy"
	recovered, err := restarted.Recover(ctx, release.ID, request)
	if err != nil || recovered.Steps[0].Phase != "succeeded" {
		t.Fatal("healthy recovery failed", err)
	}
	advance(restarted, release.ID)
	done, _ := c.Store.Deployment(ctx, release.ID)
	if done.State != "succeeded" || adapter.deploys != 1 {
		t.Fatal("readiness recovery redispatched or lost completion")
	}
}
