package system_test

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strings"
	"testing"
)

type rollbackFixture struct{ *fake }

func (f *rollbackFixture) Capabilities() operator.Capabilities {
	return operator.Capabilities{ImageRollback: true, Standard: true, ImmutableImages: true, Environment: true}
}
func (f *rollbackFixture) CheckRollbackConfiguration(context.Context, string, domain.Component) error {
	return nil
}
func (f *rollbackFixture) Ensure(ctx context.Context, s operator.Spec) (operator.Resource, error) {
	resource, err := f.fake.Ensure(ctx, s)
	if err != nil {
		return resource, err
	}
	resource.Image = s.Component.Image
	resource.Port = s.Component.Port
	resource.Status = "running:healthy"
	f.resources[resource.ID] = resource
	return resource, nil
}
func rollbackHistory(t *testing.T) (*controller.Controller, *rollbackFixture, domain.Application, domain.Deployment, domain.Deployment) {
	t.Helper()
	f := &rollbackFixture{fake: &fake{resources: map[string]operator.Resource{}}}
	c, _ := setup(t, f)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 2
	m.Components[0].Image = "registry.test/web@sha256:" + strings.Repeat("a", 64)
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	old, err := c.Enqueue(ctx, app.ID, "rollback-old-release")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		_ = c.Advance(ctx, old.ID)
	}
	old, _ = c.Store.Deployment(ctx, old.ID)
	if old.State != "succeeded" {
		t.Fatal("old fixture release incomplete")
	}
	updated := app.Manifest
	updated.Components = append([]domain.Component(nil), app.Manifest.Components...)
	updated.Components[0].Image = "registry.test/web@sha256:" + strings.Repeat("b", 64)
	app, err = c.UpdateApplication(ctx, app.ID, updated, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := c.Enqueue(ctx, app.ID, "rollback-new-release")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		_ = c.Advance(ctx, newer.ID)
	}
	newer, _ = c.Store.Deployment(ctx, newer.ID)
	if newer.State != "succeeded" {
		t.Fatal("new fixture release incomplete")
	}
	return c, f, app, old, newer
}

func TestImageRollbackPlansRevalidateAndCreateIdempotentNewRelease(t *testing.T) {
	c, f, app, old, newer := rollbackHistory(t)
	ctx := context.Background()
	before := f.deploys
	plan, err := c.PlanRollback(ctx, app.ID, old.ID, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Instances) != 2 || plan.LatestReleaseID != newer.ID || f.deploys != before {
		t.Fatal("plan mutated provider or lost replicas")
	}
	request := controller.RollbackRequest{ReleaseID: old.ID, PlanHash: plan.PlanHash}
	if _, err = c.EnqueueRollback(ctx, app.ID, "rollback-apply-01", request, app.Version); err == nil {
		t.Fatal("data compatibility acknowledgement not required")
	}
	request.AcknowledgeDataCompatibility = true
	stale := request
	stale.PlanHash = strings.Repeat("0", 64)
	if _, err = c.EnqueueRollback(ctx, app.ID, "rollback-stale-01", stale, app.Version); err != domain.ErrConflict {
		t.Fatal("stale plan accepted", err)
	}
	release, err := c.EnqueueRollback(ctx, app.ID, "rollback-apply-01", request, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := c.EnqueueRollback(ctx, app.ID, "rollback-apply-01", request, app.Version)
	if err != nil || duplicate.ID != release.ID {
		t.Fatal("rollback retry duplicated release", err)
	}
	if release.Manifest.Components[0].Image != old.Manifest.Components[0].Image || release.Steps[0].RollbackFrom != old.ID {
		t.Fatal("rollback snapshot/provenance missing")
	}
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		_ = restarted.Advance(ctx, release.ID)
	}
	done, _ := c.Store.Deployment(ctx, release.ID)
	if done.State != "succeeded" || f.deploys != before+2 || len(f.resources) != 2 {
		t.Fatal("rollback did not restore existing replicas exactly once")
	}
	current, _ := c.Store.Application(ctx, app.ID)
	if current.Version != app.Version || current.Manifest.Components[0].Image != newer.Manifest.Components[0].Image {
		t.Fatal("rollback silently rewrote desired configuration")
	}
	for _, step := range done.Steps {
		if f.resources[step.ResourceID].Image != old.Manifest.Components[0].Image {
			t.Fatal("rollback image was not restored")
		}
	}
}

func TestImageRollbackRejectsIncompatibleSnapshotsAndExecutionDrift(t *testing.T) {
	c, f, app, old, newer := rollbackHistory(t)
	ctx := context.Background()
	before := f.deploys
	if _, err := c.PlanRollback(ctx, app.ID, newer.ID, app.Version); err == nil {
		t.Fatal("latest snapshot offered as rollback")
	}
	if _, err := c.PlanRollback(ctx, app.ID, old.ID, app.Version+1); err != domain.ErrConflict {
		t.Fatal("stale version accepted")
	}
	plan, err := c.PlanRollback(ctx, app.ID, old.ID, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	resource := f.resources[newer.Steps[0].ResourceID]
	resource.Image = "registry.test/unexpected@sha256:" + strings.Repeat("c", 64)
	f.resources[resource.ID] = resource
	if _, err = c.EnqueueRollback(ctx, app.ID, "rollback-drift-01", controller.RollbackRequest{ReleaseID: old.ID, PlanHash: plan.PlanHash, AcknowledgeDataCompatibility: true}, app.Version); err == nil {
		t.Fatal("operator image drift accepted")
	}
	resource.Image = newer.Manifest.Components[0].Image
	f.resources[resource.ID] = resource
	release, err := c.EnqueueRollback(ctx, app.ID, "rollback-drift-02", controller.RollbackRequest{ReleaseID: old.ID, PlanHash: plan.PlanHash, AcknowledgeDataCompatibility: true}, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	resource.Description = "foreign-owner"
	f.resources[resource.ID] = resource
	_ = c.Advance(ctx, release.ID)
	held, _ := c.Store.Deployment(ctx, release.ID)
	if held.State != "attention" || f.deploys != before {
		t.Fatal("queued rollback ignored ownership drift")
	}
	// Mark the synthetic hold terminal to inspect independent configuration checks.
	held.State = "failed"
	if err = c.Store.SaveDeployment(ctx, held); err != nil {
		t.Fatal(err)
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].Env = map[string]string{"SCHEMA_VERSION": "changed"}
	updated, err := c.UpdateApplication(ctx, app.ID, changed, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PlanRollback(ctx, app.ID, old.ID, updated.Version); err == nil {
		t.Fatal("configuration differences accepted by image-only rollback")
	}
}
