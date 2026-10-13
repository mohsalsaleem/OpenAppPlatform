package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

func TestHeadlessOnboardingOwnerReviewScopePauseRevocationAndExpiry(t *testing.T) {
	ctx := context.Background()
	c, _ := setup(t, &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}})
	c.RequireIdentity = true
	target := domain.Target{ID: "fixture", Name: "Docker fixture", Operator: "docker", Environment: "staging"}
	if e := c.Store.SaveTarget(ctx, target); e != nil {
		t.Fatal(e)
	}
	otherTarget := target
	otherTarget.ID = "other-target"
	c.Store.SaveTarget(ctx, otherTarget)
	operatorTarget := target
	operatorTarget.ID = "operator-target"
	operatorTarget.Operator = "coolify"
	c.Store.SaveTarget(ctx, operatorTarget)
	srv := httptest.NewServer((&httpapi.Server{Controller: c, SetupToken: strings.Repeat("s", 32)}).Handler())
	defer srv.Close()
	private := t.TempDir()
	os.Chmod(private, 0700)
	ownerFile := filepath.Join(private, "owner.json")
	agentFile := filepath.Join(private, "agent.json")
	invoke := func(identity string, args []string, body any, want int) cli.Result {
		t.Helper()
		var in, out bytes.Buffer
		if body != nil {
			json.NewEncoder(&in).Encode(body)
		}
		argv := []string{"--url", srv.URL}
		if identity != "" {
			argv = append(argv, "--credential-file", identity)
		}
		argv = append(argv, args...)
		code := cli.Run(ctx, argv, func(k string) string {
			if k == "OAP_SETUP_TOKEN" {
				return strings.Repeat("s", 32)
			}
			return ""
		}, &in, &out)
		var result cli.Result
		if json.Unmarshal(out.Bytes(), &result) != nil {
			t.Fatal("invalid CLI envelope")
		}
		if bytes.Contains(out.Bytes(), []byte(`"token":`)) || bytes.Contains(out.Bytes(), []byte(`"session":`)) {
			t.Fatal("issued credential leaked to output")
		}
		if code != want {
			t.Fatalf("CLI command %s exit %d, expected %d, code %v", args[len(args)-1], code, want, result.Error)
		}
		return result
	}
	decode := func(value any, dst any) {
		t.Helper()
		raw, _ := json.Marshal(value)
		if json.Unmarshal(raw, dst) != nil {
			t.Fatal("invalid data")
		}
	}
	invoke("", []string{"--out", ownerFile, "--file", "-", "setup"}, map[string]string{"email": "headless@example.invalid", "name": "Owner", "password": "headless-test-password"}, 0)
	// Fresh login is separate from setup, and cannot overwrite an existing credential.
	loginFile := filepath.Join(private, "login.json")
	invoke("", []string{"--out", loginFile, "--file", "-", "login"}, map[string]string{"email": "headless@example.invalid", "password": "headless-test-password"}, 0)
	invoke("", []string{"--out", ownerFile, "--file", "-", "setup"}, map[string]string{}, 2)
	invoke(ownerFile, []string{"--out", agentFile, "--file", "-", "grant"}, map[string]any{"name": "Lifecycle agent", "targetId": "fixture", "environment": "staging", "maxApplications": 1, "actions": []string{"deploy", "restart", "configure", "recover"}}, 0)
	invoke(ownerFile, []string{"--out", filepath.Join(private, "bad.json"), "--file", "-", "grant"}, map[string]any{"name": "Wrong operator", "targetId": "operator-target", "environment": "staging", "maxApplications": 1, "actions": []string{"deploy"}}, 4)
	var targets []domain.Target
	decode(invoke(agentFile, []string{"targets"}, nil, 0).Data, &targets)
	if len(targets) != 1 || targets[0].ID != "fixture" {
		t.Fatal("target enumeration escaped pin")
	}
	invoke(agentFile, []string{"--target", "fixture", "target-capabilities"}, nil, 0)
	invoke(agentFile, []string{"--target", "other-target", "resources"}, nil, 3)
	invoke(agentFile, []string{"agent-control"}, nil, 3)
	invoke(agentFile, []string{"--out", filepath.Join(private, "self.json"), "--file", "-", "grant"}, map[string]any{}, 3)
	m := manifest()
	m.Name = "headless-created"
	m.Components[0].Instances = 1
	m.Components[0].Image = "example/api@sha256:" + strings.Repeat("a", 64)
	siblingManifest := m
	siblingManifest.Name = "owner-sibling"
	sibling, e := c.CreateApplication(ctx, siblingManifest)
	if e != nil {
		t.Fatal(e)
	}
	var app, retried domain.Application
	decode(invoke(agentFile, []string{"--key", "create-one", "--file", "-", "create"}, m, 0).Data, &app)
	decode(invoke(agentFile, []string{"--key", "create-one", "--file", "-", "create"}, m, 0).Data, &retried)
	if app.ID != retried.ID {
		t.Fatal("creation retry duplicated application")
	}
	changed := m
	changed.Name = "second-app"
	invoke(agentFile, []string{"--key", "create-two", "--file", "-", "create"}, changed, 4)
	changed.TargetID = "other-target"
	invoke(agentFile, []string{"--key", "outside", "--file", "-", "create"}, changed, 4)
	invoke(agentFile, []string{"--app", sibling.ID, "get"}, nil, 3)
	var apps []domain.Application
	decode(invoke(agentFile, []string{"apps"}, nil, 0).Data, &apps)
	if len(apps) != 1 || apps[0].ID != app.ID {
		t.Fatal("application enumeration escaped creation ownership")
	}
	// Same token receives existing API lifecycle authority only on its own app.
	var planned struct {
		ID   string `json:"id"`
		Hash string `json:"planHash"`
	}
	decode(invoke(agentFile, []string{"--app", app.ID, "--file", "-", "plan"}, map[string]any{"expectedVersion": app.Version, "components": []string{"web"}}, 0).Data, &planned)
	invoke(agentFile, []string{"--app", app.ID, "--plan", planned.ID, "--file", "-", "approve"}, map[string]string{"planHash": planned.Hash}, 3)
	invoke(ownerFile, []string{"--app", app.ID, "--plan", planned.ID, "--file", "-", "approve"}, map[string]string{"planHash": planned.Hash}, 0)
	first := invoke(agentFile, []string{"--app", app.ID, "--plan", planned.ID, "--file", "-", "execute-plan"}, map[string]any{}, 0)
	repeat := invoke(agentFile, []string{"--app", app.ID, "--plan", planned.ID, "--file", "-", "execute-plan"}, map[string]any{}, 0)
	if first.OperationID == "" || first.OperationID != repeat.OperationID {
		t.Fatal("plan execution was not durable/idempotent")
	}
	if e = c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(ctx)
	invoke(agentFile, []string{"--operation", first.OperationID, "--timeout", "10s", "wait"}, nil, 0)
	svc := access.Service{Pool: c.Store.Pool}
	control, e := svc.Control(ctx)
	if e != nil {
		t.Fatal(e)
	}
	invoke(ownerFile, []string{"--file", "-", "set-agent-control"}, map[string]any{"expectedVersion": control.Version, "enabled": false, "reason": "Headless pause acceptance"}, 0)
	invoke(agentFile, []string{"apps"}, nil, 3)
	invoke(ownerFile, []string{"--file", "-", "set-agent-control"}, map[string]any{"expectedVersion": control.Version + 1, "enabled": true, "reason": "Resume acceptance"}, 0)
	var identity domain.Principal
	decode(invoke(agentFile, []string{"whoami"}, nil, 0).Data, &identity)
	invoke(ownerFile, []string{"--credential", identity.CredentialID, "revoke"}, nil, 0)
	invoke(agentFile, []string{"apps"}, nil, 3)
	// Expiry and user deactivation are checked at the service/admission boundary.
	expired, id, e := svc.IssueOnboarding(ctx, identity.UserID, "Expired", domain.AgentPermissions{TargetID: "fixture", Environment: "staging", Actions: []string{"deploy"}, CreateApplications: true, MaxApplications: 1})
	if e != nil {
		t.Fatal(e)
	}
	c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET expires_at=$2 WHERE id=$1", id, time.Now().Add(-time.Minute))
	if _, e = svc.Principal(ctx, expired); e == nil {
		t.Fatal("expired onboarding grant accepted")
	}
	var count int
	c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_audit WHERE credential_id=$1 AND action LIKE 'POST /api/v1/applications%' AND status IN (201,202)", identity.CredentialID).Scan(&count)
	if count < 3 {
		t.Fatal("agent creation/execution audit missing")
	}
}

