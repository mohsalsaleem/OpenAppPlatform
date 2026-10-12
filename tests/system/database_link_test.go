package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"testing"
)

type databaseFake struct{ *fake }

func (f databaseFake) Capabilities() operator.Capabilities {
	c := f.fake.Capabilities()
	c.DatabaseLinks = true
	return c
}
func TestDatabaseLinkVersionReservationAndAuthority(t *testing.T) {
	f := databaseFake{&fake{resources: map[string]operator.Resource{"db": {ID: "db", Name: "Existing PostgreSQL", Image: "postgres:17", ArtifactKind: "database", Status: "running:healthy"}}}}
	c, srv := setup(t, f)
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, domain.Manifest{Name: "linked", Environment: "staging", TargetID: "fixture", Components: []domain.Component{{Name: "api", Image: "api:1", Port: 8080}}})
	if err != nil {
		t.Fatal(err)
	}
	v := controller.DatabaseLinkRequest{Name: "database", ResourceID: "db", Endpoint: "postgresql://db:5432/application", ExpectedVersion: app.Version, Acknowledge: true}
	bad := v
	bad.Endpoint = "postgresql://private:password@db/application"
	if _, err = c.LinkDatabase(ctx, app.ID, bad); err == nil {
		t.Fatal("credentials accepted as metadata")
	}
	code, body := request(t, srv, http.MethodPost, "/api/v1/applications/"+app.ID+"/databases", "", v)
	if code != 200 {
		t.Fatalf("link: %d %s", code, body)
	}
	json.Unmarshal(body, &app)
	if app.Version != 2 || len(app.Manifest.Components) != 2 {
		t.Fatal("link did not advance definition")
	}
	if _, err = c.LinkDatabase(ctx, app.ID, v); err != domain.ErrConflict {
		t.Fatal("stale link accepted")
	}
	if _, err = c.EnqueueRestart(ctx, app.ID, "database-restart", controller.RestartRequest{Component: "database", Ordinal: 1}, app.Version); err == nil {
		t.Fatal("database restart allowed")
	}
	if _, err = c.EnableManagement(ctx, app.ID, "database", app.Version); err == nil {
		t.Fatal("database handoff allowed")
	}
	if _, err = c.EnqueueSelected(ctx, app.ID, "database-deploy", nil, app.Version, []string{"database"}); err == nil {
		t.Fatal("database deploy allowed")
	}
	app.Manifest.Components[0].DependsOn = []string{"database"}
	app, err = c.UpdateApplication(ctx, app.ID, app.Manifest, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.EnqueueVersion(ctx, app.ID, "managed-api-release", nil, app.Version)
	if err != nil || len(d.Steps) != 1 || d.Steps[0].Component != "api" {
		t.Fatal("default release selected database", err)
	}
	before := f.deploys
	f.resources["db"] = operator.Resource{ID: "db", ArtifactKind: "database", Status: "exited"}
	if err = c.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	if d.State != "attention" || f.deploys != before {
		t.Fatal("unhealthy dependency dispatched a workload")
	}
	instances, err := c.Instances(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if instances[1].Status != "exited" {
		t.Fatal("database failure hidden")
	}
	c.RequireIdentity = true
	agent := domain.WithPrincipal(ctx, domain.Principal{Role: "owner", Kind: "agent", Scope: "operate", WorkspaceID: "default"})
	if _, err = c.LinkDatabase(agent, app.ID, v); err == nil {
		t.Fatal("agent changed database membership")
	}
}
