package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/mcp"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeDockerHTTPProbeOverridesImageFailsAndReturnsToImageCheck(t *testing.T) {
	socket, image := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("isolated Docker fixture required")
	}
	ctx := context.Background()
	target := domain.Target{ID: "http-probes", Name: "HTTP Probes", Operator: "docker", Environment: "staging", URL: "unix://" + socket, Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s := testStore(t)
	if err = s.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	c, err := controller.New(ctx, s, func(domain.Target) (operator.Adapter, error) { return adapter, nil })
	if err != nil {
		t.Fatal(err)
	}
	c.PollInterval = 20 * time.Millisecond
	m := domain.Manifest{Name: "native-http-probes", Environment: "staging", TargetID: target.ID, Components: []domain.Component{{Name: "web", Kind: "web", Image: image, Port: 8080, HostPort: port, Instances: 1, Strategy: "standard", HealthCheck: &domain.HealthCheck{Mode: "http", Path: "/health/custom", IntervalSeconds: 1, TimeoutSeconds: 1, Retries: 1}}}}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	// The external named agent owns diagnosis/correction through the public
	// contracts; fixture creation and the grant remain explicit owner setup.
	c.RequireIdentity = true
	svc := access.Service{Pool: s.Pool}
	owner, err := svc.Register(ctx, "diagnose-native@example.invalid", "Owner", "diagnose-native-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := svc.IssueRestricted(ctx, owner, "Diagnosis agent", "operate", app.ID, time.Hour, domain.AgentPermissions{Actions: []string{"configure", "deploy", "recover"}, TargetID: target.ID, Environment: target.Environment})
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer api.Close()
	invoke := func(command string, flags []string, body any, want int) cli.Result {
		t.Helper()
		var in, out bytes.Buffer
		if body != nil {
			json.NewEncoder(&in).Encode(body)
		}
		args := append([]string{"--url", api.URL}, flags...)
		if body != nil {
			args = append(args, "--file", "-")
		}
		args = append(args, command)
		code := cli.Run(ctx, args, func(key string) string {
			if key == "OAP_API_TOKEN" {
				return token
			}
			return ""
		}, &in, &out)
		var result cli.Result
		if json.Unmarshal(out.Bytes(), &result) != nil || code != want {
			t.Fatalf("agent %s exit %d expected %d", command, code, want)
		}
		return result
	}
	engine := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	imageResponse, err := engine.Get("http://docker/v1.45/images/" + image + "/json")
	if err != nil {
		t.Fatal(err)
	}
	var imageInfo struct {
		ID string `json:"Id"`
	}
	json.NewDecoder(imageResponse.Body).Decode(&imageInfo)
	imageResponse.Body.Close()
	if !strings.HasPrefix(imageInfo.ID, "sha256:") {
		t.Fatal("immutable fixture image missing")
	}
	act := func(request controller.ActionRequest) store.ActionResult {
		t.Helper()
		created := invoke("action-plan", nil, request, 0)
		raw, _ := json.Marshal(created.Data)
		var plan store.ActionPlan
		if json.Unmarshal(raw, &plan) != nil {
			t.Fatal("invalid plan")
		}
		executed := invoke("execute-action", []string{"--plan", plan.ID}, map[string]any{}, 0)
		raw, _ = json.Marshal(executed.Data)
		json.Unmarshal(raw, &plan)
		if plan.Result == nil {
			t.Fatal("missing action receipt")
		}
		return *plan.Result
	}
	deploy := func() domain.Deployment {
		t.Helper()
		receipt := act(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: app.Version, Images: map[string]string{"web": imageInfo.ID}})
		d, e := s.Deployment(ctx, receipt.OperationID)
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	incident := func() controller.Incident {
		t.Helper()
		result := invoke("incident", []string{"--app", app.ID, "--component", "web"}, nil, 0)
		raw, _ := json.Marshal(result.Data)
		var evidence controller.Incident
		if json.Unmarshal(raw, &evidence) != nil {
			t.Fatal("invalid incident")
		}
		return evidence
	}
	mcpIncident := func() controller.Incident {
		t.Helper()
		rpc, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "get_incident_context", "arguments": map[string]string{"applicationId": app.ID, "component": "web"}}})
		var out bytes.Buffer
		server := mcp.Server{URL: api.URL, Token: token, Operate: true}
		if err := server.Serve(ctx, bytes.NewReader(append(rpc, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal(out.Bytes(), &reply) != nil || reply.Result.IsError || len(reply.Result.Content) != 1 {
			t.Fatal("MCP incident failed")
		}
		var evidence controller.Incident
		if json.Unmarshal([]byte(reply.Result.Content[0].Text), &evidence) != nil {
			t.Fatal("MCP incident invalid")
		}
		return evidence
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	release := deploy()
	release = waitRelease(t, c, release.ID)
	defer adapter.Stop(ctx, release.Steps[0].ResourceID)
	if err = adapter.CheckHealthCheck(ctx, release.Steps[0].ResourceID, release.Manifest.Components[0], false); err != nil {
		t.Fatal(err)
	}
	// Only the check changes. The image's own check stays healthy, so failure proves
	// the configured endpoint overrides it rather than merely inheriting image health.
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	copyProbe := *changed.Components[0].HealthCheck
	copyProbe.Path = "/health/fail"
	changed.Components[0].HealthCheck = &copyProbe
	act(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: app.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: changed}})
	app, err = s.Application(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	broken := deploy()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		broken, _ = c.Store.Deployment(ctx, broken.ID)
		if domain.Terminal(broken.State) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if broken.State != "failed" || broken.Steps[0].RemoteDeploymentID == release.Steps[0].RemoteDeploymentID {
		t.Fatalf("HTTP failure was not real runtime replacement: %+v", broken)
	}
	if err = adapter.CheckHealthCheck(ctx, broken.Steps[0].ResourceID, broken.Manifest.Components[0], false); err != nil {
		t.Fatal(err)
	}
	failedEvidence := incident()
	toolEvidence := mcpIncident()
	if failedEvidence.State != "issue" || toolEvidence.State != "issue" || failedEvidence.Operations[0].ID != broken.ID || toolEvidence.Operations[0].ID != broken.ID {
		t.Fatal("agent did not observe failed release")
	}
	if failedEvidence.Logs.Status != "sampled" || !failedEvidence.UntrustedEvidence || failedEvidence.ApplicationBehaviorVerified {
		t.Fatal("incident evidence contract failed")
	}
	changedCheck := false
	if failedEvidence.Comparison != nil {
		for _, change := range failedEvidence.Comparison.Changes {
			if change.Component == "web" && change.Field == "native health check" {
				changedCheck = true
			}
		}
	}
	if !changedCheck {
		t.Fatal("release change omitted the failing health check")
	}
	allowedFix := false
	for _, next := range failedEvidence.NextActions {
		if next.Action == "configure" && next.Allowed {
			allowedFix = true
		}
	}
	if !allowedFix {
		t.Fatal("authorized correction missing")
	}
	// An old view cannot authorize a change to the newly saved definition.
	invoke("action-plan", nil, controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: release.DefinitionVersion, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: m}}, 4)
	changed = app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].HealthCheck = &domain.HealthCheck{Mode: "image"}
	act(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: app.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: changed}})
	app, err = s.Application(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := deploy()
	restored = waitRelease(t, c, restored.ID)
	if err = adapter.CheckHealthCheck(ctx, restored.Steps[0].ResourceID, restored.Manifest.Components[0], false); err != nil {
		t.Fatal(err)
	}
	invoke("wait", []string{"--app", app.ID, "--operation", restored.ID}, nil, 0)
	verified := incident()
	if verified.State != "observed_healthy" || verified.ApplicationBehaviorVerified {
		t.Fatal("corrected release health/behavior classification failed", verified.State)
	}
	resource, err := adapter.Inspect(ctx, restored.Steps[0].ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(resource.URL + "/health/custom")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(body) != "ready" {
		t.Fatal("external behavior verification failed")
	}
	response, err = http.Get(resource.URL)
	if err != nil {
		t.Fatal(err)
	}
	var identity map[string]string
	json.NewDecoder(response.Body).Decode(&identity)
	response.Body.Close()
	if response.StatusCode != 200 || identity["version"] != "v1" || identity["boot"] == "" {
		t.Fatal("fixture identity verification failed")
	}
	if directory := os.Getenv("OAP_TEST_REPORT_DIR"); directory != "" {
		receipt, _ := json.MarshalIndent(map[string]any{"schemaVersion": 1, "status": "passed", "applicationId": app.ID, "agent": "named app-scoped agent", "failureOperationId": broken.ID, "correctionOperationId": restored.ID, "diagnosisCoverage": []string{"current runtime health", "release health-check change", "CLI/API/MCP parity", "selected redacted logs"}, "applicationBehavior": map[string]any{"verified": true, "checks": []string{"GET /health/custom returns 200 ready", "GET / returns fixture v1 and nonempty boot identity"}}, "dataOperations": "none", "snapshotState": verified.State}, "", "  ")
		if err := os.WriteFile(filepath.Join(directory, "incident-acceptance.json"), append(receipt, '\n'), 0600); err != nil {
			t.Fatal("acceptance receipt write failed")
		}
	}
	if restored.Steps[0].ResourceID != release.Steps[0].ResourceID {
		t.Fatal("stable binding changed")
	}
}
