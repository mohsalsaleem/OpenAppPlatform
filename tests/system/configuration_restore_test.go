package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strings"
	"testing"
)

type restoreAdapter struct{ *fake }

func (f restoreAdapter) Capabilities() operator.Capabilities {
	caps := f.fake.Capabilities()
	caps.Environment = true
	return caps
}
func TestConfigurationRestoreReviewIsMaskedVersionedAndDoesNotDeploy(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, restoreAdapter{f})
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	release, err := c.Enqueue(ctx, app.ID, "configuration-recovery-source")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	release = waitRelease(t, c, release.ID)
	c.Jobs.Stop(ctx)
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].Image = "nginx:1.28-alpine"
	changed.Components[0].Port = 8080
	changed.Components[0].Readiness = &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}
	// A private configuration value must not enter review responses.
	changed.Components[0].Env = map[string]string{"PRIVATE_SETTING": "do-not-expose-this-value"}
	updated, err := c.UpdateApplication(ctx, app.ID, changed, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	before := f.deploys
	path := "/api/v1/applications/" + app.ID + "/configuration-restore-plan?releaseId=" + release.ID + "&expectedVersion=2"
	code, raw := request(t, srv, "GET", path, "", nil)
	if code != 200 {
		t.Fatalf("plan %d %s", code, raw)
	}
	if strings.Contains(string(raw), "do-not-expose-this-value") {
		t.Fatal("plan exposed a value")
	}
	var plan controller.ConfigurationRestorePlan
	json.Unmarshal(raw, &plan)
	code, _ = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/configuration-restores", "", map[string]any{"releaseId": release.ID, "planHash": plan.PlanHash, "expectedVersion": updated.Version})
	if code != 422 {
		t.Fatal("missing acknowledgement accepted", code)
	}
	body := map[string]any{"releaseId": release.ID, "planHash": plan.PlanHash, "expectedVersion": updated.Version, "acknowledgeEffects": true}
	staleBody := map[string]any{"releaseId": release.ID, "planHash": strings.Repeat("0", 64), "expectedVersion": updated.Version, "acknowledgeEffects": true}
	code, _ = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/configuration-restores", "", staleBody)
	if code != 409 {
		t.Fatal("unreviewed hash accepted", code)
	}
	target, err := c.Store.Target(ctx, app.Manifest.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	originalTarget := target
	target.Name = "Changed target authority"
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	code, _ = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/configuration-restores", "", body)
	if code != 409 {
		t.Fatal("stale target authority accepted", code)
	}
	if err = c.Store.SaveTarget(ctx, originalTarget); err != nil {
		t.Fatal(err)
	}

	code, raw = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/configuration-restores", "", body)
	if code != 200 {
		t.Fatalf("restore %d %s", code, raw)
	}
	var restored domain.Application
	json.Unmarshal(raw, &restored)
	if restored.Version != 3 || restored.Manifest.Components[0].Port != app.Manifest.Components[0].Port || restored.Manifest.Components[0].Image != "nginx:1.28-alpine" || f.deploys != before {
		t.Fatal("restore deployed or restored images")
	}
	code, _ = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/configuration-restores", "", body)
	if code != 409 {
		t.Fatal("stale retry accepted", code)
	}
	// Cross-application snapshots remain inaccessible.
	other, err := c.CreateApplication(ctx, domain.Manifest{Name: "another-app", Environment: app.Manifest.Environment, TargetID: app.Manifest.TargetID, Components: app.Manifest.Components})
	if err != nil {
		t.Fatal(err)
	}
	code, _ = request(t, srv, "GET", "/api/v1/applications/"+other.ID+"/configuration-restore-plan?releaseId="+release.ID+"&expectedVersion=1", "", nil)
	if code != 404 {
		t.Fatal("foreign snapshot accepted", code)
	}
	// Active releases fence recovery; ordinary updates cannot bypass this review.
	_, err = c.Enqueue(ctx, app.ID, "active-configuration-recovery")
	if err != nil {
		t.Fatal(err)
	}
	code, _ = request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/configuration-restore-plan?releaseId="+release.ID+"&expectedVersion=3", "", nil)
	if code != 409 {
		t.Fatal("active operation did not fence recovery", code)
	}
}
