package system_test

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/riverqueue/river"
	"reflect"
	"testing"
)

type healthFixture struct {
	*observationAdapter
	current *domain.HealthCheck
	drift   bool
}

func (f *healthFixture) Capabilities() operator.Capabilities {
	caps := f.fake.Capabilities()
	caps.HTTPHealthChecks = true
	return caps
}
func (f *healthFixture) Ensure(ctx context.Context, s operator.Spec) (operator.Resource, error) {
	h := *s.Component.HealthCheck
	f.current = &h
	return f.fake.Ensure(ctx, s)
}
func (f *healthFixture) CheckHealthCheck(_ context.Context, _ string, comp domain.Component, _ bool) error {
	if f.drift || !reflect.DeepEqual(comp.HealthCheck, f.current) {
		return errors.New("native health configuration differs")
	}
	return nil
}
func TestFrozenHTTPProbeChecksDriftRestartAndRecoveryWithoutRedispatch(t *testing.T) {
	f := &healthFixture{observationAdapter: &observationAdapter{fake: &fake{resources: map[string]operator.Resource{}}, status: operator.DeploymentStatus{State: "succeeded", ResourceStatus: "running"}}}
	c, _ := setup(t, f)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].HealthCheck = &domain.HealthCheck{Mode: "http", Path: "/ready"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "http-frozen")
	if err != nil {
		t.Fatal(err)
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].HealthCheck = &domain.HealthCheck{Mode: "image"}
	if _, err = c.UpdateApplication(ctx, app.ID, changed, app.Version); err != nil {
		t.Fatal(err)
	}
	advance := func(c *controller.Controller) {
		t.Helper()
		err := c.Advance(ctx, d.ID)
		var snooze *river.JobSnoozeError
		if err != nil && !errors.As(err, &snooze) {
			t.Fatal(err)
		}
	}
	advance(c)
	f.drift = true
	advance(c)
	held, _ := c.Store.Deployment(ctx, d.ID)
	if held.State != "attention" || f.deploys != 0 {
		t.Fatal("native drift dispatched work", held)
	}
	f.drift = false
	d, err = c.Recover(ctx, d.ID, controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: held.UpdatedAt, RetryPreparation: true})
	if err != nil {
		t.Fatal(err)
	}
	advance(c)
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	advance(restarted)
	waiting, _ := c.Store.Deployment(ctx, d.ID)
	if waiting.State != "running" || waiting.Steps[0].Phase != "observing" || waiting.Manifest.Components[0].HealthCheck.Path != "/ready" || f.deploys != 1 {
		t.Fatal("running-only bypassed frozen HTTP health gate", waiting)
	}
	f.drift = true
	advance(restarted)
	held, _ = c.Store.Deployment(ctx, d.ID)
	if held.State != "attention" || f.deploys != 1 {
		t.Fatal("observation drift was not fenced")
	}
	req := controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: held.UpdatedAt}
	if _, err = restarted.Recover(ctx, d.ID, req); err == nil {
		t.Fatal("recovery accepted native drift")
	}
	f.drift = false
	f.status.ResourceStatus = "running:healthy"
	d, err = restarted.Recover(ctx, d.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	advance(restarted)
	done, _ := c.Store.Deployment(ctx, d.ID)
	if done.State != "succeeded" || f.deploys != 1 {
		t.Fatal("recovery redispatched", done)
	}
}
