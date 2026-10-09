package system_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/riverqueue/river"
)

type retirementFake struct {
	*restartFake
	stops        int
	loseResponse bool
}

func (f *retirementFake) Capabilities() operator.Capabilities {
	caps := f.restartFake.Capabilities()
	caps.Retirement = true
	return caps
}
func (f *retirementFake) Retire(_ context.Context, id, owner string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resource := f.resources[id]
	if resource.Description != owner {
		return "", errors.New("ownership mismatch")
	}
	if resource.Status != "exited" {
		f.stops++
	}
	resource.Status = "exited"
	f.resources[id] = resource
	if f.loseResponse {
		return "", errors.New("response lost")
	}
	return id, nil
}
func (f *retirementFake) ObserveRetirement(ctx context.Context, remote, id, owner string) (operator.DeploymentStatus, error) {
	resource, e := f.Inspect(ctx, id)
	if e != nil {
		return operator.DeploymentStatus{}, e
	}
	if remote != id || resource.Description != owner {
		return operator.DeploymentStatus{}, errors.New("identity mismatch")
	}
	state := "running"
	if resource.Status == "exited" {
		state = "succeeded"
	}
	return operator.DeploymentStatus{State: state, ResourceStatus: resource.Status}, nil
}
func (f *retirementFake) Deploy(ctx context.Context, id string) (string, error) {
	remote, e := f.fake.Deploy(ctx, id)
	if e != nil {
		return remote, e
	}
	f.mu.Lock()
	resource := f.resources[id]
	resource.Status = "running:healthy"
	f.resources[id] = resource
	f.mu.Unlock()
	return remote, nil
}
func advanceOnce(t *testing.T, c *controller.Controller, id string) {
	t.Helper()
	if e := c.Advance(context.Background(), id); e != nil {
		var snooze *river.JobSnoozeError
		if !errors.As(e, &snooze) {
			t.Fatal(e)
		}
	}
}
func managedInstances(t *testing.T, c *controller.Controller, f *retirementFake, count int) domain.Application {
	t.Helper()
	m := manifest()
	m.Components[0].Instances = count
	app, e := c.CreateApplication(context.Background(), m)
	if e != nil {
		t.Fatal(e)
	}
	for ordinal := 1; ordinal <= count; ordinal++ {
		ref := fmt.Sprintf("instance-%d", ordinal)
		f.resources[ref] = operator.Resource{ID: ref, Name: domain.ResourceName(app.ID, "web", ordinal), Description: "OpenAppPlatform:" + app.ID + ":web", Image: m.Components[0].Image, ArtifactKind: "image", Status: "running:healthy"}
		if e = c.Store.Bind(context.Background(), "fixture", ref, app.ID, "web", ordinal); e != nil {
			t.Fatal(e)
		}
	}
	return app
}
func TestScaleDownConfirmsStopsBeforeChangingCountsAndReservesBindings(t *testing.T) {
	f := &retirementFake{restartFake: &restartFake{fake: &fake{resources: map[string]operator.Resource{}}}}
	c, _ := setup(t, f)
	ctx := context.Background()
	app := managedInstances(t, c, f, 3)
	req := controller.ScaleDownRequest{Component: "web", Instances: 1}
	d, e := c.EnqueueScaleDown(ctx, app.ID, "retire-tail-instances", req, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Steps) != 2 || d.Steps[0].Ordinal != 3 || d.Steps[1].Ordinal != 2 {
		t.Fatal("wrong retirement scope", d)
	}
	before, e := c.Store.Application(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	if before.Version != app.Version || before.Manifest.Components[0].Instances != 3 {
		t.Fatal("counts changed before stop confirmation")
	}
	if _, e = c.UpdateApplication(ctx, app.ID, before.Manifest, before.Version); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("definition edit raced retirement", e)
	}
	duplicate, e := c.EnqueueScaleDown(ctx, app.ID, "retire-tail-instances", req, app.Version)
	if e != nil || duplicate.ID != d.ID {
		t.Fatal("retirement deduplication failed", e)
	}
	if _, e = c.Enqueue(ctx, app.ID, "overlapping-deploy"); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("deployment raced retirement")
	}
	for i := 0; i < 10; i++ {
		advanceOnce(t, c, d.ID)
	}
	d, e = c.Store.Deployment(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	after, e := c.Store.Application(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	if d.State != "succeeded" || after.Manifest.Components[0].Instances != 1 || after.Version != app.Version+1 {
		t.Fatal("retirement did not commit counts", d, after)
	}
	if f.stops != 2 || len(f.resources) != 3 || f.resources["instance-1"].Status != "running:healthy" {
		t.Fatal("retirement changed survivor or deleted resources")
	}
	instances, e := c.Instances(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(instances) != 3 || instances[0].Retired || !instances[1].Retired || !instances[2].Retired {
		t.Fatal("retirement visibility lost", instances)
	}
	duplicate, e = c.EnqueueScaleDown(ctx, app.ID, "retire-tail-instances", req, app.Version)
	if e != nil || duplicate.ID != d.ID {
		t.Fatal("post-completion retry lost idempotency", e)
	}
	other := manifest()
	other.Name = "other-owner"
	other.Components[0].Instances = 1
	other.Components[0].ResourceID = "instance-2"
	if _, e = c.CreateApplication(ctx, other); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("retired binding was available to another application", e)
	}
	after.Manifest.Components[0].Instances = 3
	after, e = c.UpdateApplication(ctx, app.ID, after.Manifest, after.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.EnqueueRestart(ctx, app.ID, "retired-restart", controller.RestartRequest{Component: "web", Ordinal: 2}, after.Version); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("retired instance restarted before deployment", e)
	}
	reactivated, e := c.EnqueueVersion(ctx, app.ID, "reactivate-retained", nil, after.Version)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 15; i++ {
		advanceOnce(t, c, reactivated.ID)
	}
	instances, e = c.Instances(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, instance := range instances {
		if instance.Retired || !controller.Healthy(instance.Status) {
			t.Fatal("retained binding did not reactivate", instances)
		}
	}
}
func TestLostRetirementResponseRecoversWithoutAnotherStop(t *testing.T) {
	f := &retirementFake{restartFake: &restartFake{fake: &fake{resources: map[string]operator.Resource{}}}, loseResponse: true}
	c, srv := setup(t, f)
	ctx := context.Background()
	app := managedInstances(t, c, f, 2)
	d, e := c.EnqueueScaleDown(ctx, app.ID, "uncertain-retirement", controller.ScaleDownRequest{Component: "web", Instances: 1}, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	advanceOnce(t, c, d.ID)
	advanceOnce(t, c, d.ID)
	d, e = c.Store.Deployment(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	unchanged, _ := c.Store.Application(ctx, app.ID)
	if d.State != "attention" || unchanged.Manifest.Components[0].Instances != 2 || f.stops != 1 {
		t.Fatal("uncertain retirement was treated as completed", d)
	}
	status, _ := request(t, srv, "POST", "/api/v1/deployments/"+d.ID+"/recover", "", map[string]any{"retryFinalization": true, "expectedUpdatedAt": d.UpdatedAt})
	if status != 422 {
		t.Fatal("unconfirmed retirement finalized")
	}
	recovery := controller.Recovery{Component: "web", Ordinal: 2, RemoteDeploymentID: "instance-2", ExpectedUpdatedAt: d.UpdatedAt}
	d, e = c.Recover(ctx, d.ID, recovery)
	if e != nil {
		t.Fatal(e)
	}
	advanceOnce(t, c, d.ID)
	finished, _ := c.Store.Application(ctx, app.ID)
	if finished.Manifest.Components[0].Instances != 1 || f.stops != 1 {
		t.Fatal("recovery repeated stop or lost count update")
	}
}
func TestRetirementFinalizationRetryAndRetiredDrift(t *testing.T) {
	f := &retirementFake{restartFake: &restartFake{fake: &fake{resources: map[string]operator.Resource{}}}}
	c, _ := setup(t, f)
	ctx := context.Background()
	app := managedInstances(t, c, f, 2)
	d, e := c.EnqueueScaleDown(ctx, app.ID, "finalize-retirement", controller.ScaleDownRequest{Component: "web", Instances: 1}, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		advanceOnce(t, c, d.ID)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	d.State = "attention"
	if e = c.Store.SaveDeployment(ctx, d); e != nil {
		t.Fatal(e)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	if _, e = c.Recover(ctx, d.ID, controller.Recovery{RetryFinalization: true, ExpectedUpdatedAt: d.UpdatedAt}); e != nil {
		t.Fatal(e)
	}
	advanceOnce(t, c, d.ID)
	after, _ := c.Store.Application(ctx, app.ID)
	if after.Manifest.Components[0].Instances != 1 || f.stops != 1 {
		t.Fatal("finalization redispatched stop")
	}
	resource := f.resources["instance-2"]
	resource.Status = "running:healthy"
	f.resources["instance-2"] = resource
	instances, e := c.Instances(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	if instances[1].Status != "retired:running" || instances[1].Error == "" {
		t.Fatal("retired runtime drift hidden", instances)
	}
}
