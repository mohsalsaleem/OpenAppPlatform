package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"strings"
	"testing"
)

type inputAdapter struct {
	*fake
	value   string
	ensures int
}

func (f *inputAdapter) Capabilities() operator.Capabilities {
	caps := f.fake.Capabilities()
	caps.Environment = true
	return caps
}
func (f *inputAdapter) Ensure(ctx context.Context, s operator.Spec) (operator.Resource, error) {
	f.ensures++
	f.value = s.Component.Env["TOKEN"]
	return f.fake.Ensure(ctx, s)
}
func (f *inputAdapter) Logs(context.Context, string, int) (string, error) {
	return "TOKEN=" + f.value + " ordinary", nil
}
func inputGrants(t *testing.T, app string, value string) *runtimeinputs.Inputs {
	t.Helper()
	raw, _ := json.Marshal([]runtimeinputs.Binding{{Reference: "token-v1", TargetID: "fixture", ApplicationID: app, Component: "web", Variable: "TOKEN", ValueEnv: "OAP_SECRET_TOKEN_V1"}})
	inputs, err := runtimeinputs.Parse(raw, func(string) string { return value })
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}
func TestRuntimeInputAdmissionExecutionRecoveryAndLogRedaction(t *testing.T) {
	ctx := context.Background()
	f := &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}}
	c, srv := setup(t, f)
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].SecretRefs = map[string]string{"TOKEN": "token-v1"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, inputs := range []*runtimeinputs.Inputs{nil, inputGrants(t, strings.Repeat("b", 32), "private-test-value"), inputGrants(t, app.ID, "")} {
		c.RuntimeInputs = inputs
		if _, err = c.Enqueue(ctx, app.ID, "missing-input-grant"); err == nil {
			t.Fatal("unauthorized secret release admitted")
		}
		var count int
		if err = c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_deployments WHERE application_id=$1", app.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 || f.ensures != 0 || f.deploys != 0 {
			t.Fatal("missing input caused release/provider mutation")
		}
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "private-test-value")
	d, err := c.Enqueue(ctx, app.ID, "authorized-input-release")
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the controller before execution with the same pinned inputs.
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	restarted.RuntimeInputs = inputGrants(t, app.ID, "private-test-value")
	if err = restarted.Advance(ctx, d.ID); err == nil {
		t.Fatal("expected observation snooze")
	}
	prepared, _ := c.Store.Deployment(ctx, d.ID)
	if prepared.Steps[0].Phase != "prepared" || f.value != "private-test-value" || f.ensures != 1 {
		t.Fatal("secret was not resolved at preparation")
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "changed-test-value")
	if err = c.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	blocked, _ := c.Store.Deployment(ctx, d.ID)
	if blocked.State != "attention" || f.deploys != 0 {
		t.Fatal("changed secret dispatched")
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "private-test-value")
	if _, err = c.Recover(ctx, d.ID, controller.Recovery{Component: "web", Ordinal: 1, RetryPreparation: true, ExpectedUpdatedAt: blocked.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4; n++ {
		c.Advance(ctx, d.ID)
	}
	recovered, err := c.Store.Deployment(ctx, d.ID)
	if err != nil || recovered.State != "succeeded" || f.ensures != 1 || f.deploys != 1 {
		t.Fatal("restored binding did not recover the same resource exactly once")
	}
	for _, path := range []string{"/api/v1/applications/" + app.ID, "/api/v1/deployments/" + d.ID, "/api/v1/applications/" + app.ID + "/logs/web"} {
		status, raw := request(t, srv, "GET", path, "", nil)
		if status != 200 || strings.Contains(string(raw), "private-test-value") || strings.Contains(string(raw), "fingerprint") {
			t.Fatalf("unsafe API response status=%d", status)
		}
		if strings.Contains(path, "/logs/") && !strings.Contains(string(raw), "[REDACTED]") {
			t.Fatal("log secret not masked")
		}
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "changed-test-value")
	status, raw := request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/logs/web", "", nil)
	if status != 502 || strings.Contains(string(raw), "private-test-value") {
		t.Fatal("historical logs did not fail closed")
	}
	if _, err = c.RedactRuntimeLogs(ctx, app.ID, "private-test-value"); err == nil {
		t.Fatal("SSE log path did not fail closed")
	}
	// Recovery metadata remains private and contains no plaintext value.
	var hash string
	if err = c.Store.Pool.QueryRow(ctx, "SELECT fingerprint FROM oap_runtime_input_pins WHERE deployment_id=$1", d.ID).Scan(&hash); err != nil || len(hash) != 64 {
		t.Fatal("missing recovery pin")
	}
}

func TestRuntimeInputChangedBeforePreparationDoesNotCreateResource(t *testing.T) {
	ctx := context.Background()
	f := &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}}
	c, _ := setup(t, f)
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].SecretRefs = map[string]string{"TOKEN": "token-v1"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "private-test-value")
	d, err := c.Enqueue(ctx, app.ID, "changed-before-preparation")
	if err != nil {
		t.Fatal(err)
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "changed-test-value")
	if err = c.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	if d.State != "attention" || f.ensures != 0 || f.deploys != 0 {
		t.Fatal("changed binding caused a provider mutation")
	}
}
