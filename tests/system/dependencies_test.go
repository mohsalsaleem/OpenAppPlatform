package system_test

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strings"
	"testing"
)

type dependencyFixture struct {
	*fake
	holdLast bool
	fail     bool
}

func (a *dependencyFixture) Observe(_ context.Context, _, resource string) (operator.DeploymentStatus, error) {
	if a.fail {
		return operator.DeploymentStatus{State: "failed", ResourceStatus: "exited"}, nil
	}
	status := "running:healthy"
	if a.holdLast && strings.HasSuffix(a.resources[resource].Name, "-2") {
		status = "starting"
	}
	return operator.DeploymentStatus{State: "succeeded", ResourceStatus: status}, nil
}

func TestDependencyReplicasVerifyBeforeDependentAndSurviveRestart(t *testing.T) {
	adapter := &dependencyFixture{fake: &fake{resources: map[string]operator.Resource{}}, holdLast: true}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].DependsOn = []string{"api"}
	m.Components = append(m.Components, domain.Component{Name: "api", Image: "image:v1", Port: 80, Instances: 2})
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	release, err := c.Enqueue(ctx, app.ID, "ordered-starts")
	if err != nil {
		t.Fatal(err)
	}
	for range 12 {
		_ = c.Advance(ctx, release.ID)
	}
	waiting, _ := c.Store.Deployment(ctx, release.ID)
	if waiting.Steps[0].Component != "api" || waiting.Steps[0].Phase != "succeeded" || waiting.Steps[1].Phase != "observing" || waiting.Steps[2].ResourceID != "" || adapter.deploys != 2 {
		t.Fatal("dependent started before every dependency replica verified")
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].DependsOn = nil
	if _, err = c.UpdateApplication(ctx, app.ID, changed, app.Version); err != nil {
		t.Fatal(err)
	}
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	_ = restarted.Advance(ctx, release.ID)
	stillWaiting, _ := c.Store.Deployment(ctx, release.ID)
	if stillWaiting.Steps[2].ResourceID != "" || adapter.deploys != 2 {
		t.Fatal("restart/edit bypassed frozen dependency gate")
	}
	adapter.holdLast = false
	for range 10 {
		_ = restarted.Advance(ctx, release.ID)
	}
	done, _ := c.Store.Deployment(ctx, release.ID)
	if done.State != "succeeded" || adapter.deploys != 3 {
		t.Fatal("ordered completion failed or redispatched")
	}
}

func TestFailedDependencyNeverPreparesOrDispatchesDependent(t *testing.T) {
	adapter := &dependencyFixture{fake: &fake{resources: map[string]operator.Resource{}}, fail: true}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].DependsOn = []string{"api"}
	m.Components = append(m.Components, domain.Component{Name: "api", Image: "image:v1", Port: 80})
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	release, err := c.Enqueue(ctx, app.ID, "failed-dependency")
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		_ = c.Advance(ctx, release.ID)
	}
	done, _ := c.Store.Deployment(ctx, release.ID)
	if done.State != "failed" || done.Steps[1].ResourceID != "" || adapter.deploys != 1 || len(adapter.resources) != 1 {
		t.Fatal("dependent operation ran after failed dependency")
	}
}

func TestUnselectedDependencyIsRecheckedBeforeDispatchAndCanResume(t *testing.T) {
	adapter := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].DependsOn = []string{"api"}
	m.Components = append(m.Components, domain.Component{Name: "api", Image: "image:v1", Port: 80})
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	adapter.resources["existing-api"] = operator.Resource{ID: "existing-api", Description: "OpenAppPlatform:" + app.ID + ":api", Status: "running:healthy"}
	if err = c.Store.Bind(ctx, "fixture", "existing-api", app.ID, "api", 1); err != nil {
		t.Fatal(err)
	}
	release, err := c.Enqueue(ctx, app.ID, "partial-dependency")
	if err != nil {
		t.Fatal(err)
	}
	// Persist the affected-component selection used by source releases. The signed
	// GitHub test separately exercises this selection through actual intake.
	release.Steps = release.Steps[1:]
	if err = c.Store.SaveDeployment(ctx, release); err != nil {
		t.Fatal(err)
	}
	_ = c.Advance(ctx, release.ID) // dependency healthy: prepare web only
	resource := adapter.resources["existing-api"]
	resource.Status = "running:unhealthy"
	adapter.resources[resource.ID] = resource
	_ = c.Advance(ctx, release.ID) // recheck blocks before durable dispatch intent
	held, _ := c.Store.Deployment(ctx, release.ID)
	if held.State != "attention" || held.Steps[0].RecoveryPhase != "prepared" || adapter.deploys != 0 {
		t.Fatal("unhealthy unselected dependency did not block dispatch")
	}
	resource.Status = "running:healthy"
	adapter.resources[resource.ID] = resource
	recovered, err := c.Recover(ctx, release.ID, controller.Recovery{Component: "web", Ordinal: 1, RetryPreparation: true, ExpectedUpdatedAt: held.UpdatedAt})
	if err != nil || recovered.Steps[0].Phase != "prepared" {
		t.Fatal("pre-dispatch recovery failed", err)
	}
	for range 5 {
		_ = c.Advance(ctx, release.ID)
	}
	done, _ := c.Store.Deployment(ctx, release.ID)
	if done.State != "succeeded" || adapter.deploys != 1 || len(adapter.resources) != 2 {
		t.Fatal("recovery redeployed unselected dependency or duplicated dispatch")
	}
}
