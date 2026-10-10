package system_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/coolify"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
)

// This explicitly opted-in test retains two isolated staging resources. It uses
// a local controller and real SSH builder, not the hosted controller's identity.
func TestLiveSignedServerBuildCoolifyReplicas(t *testing.T) {
	if os.Getenv("OAP_LIVE_SERVER_BUILD") != "1" {
		t.Skip("opt-in signed source / server builder / Coolify staging verification")
	}
	commit := os.Getenv("OAP_TEST_SOURCE_COMMIT")
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	target := domain.Target{ID: "fixture", Name: "Signed build staging", Operator: "coolify", URL: os.Getenv("COOLIFY_URL"), ProjectID: os.Getenv("COOLIFY_PROJECT_ID"), ServerID: os.Getenv("COOLIFY_SERVER_ID"), Environment: os.Getenv("COOLIFY_ENVIRONMENT")}
	if target.Environment != "staging" || len(commit) != 40 {
		t.Fatal("explicit staging and committed source required")
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
	app, e := c.CreateApplication(ctx, domain.Manifest{Name: "signed-builder-" + domain.NewID()[:8], TargetID: "fixture", Environment: "staging", Components: []domain.Component{{Name: "web", Image: os.Getenv("COOLIFY_TEST_IMAGE"), Port: 8025, Instances: 2, Strategy: "standard"}}})
	if e != nil {
		t.Fatal(e)
	}
	service := access.Service{Pool: c.Store.Pool}
	user, e := service.Register(ctx, "builder-test@example.invalid", "Build verification", "isolated-build-owner-password", "")
	if e != nil {
		t.Fatal(e)
	}
	token, e := service.Issue(ctx, user, "agent", "Signed staging release", "operate", app.ID, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("OAP_LIVE_HOOK_CREDENTIAL", token)
	t.Setenv("OAP_LIVE_HOOK_SECRET", domain.NewID()+domain.NewID())
	hook := source.Hook{ID: "signed-live", Mode: "image-builder", ApplicationID: app.ID, Repository: "mohsalsaleem/OpenAppPlatform", RepositoryID: 1411193511, Branch: "main", SecretEnv: "OAP_LIVE_HOOK_SECRET", CredentialEnv: "OAP_LIVE_HOOK_CREDENTIAL", Components: []source.Component{{Name: "web", Context: "examples/server-build", ImageRepository: "127.0.0.1:5001/openappplatform/signed-staging"}}}
	c.RequireIdentity = true
	c.SourceHooks = map[string]source.Hook{hook.ID: hook}
	builder := source.CommandBuilder{Command: []string{"/usr/bin/python3", filepath.Join(root, "scripts/build-on-server.py"), "--ssh-host", "hz_primary", "--repository-root", root}}
	c.SourceBuilder = builder
	srv := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer srv.Close()
	push := source.Push{Ref: "refs/heads/main", After: commit}
	push.Repository.ID = hook.RepositoryID
	push.Repository.FullName = hook.Repository
	body, _ := json.Marshal(push)
	delivery := "signed-live-" + domain.NewID()
	send := func(signature string) (int, controller.SourceEvent) {
		t.Helper()
		r, _ := http.NewRequest("POST", srv.URL+"/api/v1/hooks/github/"+hook.ID, strings.NewReader(string(body)))
		r.Header.Set("X-GitHub-Event", "push")
		r.Header.Set("X-GitHub-Delivery", delivery)
		r.Header.Set("X-Hub-Signature-256", signature)
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var event controller.SourceEvent
		json.NewDecoder(res.Body).Decode(&event)
		return res.StatusCode, event
	}
	if code, _ := send("sha256=invalid"); code != 401 {
		t.Fatal("invalid signature accepted")
	}
	mac := hmac.New(sha256.New, []byte(os.Getenv(hook.SecretEnv)))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	code, event := send(signature)
	if code != 202 {
		t.Fatal("signed intake failed", code)
	}
	_, duplicate := send(signature)
	if duplicate.ID != event.ID {
		t.Fatal("duplicate intake changed identity")
	}
	if e = c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(ctx)
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		event, e = c.SourceEvent(ctx, event.ID)
		if e != nil {
			t.Fatal(e)
		}
		if event.State == "attention" {
			t.Fatal("source requires attention", event.Error)
		}
		if event.State == "released" {
			break
		}
		time.Sleep(time.Second)
	}
	if event.State != "released" {
		t.Fatal("source deadline", event.State)
	}
	build := event.Builds["web"]
	// Repeated helper invocation proves receipt reuse for the same stable build ID.
	reused, e := builder.Build(ctx, source.BuildRequest{BuildID: build.ID, Repository: hook.Repository, RepositoryID: hook.RepositoryID, Commit: commit, Component: hook.Components[0]})
	if e != nil || reused.Image != build.Image {
		t.Fatal("builder receipt reuse failed")
	}
	release := waitCoolifyRelease(t, event.DeploymentID, func(id string) (domain.Deployment, error) { return c.Store.Deployment(ctx, id) })
	if release.Source == nil || release.Source.Commit != commit || len(release.Steps) != 2 {
		t.Fatal("release provenance/replica count mismatch")
	}
	var resources []string
	for _, step := range release.Steps {
		resource, e := adapter.Inspect(ctx, step.ResourceID)
		if e != nil || resource.Image != build.Image || !strings.Contains(resource.Status, "healthy") {
			t.Fatal("replica digest or health mismatch", step.ResourceID)
		}
		resources = append(resources, resource.ID)
	}
	_, duplicate = send(signature)
	if duplicate.ID != event.ID || duplicate.DeploymentID != release.ID {
		t.Fatal("completed redelivery changed release")
	}
	receipt := map[string]any{"applicationId": app.ID, "eventId": event.ID, "deploymentId": release.ID, "commit": commit, "image": build.Image, "buildId": build.ID, "resources": resources, "verifiedAt": time.Now().UTC()}
	raw, _ := json.MarshalIndent(receipt, "", "  ")
	if e = os.WriteFile(filepath.Join(root, ".local/signed-build-live.json"), append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("retained staging resources=%v; one digest, signed delivery, receipt reuse and healthy replicas verified", resources)
}
