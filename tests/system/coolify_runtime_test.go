package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/coolify"
)

const receiverImage = "mendhak/http-https-echo@sha256:f55000d9196bd3c853d384af7315f509d21ffb85de315c26e9874033b9f83e15"

func coolifyTestRequest(t *testing.T, method, path string, body any) []byte {
	t.Helper()
	encoded, _ := json.Marshal(body)
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(encoded)
	}
	request, e := http.NewRequest(method, os.Getenv("COOLIFY_URL")+"/api/v1"+path, input)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv("COOLIFY_TOKEN"))
	request.Header.Set("Content-Type", "application/json")
	response, e := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if e != nil {
		t.Fatal("Coolify fixture request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("Coolify fixture %s %s HTTP %d", method, path, response.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func waitCoolifyRelease(t *testing.T, cID string, load func(string) (domain.Deployment, error)) domain.Deployment {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		d, e := load(cID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(d.State) {
			if d.State != "succeeded" {
				t.Fatalf("staging operation failed: %+v", d)
			}
			return d
		}
		time.Sleep(time.Second)
	}
	t.Fatal("staging operation deadline exceeded")
	return domain.Deployment{}
}
func waitFixtureHTTP(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		response, e := (&http.Client{Timeout: 10 * time.Second}).Get(endpoint)
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("fixture HTTP did not become ready")
}
func TestLiveCoolifyRuntimeConfiguration(t *testing.T) {
	if os.Getenv("OAP_LIVE_COOLIFY") != "1" {
		t.Skip("opt-in isolated Coolify staging verification")
	}
	target := domain.Target{ID: "fixture", Name: "Coolify runtime staging", Operator: "coolify", URL: os.Getenv("COOLIFY_URL"), ProjectID: os.Getenv("COOLIFY_PROJECT_ID"), ServerID: os.Getenv("COOLIFY_SERVER_ID"), Environment: os.Getenv("COOLIFY_ENVIRONMENT")}
	if target.Environment != "staging" || target.ProjectID == "" {
		t.Fatal("explicit staging target required")
	}
	adapter, e := coolify.New(target, os.Getenv("COOLIFY_TOKEN"))
	if e != nil {
		t.Fatal(e)
	}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	if e = c.Store.SaveTarget(ctx, target); e != nil {
		t.Fatal(e)
	}
	m := domain.Manifest{Name: "live-runtime-" + domain.NewID()[:8], TargetID: target.ID, Environment: "staging", Components: []domain.Component{
		{Name: "source", Image: os.Getenv("COOLIFY_TEST_IMAGE"), Port: 8025, Instances: 1, Env: map[string]string{"MP_LABEL": "oap-runtime-v1", "MP_MAX_MESSAGES": "3", "REMOVE_ME": "literal-$UNCHANGED", "MP_DISABLE_VERSION_CHECK": "true", "MP_SEND_API_AUTH_ACCEPT_ANY": "true", "MP_VERBOSE": "true"}},
		{Name: "receiver", Image: receiverImage, Port: 8080, Instances: 1, Env: map[string]string{"LOG_IGNORE_PATH": "^/$", "LOG_WITHOUT_NEWLINE": "true"}},
	}}
	app, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	receiver, e := adapter.Ensure(ctx, operator.Spec{Name: domain.ResourceName(app.ID, "receiver", 1), Ownership: "OpenAppPlatform:" + app.ID + ":receiver", Component: app.Manifest.Components[1], Variables: c.Store.VariableJournal(target.ID, app.ID, "receiver", 1)})
	if e != nil {
		t.Fatal(e)
	}
	coolifyTestRequest(t, "PATCH", "/applications/"+receiver.ID, map[string]any{"health_check_enabled": true, "health_check_path": "/", "health_check_port": "8080"})
	t.Logf("retained receiver fixture resource=%s", receiver.ID)
	d, e := c.Enqueue(ctx, app.ID, "runtime-first-release")
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(ctx)
	load := func(id string) (domain.Deployment, error) { return c.Store.Deployment(ctx, id) }
	d = waitCoolifyRelease(t, d.ID, load)
	base, e := adapter.Inspect(ctx, os.Getenv("COOLIFY_TEST_RESOURCE_ID"))
	if e != nil {
		t.Fatal(e)
	}
	baseURL, e := url.Parse(strings.Split(base.URL, ",")[0])
	if e != nil {
		t.Fatal(e)
	}
	suffix := strings.TrimPrefix(baseURL.Hostname(), base.ID+".")
	if suffix == baseURL.Hostname() {
		t.Fatal("fixture wildcard domain cannot be resolved")
	}
	endpoints := map[string]string{}
	for _, step := range d.Steps {
		resource, e := adapter.Inspect(ctx, step.ResourceID)
		if e != nil {
			t.Fatal(e)
		}
		if resource.Description != "OpenAppPlatform:"+app.ID+":"+step.Component {
			t.Fatal("fixture resource ownership mismatch")
		}
		endpoint := "http://" + resource.ID + "." + suffix
		coolifyTestRequest(t, "PATCH", "/applications/"+resource.ID, map[string]any{"domains": endpoint, "is_force_https_enabled": false})
		endpoints[step.Component] = endpoint
		t.Logf("retained runtime fixture component=%s resource=%s url=%s", step.Component, resource.ID, endpoint)
	}
	sourceID := d.Steps[0].ResourceID
	receiverID := d.Steps[1].ResourceID
	coolifyTestRequest(t, "POST", "/applications/"+sourceID+"/envs", map[string]any{"key": "OPERATOR_KEEP", "value": "operator-owned", "is_literal": true, "is_runtime": true, "is_buildtime": false})
	current, e := c.Store.Application(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	current.Manifest.Components[0].Services = map[string]string{"MP_WEBHOOK_URL": "receiver"}
	current.Manifest.Components[0].ServiceEndpoints = map[string]string{"receiver": endpoints["receiver"] + "/oap-hook"}
	current, e = c.UpdateApplication(ctx, app.ID, current.Manifest, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	connected, e := c.EnqueueVersion(ctx, app.ID, "runtime-connect-release", nil, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	waitCoolifyRelease(t, connected.ID, load)
	waitFixtureHTTP(t, endpoints["source"])
	waitFixtureHTTP(t, endpoints["receiver"])
	raw := coolifyTestRequest(t, "GET", "/applications/"+sourceID+"/envs", nil)
	var before []struct {
		UUID, Key, Value string
		Preview          bool `json:"is_preview"`
	}
	json.Unmarshal(raw, &before)
	keepUUID := ""
	for _, row := range before {
		if row.Preview {
			continue
		}
		if row.Key == "OPERATOR_KEEP" {
			keepUUID = row.UUID
		}
		if row.Key == "REMOVE_ME" && row.Value != "literal-$UNCHANGED" {
			t.Fatal("literal configuration changed")
		}
	}
	if keepUUID == "" {
		t.Fatal("operator-owned variable missing")
	}
	nonce := "oap-webhook-" + domain.NewID()
	payload := map[string]any{"From": map[string]string{"Email": "fixture@example.invalid"}, "To": []map[string]string{{"Email": "capture@example.invalid"}}, "Subject": nonce, "Text": "Synthetic local Mailpit capture; no external relay."}
	encoded, _ := json.Marshal(payload)
	response, e := (&http.Client{Timeout: 15 * time.Second}).Post(endpoints["source"]+"/api/v1/send", "application/json", bytes.NewReader(encoded))
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("fixture capture request failed", response.StatusCode)
	}
	deadline := time.Now().Add(30 * time.Second)
	received := false
	for time.Now().Before(deadline) {
		logs, e := adapter.Logs(ctx, receiverID, 200)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(logs, nonce) {
			received = true
			break
		}
		time.Sleep(time.Second)
	}
	if !received {
		t.Fatal("configured connection did not deliver the synthetic webhook to the receiver")
	}
	delete(current.Manifest.Components[0].Env, "REMOVE_ME")
	current.Manifest.Components[0].Env["MP_LABEL"] = "oap-runtime-v2"
	current.Manifest.Components[0].Env["MP_MAX_MESSAGES"] = "5"
	current, e = c.UpdateApplication(ctx, app.ID, current.Manifest, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	updated, e := c.EnqueueVersion(ctx, app.ID, "runtime-update-release", nil, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	waitCoolifyRelease(t, updated.ID, load)
	raw = coolifyTestRequest(t, "GET", "/applications/"+sourceID+"/envs", nil)
	var after []struct {
		UUID, Key, Value string
		Preview          bool `json:"is_preview"`
	}
	json.Unmarshal(raw, &after)
	labelOK, keepOK := false, false
	for _, row := range after {
		if row.Preview {
			continue
		}
		if row.Key == "REMOVE_ME" {
			t.Fatal("removed managed variable still exists")
		}
		if row.Key == "MP_LABEL" {
			labelOK = row.Value == "oap-runtime-v2"
		}
		if row.Key == "OPERATOR_KEEP" {
			keepOK = row.UUID == keepUUID && row.Value == "operator-owned"
		}
	}
	if !labelOK || !keepOK {
		t.Fatal("update failed or unrelated operator variable changed")
	}
	t.Logf("verified app=%s variable add/update/removal, literal value, unrelated-variable preservation, and component webhook delivery", app.ID)
}
