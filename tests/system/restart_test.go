package system_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type restartFake struct {
	*fake
	restarts int
}

func (f *restartFake) Capabilities() operator.Capabilities {
	return operator.Capabilities{Standard: true, Discovery: true, Restart: true, Environment: true, ApplicationDNS: true}
}
func (f *restartFake) Restart(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return fmt.Sprintf("restart-%d", f.restarts), nil
}
func waitRelease(t *testing.T, c *controller.Controller, id string) domain.Deployment {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		d, e := c.Store.Deployment(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(d.State) {
			if d.State != "succeeded" {
				t.Fatalf("release failed %+v", d)
			}
			return d
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("release timed out")
	return domain.Deployment{}
}
func TestRestartIsScopedIdempotentAndDoesNotApplyConfiguration(t *testing.T) {
	f := &restartFake{fake: &fake{resources: map[string]operator.Resource{}}}
	c, srv := setup(t, f)
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.EnqueueRestart(ctx, app.ID, "no-runtime-restart", controller.RestartRequest{Component: "web", Ordinal: 1}, app.Version); e == nil {
		t.Fatal("undeployed restart accepted")
	}
	d, e := c.Enqueue(ctx, app.ID, "initial-release")
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(ctx)
	d = waitRelease(t, c, d.ID)
	next := app.Manifest
	next.Components[0].Image = "nginx:1.28-alpine"
	app, e = c.UpdateApplication(ctx, app.ID, next, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	req := map[string]any{"component": "web", "ordinal": 2, "expectedVersion": app.Version}
	path := "/api/v1/applications/" + app.ID + "/restarts"
	status, raw := request(t, srv, "POST", path, "restart-instance-two", req)
	if status != 202 {
		t.Fatalf("restart %d %s", status, raw)
	}
	var restart domain.Deployment
	json.Unmarshal(raw, &restart)
	status, raw = request(t, srv, "POST", path, "restart-instance-two", req)
	if status != 202 {
		t.Fatal(status)
	}
	var duplicate domain.Deployment
	json.Unmarshal(raw, &duplicate)
	if duplicate.ID != restart.ID {
		t.Fatal("restart retry duplicated operation")
	}
	restart = waitRelease(t, c, restart.ID)
	if len(restart.Steps) != 1 || restart.Steps[0].Action != "restart" || restart.Steps[0].ResourceID != d.Steps[1].ResourceID {
		t.Fatalf("restart escaped scope %+v", restart)
	}
	f.mu.Lock()
	restarts, deploys := f.restarts, f.deploys
	image := f.resources[d.Steps[1].ResourceID].Image
	f.mu.Unlock()
	if restarts != 1 || deploys != 2 || image != "nginx:1.27-alpine" {
		t.Fatalf("restart applied future definition: restart=%d deploy=%d image=%s", restarts, deploys, image)
	}
	req["ordinal"] = 1
	status, _ = request(t, srv, "POST", path, "restart-instance-two", req)
	if status != 409 {
		t.Fatal("idempotency key accepted different instance")
	}
	req["expectedVersion"] = 1
	status, _ = request(t, srv, "POST", path, "stale-restart-request", req)
	if status != 409 {
		t.Fatal("stale restart admitted")
	}
}
func TestUnsupportedRuntimeFeaturesAreRejected(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	m := manifest()
	m.Components[0].Env = map[string]string{"MODE": "preview"}
	if _, e := c.CreateApplication(ctx, m); e == nil {
		t.Fatal("unsupported environment silently accepted")
	}
	m = manifest()
	m.Components[0].Services = map[string]string{"SELF_URL": "web"}
	if _, e := c.CreateApplication(ctx, m); e == nil {
		t.Fatal("unsupported connection silently accepted")
	}
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.EnqueueRestart(ctx, app.ID, "unsupported-restart", controller.RestartRequest{Component: "web", Ordinal: 1}, app.Version); e == nil {
		t.Fatal("unsupported restart accepted")
	}
}

func TestRestartBlocksManagedOwnershipDrift(t *testing.T) {
	f := &restartFake{fake: &fake{resources: map[string]operator.Resource{"bound": {ID: "bound", Description: "another-owner", Status: "running:healthy"}}}}
	c, _ := setup(t, f)
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Store.Bind(ctx, "fixture", "bound", app.ID, "web", 1); e != nil {
		t.Fatal(e)
	}
	d, e := c.EnqueueRestart(ctx, app.ID, "ownership-drift-restart", controller.RestartRequest{Component: "web", Ordinal: 1}, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Advance(ctx, d.ID); e != nil {
		t.Fatal(e)
	}
	d, e = c.Store.Deployment(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if d.State != "attention" || f.restarts != 0 {
		t.Fatal("restart mutated resource after ownership drift", d)
	}
}
