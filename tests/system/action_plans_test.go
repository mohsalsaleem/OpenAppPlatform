package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

func TestPortableActionPlansAtomicSaveReviewAndStaleness(t *testing.T) {
	ctx := context.Background()
	c, _ := setup(t, &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}})
	c.RequireIdentity = true
	if err := c.Store.SaveTarget(ctx, domain.Target{ID: "fixture", Name: "Docker", Operator: "docker", Environment: "staging"}); err != nil {
		t.Fatal(err)
	}
	svc := access.Service{Pool: c.Store.Pool}
	user, err := svc.Register(ctx, "portable@example.invalid", "Owner", "portable-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.Issue(ctx, user, "session", "Owner", "operate", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.Principal(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := domain.WithPrincipal(ctx, owner)
	token, _, err := svc.IssueOnboarding(ctx, user, "Lifecycle", domain.AgentPermissions{TargetID: "fixture", Environment: "staging", CreateApplications: true, MaxApplications: 1, Actions: []string{"configure", "deploy", "restart", "recover"}})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := svc.Principal(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	agentCtx := domain.WithPrincipal(ctx, actor)
	m := manifest()
	m.Name = "portable"
	m.Components[0].Instances = 1
	m.Components[0].Image = "sha256:" + strings.Repeat("a", 64)
	m.Components[0].Env = map[string]string{"PRIVATE": "portable-literal-secret"}
	spec := domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: m}
	create, err := c.CreateActionPlan(agentCtx, controller.ActionRequest{Action: "create", Specification: &spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateApplication(domain.WithCreationKey(agentCtx, "action-plan:"+create.ID), m); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("direct creation reused reserved plan key", err)
	}
	var count int
	c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_applications").Scan(&count)
	if count != 0 {
		t.Fatal("planning created application")
	}
	raw, _ := json.Marshal(create)
	if bytes.Contains(raw, []byte("portable-literal-secret")) {
		t.Fatal("public plan leaked literal")
	}
	const concurrency = 3
	results := make(chan store.ActionPlan, concurrency)
	failures := make(chan error, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p, e := c.ExecuteActionPlan(agentCtx, create.ID); results <- p; failures <- e }()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	appID := ""
	for p := range results {
		if p.Result == nil {
			t.Fatal("no receipt")
		}
		if appID != "" && appID != p.Result.ApplicationID {
			t.Fatal("duplicate creation")
		}
		appID = p.Result.ApplicationID
	}
	c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_applications").Scan(&count)
	if count != 1 {
		t.Fatal("creation not atomic")
	}
	// Distinct creation plans share one bounded credential. At capacity both
	// must deny cleanly, without a shared-to-exclusive credential lock deadlock.
	first, err := c.CreateActionPlan(agentCtx, controller.ActionRequest{Action: "create", Specification: &spec})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.CreateActionPlan(agentCtx, controller.ActionRequest{Action: "create", Specification: &spec})
	if err != nil {
		t.Fatal(err)
	}
	capacityErrors := make(chan error, 2)
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); _, e := c.ExecuteActionPlan(agentCtx, id); capacityErrors <- e }(id)
	}
	wg.Wait()
	close(capacityErrors)
	for e := range capacityErrors {
		if !errors.Is(e, domain.ErrConflict) {
			t.Fatal("concurrent creation capacity/lock failure", e)
		}
	}
	actor, err = svc.Principal(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	agentCtx = domain.WithPrincipal(ctx, actor)
	app, err := c.Store.Application(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	desired := spec
	desired.Spec.Components = append([]domain.Component(nil), spec.Spec.Components...)
	desired.Spec.Components[0].Env = map[string]string{"PRIVATE": "second-private-value"}
	configure, err := c.CreateActionPlan(agentCtx, controller.ActionRequest{Action: "configure", ApplicationID: appID, ExpectedVersion: app.Version, Specification: &desired, RequireReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecuteActionPlan(agentCtx, configure.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("review not enforced", err)
	}
	if _, err = c.ApproveActionPlan(agentCtx, configure.ID, configure.Hash); !errors.Is(err, access.ErrDenied) {
		t.Fatal("agent self-approved", err)
	}
	if _, err = c.ApproveActionPlan(ownerCtx, configure.ID, strings.Repeat("0", 64)); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("wrong hash approved", err)
	}
	if _, err = c.ApproveActionPlan(ownerCtx, configure.ID, configure.Hash); err != nil {
		t.Fatal(err)
	}
	saved, err := c.ExecuteActionPlan(agentCtx, configure.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Result == nil || saved.Result.State != "saved" || saved.Result.DefinitionVersion != 2 || saved.Result.OperationID != "" {
		t.Fatal("save is not metadata-only")
	}
	repeated, err := c.ExecuteActionPlan(agentCtx, configure.ID)
	if err != nil || repeated.Result.DefinitionVersion != 2 {
		t.Fatal("save replay changed definition", err)
	}
	c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_deployments").Scan(&count)
	if count != 0 {
		t.Fatal("save deployed")
	}
	deploy, err := c.CreateActionPlan(agentCtx, controller.ActionRequest{Action: "deploy", ApplicationID: appID, ExpectedVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Store.Pool.Exec(ctx, "UPDATE oap_applications SET version=version+1 WHERE id=$1", appID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecuteActionPlan(agentCtx, deploy.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale plan admitted", err)
	}
	// Rebind a private reference after planning; values never enter public plans.
	app, err = c.Store.Application(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	app.Manifest.Components[0].SecretRefs = map[string]string{"TOKEN": "token-v1"}
	updated, err := c.UpdateApplication(agentCtx, appID, app.Manifest, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := json.Marshal([]runtimeinputs.Binding{{Reference: "token-v1", TargetID: "fixture", ApplicationID: appID, Component: "web", Variable: "TOKEN", ValueEnv: "OAP_SECRET_PORTABLE"}})
	c.RuntimeInputs, err = runtimeinputs.Parse(binding, func(string) string { return "private-before" })
	if err != nil {
		t.Fatal(err)
	}
	next := func() store.ActionPlan {
		t.Helper()
		p, e := c.CreateActionPlan(agentCtx, controller.ActionRequest{Action: "deploy", ApplicationID: appID, ExpectedVersion: updated.Version})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	privatePlan := next()
	c.RuntimeInputs, err = runtimeinputs.Parse(binding, func(string) string { return "private-after" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecuteActionPlan(agentCtx, privatePlan.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("changed private input admitted", err)
	}
	public, _ := json.Marshal(privatePlan)
	if bytes.Contains(public, []byte("private-before")) || bytes.Contains(public, []byte("private-after")) {
		t.Fatal("private input leaked")
	}
	expired := next()
	if _, err = c.Store.Pool.Exec(ctx, "UPDATE oap_action_plans SET expires_at=now()-interval '1 second' WHERE id=$1", expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecuteActionPlan(agentCtx, expired.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("expired plan admitted", err)
	}
	changedTarget := next()
	target, err := c.Store.Target(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	target.Name = "changed-target"
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecuteActionPlan(agentCtx, changedTarget.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("changed target admitted", err)
	}
	if _, err = c.CreateActionPlan(agentCtx, controller.ActionRequest{Action: "deploy", ApplicationID: appID, ExpectedVersion: updated.Version, Images: map[string]string{"web": "example/api:mutable"}}); err == nil {
		t.Fatal("mutable image plan accepted")
	}
	if _, err = c.EnqueueVersion(agentCtx, appID, "action-plan:"+changedTarget.ID, nil, updated.Version); err == nil {
		t.Fatal("ordinary endpoint used plan namespace")
	}
	// Exercise root-plan HTTP authorization; body identifiers cannot escape scope.
	api := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer api.Close()
	call := func(method, path, bearer string, body any) int {
		t.Helper()
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, api.URL+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}
	reader, err := svc.IssueRestricted(ctx, user, "Reader", "read", appID, time.Hour, domain.AgentPermissions{Actions: []string{}, TargetID: "fixture", Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	request := controller.ActionRequest{Action: "deploy", ApplicationID: appID, ExpectedVersion: updated.Version}
	if status := call("POST", "/api/v1/action-plans", reader, request); status != 403 {
		t.Fatal("read agent planned mutation", status)
	}
	if status := call("GET", "/api/v1/action-plans/"+changedTarget.ID+"/input", token, nil); status != 403 {
		t.Fatal("agent read owner-only literal input", status)
	}
	request.ApplicationID = strings.Repeat("0", 32)
	if status := call("POST", "/api/v1/action-plans", token, request); status != 404 && status != 403 {
		t.Fatal("scope escaped", status)
	}
	pending := next()
	if _, err = c.Store.Pool.Exec(ctx, "UPDATE oap_agent_control SET enabled=false WHERE workspace_id='default'"); err != nil {
		t.Fatal(err)
	}
	if status := call("POST", "/api/v1/action-plans/"+pending.ID+"/execute", token, map[string]any{}); status != 401 && status != 403 {
		t.Fatal("paused agent executed", status)
	}
	if _, err = c.Store.Pool.Exec(ctx, "UPDATE oap_agent_control SET enabled=true WHERE workspace_id='default'"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET revoked_at=now() WHERE id=$1", actor.CredentialID); err != nil {
		t.Fatal(err)
	}
	if status := call("POST", "/api/v1/action-plans/"+pending.ID+"/execute", token, map[string]any{}); status != 401 {
		t.Fatal("revoked agent executed", status)
	}
	c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_deployments").Scan(&count)
	if count != 0 {
		t.Fatal("denied plan dispatched")
	}

}