func TestConcurrentOnboardingCreationLimitAndCredentialIsolation(t *testing.T) {
	ctx := context.Background()
	c, _ := setup(t, &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}})
	c.RequireIdentity = true
	target := domain.Target{ID: "fixture", Name: "Docker fixture", Operator: "docker", Environment: "staging"}
	if err := c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	svc := access.Service{Pool: c.Store.Pool}
	user, err := svc.Register(ctx, "creation-race@example.invalid", "Owner", "creation-race-test-password", "")
	if err != nil {
		t.Fatal(err)
	}
	issue := func(name string) domain.Principal {
		t.Helper()
		token, _, e := svc.IssueOnboarding(ctx, user, name, domain.AgentPermissions{TargetID: "fixture", Environment: "staging", Actions: []string{"deploy"}, CreateApplications: true, MaxApplications: 1})
		if e != nil {
			t.Fatal(e)
		}
		p, e := svc.Principal(ctx, token)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	agent := issue("Bounded creator")
	other := issue("Other creator")
	type outcome struct {
		app domain.Application
		err error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for i := range 2 {
		go func(index int) {
			<-start
			m := manifest()
			m.Name = []string{"race-first", "race-second"}[index]
			app, e := c.CreateApplication(domain.WithCreationKey(domain.WithPrincipal(ctx, agent), m.Name), m)
			results <- outcome{app, e}
		}(i)
	}
	close(start)
	success := 0
	var created domain.Application
	for range 2 {
		result := <-results
		if result.err == nil {
			success++
			created = result.app
		}
	}
	if success != 1 {
		t.Fatal("concurrent requests exceeded creation limit")
	}
	current, e := svc.ByID(ctx, agent.CredentialID)
	if e != nil || !current.Allows(created.ID, "deploy", "fixture", "staging") {
		t.Fatal("creator lost own application authority")
	}
	if other.Reads(created.ID, "fixture", "staging") {
		t.Fatal("another target grant inherited application access")
	}
	// Authentication snapshots cannot bypass the admission recheck after revocation.
	c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET revoked_at=now() WHERE id=$1", other.CredentialID)
	m := manifest()
	m.Name = "revoked-creator"
	if _, e = c.CreateApplication(domain.WithCreationKey(domain.WithPrincipal(ctx, other), "revoked-create"), m); e == nil {
		t.Fatal("revoked authentication snapshot created an app")
	}
}
