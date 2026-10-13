package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/riverqueue/river"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/mcp"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
)

func TestM2ScopedRemoteDiagnosticsPlansPauseAndRecovery(t *testing.T) {
	ctx := context.Background()
	f := &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}}
	c, _ := setup(t, f)
	c.RequireIdentity = true
	svc := access.Service{Pool: c.Store.Pool}
	user, err := svc.Register(ctx, "m2@example.invalid", "Owner", "m2-test-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.Issue(ctx, user, "session", "Owner", "operate", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].Image = "example/api@sha256:" + strings.Repeat("a", 64)
	m.Components[0].Env = map[string]string{"PRIVATE": "m2-context-secret"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	m.Name = "sibling"
	other, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	read, err := svc.IssueRestricted(ctx, user, "Reader", "read", app.ID, time.Hour, domain.AgentPermissions{Actions: []string{}, TargetID: "fixture", Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := svc.IssueRestricted(ctx, user, "Deployer", "operate", app.ID, time.Hour, domain.AgentPermissions{Actions: []string{"deploy"}, TargetID: "fixture", Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&httpapi.Server{Controller: c, RemoteMCP: true}).Handler())
	defer server.Close()
	call := func(method, path, token string, body any) (int, []byte) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("X-OAP-CSRF", "1")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		if token == "" {
			req.AddCookie(&http.Cookie{Name: "oap-session", Value: session})
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, data
	}
	root := "/api/v1/applications/" + app.ID
	for _, credential := range []string{read, actor} {
		status, projection := call("GET", root+"/capabilities", credential, nil)
		var capabilities struct {
			Actions map[string]map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"actions"`
		}
		if status != 200 || json.Unmarshal(projection, &capabilities) != nil {
			t.Fatal("scoped capabilities unavailable")
		}
		if capabilities.Actions["web"]["deploy"].Allowed != (credential == actor) {
			t.Fatal("capabilities ignored action grant")
		}
		status, _ = call("GET", "/api/v1/applications/"+other.ID+"/capabilities", credential, nil)
		if status != 404 {
			t.Fatal("capabilities escaped app scope")
		}
	}
	rpc := func(name, id string) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": map[string]string{"applicationId": id}}}
	}
	// Real stdio bridge uses the same owner-mode API boundary as remote inspection.
	var output bytes.Buffer
	bridge := mcp.Server{URL: server.URL, Token: read}
	input, _ := json.Marshal(rpc("get_diagnostic_context", app.ID))
	if err = bridge.Serve(ctx, bytes.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "missingEvidence") || strings.Contains(output.String(), "m2-context-secret") {
		t.Fatal("missing or private diagnostic context")
	}
	for _, path := range []string{"/api/v1/applications/" + other.ID + "/diagnostics", "/api/v1/access/agent-control", "/api/v1/control-database"} {
		code, _ := call("GET", path, read, nil)
		if code != 404 && code != 403 {
			t.Fatal("reader escaped scope", path, code)
		}
	}
	code, data := call("POST", root+"/mcp", read, rpc("get_diagnostic_context", app.ID))
	if code != 200 || !bytes.Contains(data, []byte("untrustedEvidence")) || bytes.Contains(data, []byte("m2-context-secret")) {
		t.Fatal("remote diagnostic failed", code)
	}
	code, data = call("POST", root+"/mcp", read, rpc("get_diagnostic_context", other.ID))
	if code != 200 || !bytes.Contains(data, []byte("isError")) {
		t.Fatal("remote sibling context admitted")
	}
	code, data = call("POST", root+"/mcp", read, rpc("deploy_application", app.ID))
	if code != 200 || !bytes.Contains(data, []byte("isError")) {
		t.Fatal("remote read transport mutated")
	}
	code, _ = call("GET", root+"/mcp", read, nil)
	if code != 405 {
		t.Fatal("unexpected streaming transport")
	}
	// Read credential cannot create a lifecycle plan.
	body := map[string]any{"expectedVersion": app.Version, "components": []string{app.Manifest.Components[0].Name}}
	code, _ = call("POST", root+"/plans", read, body)
	if code != 403 {
		t.Fatal("read agent planned mutation")
	}
	create := func() controller.DeploymentPlan {
		code, data := call("POST", root+"/plans", actor, body)
		if code != 201 {
			t.Fatalf("create plan %d %s", code, data)
		}
		var v controller.DeploymentPlan
		if json.Unmarshal(data, &v) != nil {
			t.Fatal("bad plan")
		}
		return v
	}
	principal, _ := svc.Principal(ctx, actor)
	for _, images := range []map[string]string{{app.Manifest.Components[0].Name: "example/api:latest"}, {"outside": "example/api@sha256:" + strings.Repeat("a", 64)}} {
		if _, e := c.CreateDeploymentPlan(domain.WithPrincipal(ctx, principal), app.ID, app.Version, []string{app.Manifest.Components[0].Name}, images); e == nil {
			t.Fatal("unsafe plan admitted")
		}
	}
	// The ordinary release API cannot occupy a plan's idempotency namespace.
	if _, e := c.EnqueueVersion(domain.WithPrincipal(ctx, principal), app.ID, "plan:reserved", nil, app.Version); e == nil {
		t.Fatal("reserved plan namespace accepted")
	}
	plan := create()
	code, _ = call("POST", root+"/plans/"+plan.ID+"/execute", actor, map[string]any{})
	if code != 409 {
		t.Fatal("unapproved plan executed", code)
	}
	code, _ = call("POST", root+"/plans/"+plan.ID+"/approve", actor, map[string]string{"planHash": plan.PlanHash})
	if code != 403 {
		t.Fatal("agent self approved")
	}
	code, _ = call("POST", root+"/plans/"+plan.ID+"/approve", "", map[string]string{"planHash": "stale"})
	if code != 409 {
		t.Fatal("wrong hash approved")
	}
	code, _ = call("POST", root+"/plans/"+plan.ID+"/approve", "", map[string]string{"planHash": plan.PlanHash})
	if code != 200 {
		t.Fatal("owner approval denied", code)
	}
	another, e := svc.IssueRestricted(ctx, user, "Other Deployer", "operate", app.ID, time.Hour, domain.AgentPermissions{Actions: []string{"deploy"}, TargetID: "fixture", Environment: "staging"})
	if e != nil {
		t.Fatal(e)
	}
	code, _ = call("POST", root+"/plans/"+plan.ID+"/execute", another, map[string]any{})
	if code != 403 {
		t.Fatal("another creator used approval")
	}
	code, data = call("POST", root+"/plans/"+plan.ID+"/execute", actor, map[string]any{})
	if code != 202 {
		t.Fatalf("execute %d %s", code, data)
	}
	var release domain.Deployment
	json.Unmarshal(data, &release)
	code, data = call("POST", root+"/plans/"+plan.ID+"/execute", actor, map[string]any{})
	var retry domain.Deployment
	json.Unmarshal(data, &retry)
	if code != 202 || retry.ID != release.ID {
		t.Fatal("plan duplicate retry changed operation")
	}
	// Persistent emergency pause also fences queued work, not just HTTP admission.
	control, err := svc.Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, _ = call("PUT", "/api/v1/access/agent-control", "", map[string]any{"enabled": false, "expectedVersion": control.Version, "reason": "Temporary acceptance pause"})
	if code != 200 {
		t.Fatal("owner pause denied", code)
	}
	code, _ = call("POST", root+"/mcp", read, rpc("get_diagnostic_context", app.ID))
	if code != 401 {
		t.Fatal("paused agent still accessed remote context")
	}
	if err = c.Advance(ctx, release.ID); err != nil {
		t.Fatal(err)
	}
	held, err := c.Store.Deployment(ctx, release.ID)
	if err != nil || held.State != "attention" || f.deploys != 0 {
		t.Fatal("pause did not hold queued operation", err)
	}
	code, _ = call("GET", "/api/v1/access/agent-control", "", nil)
	if code != 200 {
		t.Fatal("pause locked out owner")
	}
	control, _ = svc.Control(ctx)
	if _, err = svc.UpdateControl(ctx, true, control.Version, ""); err != nil {
		t.Fatal(err)
	}
	code, _ = call("POST", "/api/v1/deployments/"+release.ID+"/recover", "", controller.Recovery{Component: held.Steps[0].Component, Ordinal: held.Steps[0].Ordinal, RetryPreparation: true, ExpectedUpdatedAt: held.UpdatedAt})
	if code != 200 {
		t.Fatal("owner could not recover paused pre-dispatch work", code)
	}
	finish := func(id string) {
		for range 15 {
			var snooze *river.JobSnoozeError
			if e := c.Advance(ctx, id); e != nil && !errors.As(e, &snooze) {
				t.Fatal(e)
			}
			v, e := c.Store.Deployment(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if v.State == "succeeded" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("authorized deployment did not complete")
	}
	finish(release.ID)
	live := create()
	code, _ = call("POST", root+"/plans/"+live.ID+"/approve", "", map[string]string{"planHash": live.PlanHash})
	if code != 200 {
		t.Fatal("live approval denied")
	}
	code, data = call("POST", root+"/plans/"+live.ID+"/execute", actor, map[string]any{})
	if code != 202 {
		t.Fatal("live execution denied", code)
	}
	var liveRelease domain.Deployment
	json.Unmarshal(data, &liveRelease)
	finish(liveRelease.ID)
	code, data = call("POST", root+"/plans/"+live.ID+"/execute", actor, map[string]any{})
	if code != 202 {
		t.Fatal("completed retry failed")
	}
	json.Unmarshal(data, &retry)
	if retry.ID != liveRelease.ID || f.deploys != 2 {
		t.Fatal("authorized agent duplicated dispatch")
	}
	// Stale and expired approvals are conservative conflicts even after resume.
	stale := create()
	c.Store.Pool.Exec(ctx, "UPDATE oap_applications SET version=version+1 WHERE id=$1", app.ID)
	code, _ = call("POST", root+"/plans/"+stale.ID+"/approve", "", map[string]string{"planHash": stale.PlanHash})
	if code != 409 {
		t.Fatal("stale definition approved")
	}
	c.Store.Pool.Exec(ctx, "UPDATE oap_applications SET version=version-1 WHERE id=$1", app.ID)
	expired := create()
	c.Store.Pool.Exec(ctx, "UPDATE oap_deployment_plans SET expires_at=now()-interval '1 second' WHERE id=$1", expired.ID)
	code, _ = call("POST", root+"/plans/"+expired.ID+"/approve", "", map[string]string{"planHash": expired.PlanHash})
	if code != 409 {
		t.Fatal("expired plan approved")
	}
	p, _ := svc.Principal(ctx, read)
	c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET revoked_at=now() WHERE id=$1", p.CredentialID)
	code, _ = call("POST", root+"/mcp", read, rpc("get_diagnostic_context", app.ID))
	if code != 401 {
		t.Fatal("revoked remote agent accepted")
	}
	if f.deploys != 2 {
		t.Fatal("unexpected additional workload dispatch")
	}
}

func TestM2PlanRuntimeInputsRemainPrivateAndFreezeAcrossReview(t *testing.T) {
	ctx := context.Background()
	f := &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}}
	c, _ := setup(t, f)
	c.RequireIdentity = true
	service := access.Service{Pool: c.Store.Pool}
	user, err := service.Register(ctx, "m2-input@example.invalid", "Owner", "m2-private-input-password", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Issue(ctx, user, "session", "Owner", "operate", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := service.Principal(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].Image = "example/web@sha256:" + strings.Repeat("a", 64)
	m.Components[0].SecretRefs = map[string]string{"TOKEN": "token-v1"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	token, err := service.IssueRestricted(ctx, user, "Plan Creator", "operate", app.ID, time.Hour, domain.AgentPermissions{Actions: []string{"deploy"}, TargetID: "fixture", Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := service.Principal(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	agentCtx := domain.WithPrincipal(ctx, actor)
	ownerCtx := domain.WithPrincipal(ctx, owner)
	if _, err = c.CreateDeploymentPlan(agentCtx, app.ID, app.Version, []string{"web"}, nil); err == nil {
		t.Fatal("missing secret plan created")
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "plan-private-input-original")
	plan, err := c.CreateDeploymentPlan(agentCtx, app.ID, app.Version, []string{"web"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var private string
	if err = c.Store.Pool.QueryRow(ctx, "SELECT definition_hash FROM oap_deployment_plans WHERE id=$1", plan.ID).Scan(&private); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "plan-private-input-original") || strings.Contains(string(raw), private) {
		t.Fatal("private input fingerprint/value leaked")
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "plan-private-input-changed")
	if _, err = c.ApproveDeploymentPlan(ownerCtx, app.ID, plan.ID, plan.PlanHash); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("changed input approved", err)
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "plan-private-input-original")
	if _, err = c.ApproveDeploymentPlan(ownerCtx, app.ID, plan.ID, plan.PlanHash); err != nil {
		t.Fatal(err)
	}
	for _, inputs := range []*runtimeinputs.Inputs{nil, inputGrants(t, app.ID, "plan-private-input-changed")} {
		c.RuntimeInputs = inputs
		if _, err = c.ExecuteDeploymentPlan(agentCtx, app.ID, plan.ID); !errors.Is(err, domain.ErrConflict) {
			t.Fatal("changed/missing approved input executed", err)
		}
	}
	if f.ensures != 0 || f.deploys != 0 {
		t.Fatal("input mismatch reached provider")
	}
	c.RuntimeInputs = inputGrants(t, app.ID, "plan-private-input-original")
	release, err := c.ExecuteDeploymentPlan(agentCtx, app.ID, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := c.ExecuteDeploymentPlan(agentCtx, app.ID, plan.ID)
	if err != nil || retry.ID != release.ID {
		t.Fatal("restored approved input retry changed admission", err)
	}
}
