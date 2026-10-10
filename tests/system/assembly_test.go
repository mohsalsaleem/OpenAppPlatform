package system_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/coolify"
)

func TestObservedAssemblyReservationsAndManagementBoundary(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{
		"existing-image":  {ID: "existing-image", Name: "Existing image", Image: "nginx:1.27-alpine", Port: 80, ArtifactKind: "image", Status: "running:healthy"},
		"existing-source": {ID: "existing-source", Name: "Source app", Port: 3000, ArtifactKind: "source", Status: "running:healthy"},
	}}
	c, srv := setup(t, f)
	m := domain.Manifest{Name: "assembled", Environment: "staging", TargetID: "fixture", Components: []domain.Component{
		{Name: "web", ResourceID: "existing-image", Image: "nginx:1.27-alpine", Port: 80, Management: "observe"},
		{Name: "api", ResourceID: "existing-source", Port: 3000, Management: "observe"},
	}}
	code, raw := request(t, srv, http.MethodPost, "/api/v1/applications", "", m)
	if code != 201 {
		t.Fatalf("assembly HTTP %d: %s", code, raw)
	}
	var app domain.Application
	json.Unmarshal(raw, &app)
	instances, err := c.Instances(context.Background(), app.ID)
	if err != nil || len(instances) != 2 || instances[0].Status != "running:healthy" {
		t.Fatalf("observed health unavailable: %+v %v", instances, err)
	}
	code, _ = request(t, srv, http.MethodGet, "/api/v1/applications/"+app.ID+"/logs/api", "", nil)
	if code != 200 {
		t.Fatal("source logs unavailable")
	}
	items, err := c.Discover(context.Background(), "fixture")
	if err != nil || len(items) != 2 {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ApplicationID != app.ID {
			t.Fatal("reservation missing")
		}
	}
	m.Name = "duplicate"
	code, _ = request(t, srv, http.MethodPost, "/api/v1/applications", "", m)
	if code != 409 {
		t.Fatalf("duplicate binding HTTP %d", code)
	}
	if _, err = c.Enqueue(context.Background(), app.ID, "observed-deploy"); err == nil {
		t.Fatal("observe deployment allowed")
	}
	if _, err = c.EnqueueRestart(context.Background(), app.ID, "observed-restart", controller.RestartRequest{Component: "api", Ordinal: 1}, app.Version); err == nil {
		t.Fatal("observe restart allowed")
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].Management = ""
	if _, err = c.UpdateApplication(context.Background(), app.ID, changed, app.Version); err == nil {
		t.Fatal("configuration bypassed handoff")
	}
	if _, err = c.EnableManagement(context.Background(), app.ID, "api", app.Version); err == nil {
		t.Fatal("source handoff allowed")
	}
	if _, err = c.EnableManagement(context.Background(), app.ID, "web", app.Version+1); err == nil {
		t.Fatal("stale version allowed")
	}
	changedResource := f.resources["existing-image"]
	changedResource.Port = 81
	f.resources["existing-image"] = changedResource
	if _, err = c.EnableManagement(context.Background(), app.ID, "web", app.Version); err == nil {
		t.Fatal("configuration drift accepted")
	}
	changedResource.Port = 80
	changedResource.Description = "OpenAppPlatform:other:web"
	f.resources["existing-image"] = changedResource
	if _, err = c.EnableManagement(context.Background(), app.ID, "web", app.Version); err == nil {
		t.Fatal("foreign OAP ownership accepted")
	}
	changedResource.Description = ""
	f.resources["existing-image"] = changedResource
	enabled, err := c.EnableManagement(context.Background(), app.ID, "web", app.Version)
	if err != nil || enabled.Version != app.Version+1 || enabled.Manifest.Components[0].Management != "" {
		t.Fatalf("image handoff failed: %v", err)
	}
	if _, err = c.Enqueue(context.Background(), app.ID, "mixed-deploy"); err == nil {
		t.Fatal("mixed observed app deployed")
	}
	var releases int
	c.Store.Pool.QueryRow(context.Background(), "SELECT count(*) FROM oap_deployments").Scan(&releases)
	if f.deploys != 0 || len(f.resources) != 2 || releases != 0 {
		t.Fatal("assembly/handoff mutated provider or created a release")
	}
}

func TestLiveCoolifyObserveOnlyAssembly(t *testing.T) {
	if os.Getenv("OAP_LIVE_COOLIFY") != "1" {
		t.Skip("opt-in read-only Coolify staging verification")
	}
	target := domain.Target{ID: "fixture", Name: "Coolify staging", Operator: "coolify", URL: os.Getenv("COOLIFY_URL"), ProjectID: os.Getenv("COOLIFY_PROJECT_ID"), ServerID: os.Getenv("COOLIFY_SERVER_ID"), Environment: os.Getenv("COOLIFY_ENVIRONMENT")}
	if target.Environment != "staging" || target.ProjectID == "" {
		t.Fatal("explicit staging scope required")
	}
	adapter, err := coolify.New(target, os.Getenv("COOLIFY_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	resources, err := adapter.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := domain.Manifest{Name: "observed-" + domain.NewID()[:8], Environment: target.Environment, TargetID: target.ID}
	before := map[string]operator.Resource{}
	for _, r := range resources {
		if r.ArtifactKind != "image" && r.ArtifactKind != "source" {
			continue
		}
		actual, err := adapter.Inspect(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		m.Components = append(m.Components, domain.Component{Name: fmt.Sprintf("service-%d", len(m.Components)+1), ResourceID: actual.ID, Image: actual.Image, Port: actual.Port, Management: "observe"})
		before[actual.ID] = actual
		if len(m.Components) == 2 {
			break
		}
	}
	if len(m.Components) != 2 {
		t.Fatal("two staging services required")
	}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	live, err := c.Instances(ctx, app.ID)
	if err != nil || len(live) != 2 {
		t.Fatalf("live inspection: %v", err)
	}
	for _, instance := range live {
		if instance.Error != "" || instance.Resource == nil {
			t.Fatal("live observation failed")
		}
		if _, err = adapter.Logs(ctx, instance.ResourceID, 20); err != nil {
			t.Fatal(err)
		}
		after, err := adapter.Inspect(ctx, instance.ResourceID)
		if err != nil {
			t.Fatal(err)
		}
		prior := before[instance.ResourceID]
		if after.Image != prior.Image || after.Description != prior.Description || after.Port != prior.Port || after.URL != prior.URL {
			t.Fatal("operator configuration changed")
		}
	}
	if _, err = c.Enqueue(ctx, app.ID, "live-observed-blocked"); err == nil {
		t.Fatal("observe app deployed")
	}
	t.Log("two existing staging services grouped; health/logs inspected; no release dispatched")
}
