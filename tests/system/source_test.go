package system_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/coolify"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
)

type fixtureBuilder struct {
	calls    int
	fail     bool
	requests []source.BuildRequest
}

func (b *fixtureBuilder) Build(_ context.Context, r source.BuildRequest) (source.BuildResult, error) {
	b.calls++
	b.requests = append(b.requests, r)
	if b.fail {
		return source.BuildResult{}, errors.New("uncertain")
	}
	return source.BuildResult{Commit: r.Commit, Image: r.Component.ImageRepository + "@sha256:" + strings.Repeat("a", 64)}, nil
}
func TestSignedGitHubDeliveryBuildOnceSelectedReplicasAndRecovery(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	ctx := context.Background()
	m := manifest()
	m.Name = "source-app"
	m.Components[0].Instances = 2
	m.Components = append(m.Components, domain.Component{Name: "api", Image: "nginx:alpine", Port: 80})
	app, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	service := access.Service{Pool: c.Store.Pool}
	user, e := service.Register(ctx, "owner@example.invalid", "Owner", "owner-source-password", "")
	if e != nil {
		t.Fatal(e)
	}
	token, e := service.Issue(ctx, user, "agent", "GitHub", "operate", app.ID, 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	p, e := service.Principal(ctx, token)
	if e != nil {
		t.Fatal(e)
	}
	c.RequireIdentity = true
	t.Setenv("OAP_TEST_HOOK_SECRET", strings.Repeat("s", 32))
	t.Setenv("OAP_TEST_HOOK_CREDENTIAL", token)
	h := source.Hook{ID: "fixture", ApplicationID: app.ID, Repository: "owner/repository", RepositoryID: 123, Branch: "main", SecretEnv: "OAP_TEST_HOOK_SECRET", CredentialEnv: "OAP_TEST_HOOK_CREDENTIAL", Components: []source.Component{{Name: "web", Context: ".", ImageRepository: "registry.example/owner/web"}}}
	c.SourceHooks = map[string]source.Hook{h.ID: h}
	builder := &fixtureBuilder{}
	c.SourceBuilder = builder
	srv := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer srv.Close()
	push := source.Push{Ref: "refs/heads/main", After: strings.Repeat("b", 40)}
	push.Repository.ID = 123
	push.Repository.FullName = h.Repository
	call := func(delivery, signature string, value source.Push) (int, []byte) {
		body, _ := json.Marshal(value)
		r, _ := http.NewRequest("POST", srv.URL+"/api/v1/hooks/github/fixture", strings.NewReader(string(body)))
		r.Header.Set("X-GitHub-Event", "push")
		r.Header.Set("X-GitHub-Delivery", delivery)
		if signature == "valid" {
			mac := hmac.New(sha256.New, []byte(strings.Repeat("s", 32)))
			mac.Write(body)
			signature = "sha256=" + hex.EncodeToString(mac.Sum(nil))
		}
		r.Header.Set("X-Hub-Signature-256", signature)
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var out json.RawMessage
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := call("delivery-0001", "invalid", push); code != 401 {
		t.Fatal("bad signature accepted")
	}
	mismatch := push
	mismatch.Repository.ID = 999
	if code, _ := call("delivery-0001", "valid", mismatch); code != 200 {
		t.Fatal("unmapped repository not ignored")
	}
	code, raw := call("delivery-0001", "valid", push)
	if code != 202 {
		t.Fatalf("intake %d %s", code, raw)
	}
	var event controller.SourceEvent
	json.Unmarshal(raw, &event)
	_, raw = call("delivery-0001", "valid", push)
	var duplicate controller.SourceEvent
	json.Unmarshal(raw, &duplicate)
	if duplicate.ID != event.ID {
		t.Fatal("delivery was not deduplicated")
	}
	changed := push
	changed.After = strings.Repeat("c", 40)
	changed.Before = push.After
	if code, _ := call("delivery-0001", "valid", changed); code != 409 {
		t.Fatal("changed duplicate accepted")
	}
	for i := 0; i < 3; i++ {
		_ = c.AdvanceSource(ctx, event.ID)
	}
	event, e = c.SourceEvent(ctx, event.ID)
	if e != nil || event.State != "released" || builder.calls != 1 {
		t.Fatalf("build/release %+v %v calls=%d", event, e, builder.calls)
	}
	release, e := c.Store.Deployment(ctx, event.DeploymentID)
	if e != nil || len(release.Steps) != 2 || release.Source == nil || release.Source.Commit != push.After {
		t.Fatal("selected component provenance lost")
	}
	for _, step := range release.Steps {
		if step.Component != "web" {
			t.Fatal("unaffected component deployed")
		}
	}
	if release.Manifest.Components[0].Image != "registry.example/owner/web@sha256:"+strings.Repeat("a", 64) {
		t.Fatal("digest snapshot lost")
	}
	for i := 0; i < 12; i++ {
		_ = c.Advance(ctx, release.ID)
	}
	release, _ = c.Store.Deployment(ctx, release.ID)
	if release.State != "succeeded" || f.deploys != 2 {
		t.Fatal("replica release failed")
	}
	builder.fail = true
	code, raw = call("delivery-0002", "valid", changed)
	if code != 202 {
		t.Fatal(code)
	}
	json.Unmarshal(raw, &event)
	_ = c.AdvanceSource(ctx, event.ID)
	event, _ = c.SourceEvent(ctx, event.ID)
	if event.State != "attention" {
		t.Fatal("uncertain build automatically retried")
	}
	calls := builder.calls
	_ = c.AdvanceSource(ctx, event.ID)
	if builder.calls != calls {
		t.Fatal("attention rebuilt automatically")
	}
	ownerSecret, _ := service.Issue(ctx, user, "session", "Owner", "operate", "", time.Hour)
	owner, _ := service.Principal(ctx, ownerSecret)
	ownerCtx := domain.WithPrincipal(ctx, owner)
	if _, e = c.RecoverSource(ownerCtx, event.ID, event.UpdatedAt, app.Version, false, false); e == nil {
		t.Fatal("uncertain build retried without explicit confirmation")
	}
	recovered, e := c.RecoverSource(ownerCtx, event.ID, event.UpdatedAt, app.Version, true, false)
	if e != nil {
		t.Fatal(e)
	}
	builder.fail = false
	_ = c.AdvanceSource(ctx, recovered.ID)
	_ = c.AdvanceSource(ctx, recovered.ID)
	recovered, _ = c.SourceEvent(ctx, recovered.ID)
	if recovered.State != "released" {
		t.Fatal("explicit recovery failed", recovered.Error)
	}
	if builder.requests[0].Commit != push.After || builder.requests[0].Component.Name != "web" {
		t.Fatal("build request not pinned")
	}
	// Revoked credentials cannot start another build.
	push.Before = changed.After
	_, raw = call("delivery-0003", "valid", push)
	json.Unmarshal(raw, &event)
	c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET revoked_at=now() WHERE id=$1", p.CredentialID)
	calls = builder.calls
	_ = c.AdvanceSource(ctx, event.ID)
	event, _ = c.SourceEvent(ctx, event.ID)
	if builder.calls != calls || event.State != "attention" {
		t.Fatal("revoked credential built source")
	}
}

type nativeSourceFixture struct {
	*fake
	observations int
}

func (f *nativeSourceFixture) FindSourceDeployment(_ context.Context, id, repo, branch, commit string, _ time.Time) (operator.NativeSourceDeployment, error) {
	f.observations++
	return operator.NativeSourceDeployment{ID: "native-deployment", Commit: commit, State: "succeeded", ResourceStatus: "running:healthy"}, nil
}
func TestGitHubAppNativeObservationKeepsOperatorOwnership(t *testing.T) {
	f := &nativeSourceFixture{fake: &fake{resources: map[string]operator.Resource{"native-source": {ID: "native-source", ArtifactKind: "source", Port: 8080, Status: "running:healthy"}}}}
	c, _ := setup(t, f)
	ctx := context.Background()
	m := domain.Manifest{Name: "native-app", TargetID: "fixture", Environment: "staging", Components: []domain.Component{{Name: "web", ResourceID: "native-source", Management: "observe", Port: 8080}}}
	app, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	service := access.Service{Pool: c.Store.Pool}
	user, _ := service.Register(ctx, "owner@example.invalid", "Owner", "native-owner-password", "")
	token, _ := service.Issue(ctx, user, "agent", "Native tracking", "read", app.ID, time.Hour)
	p, _ := service.Principal(ctx, token)
	hook := source.Hook{ID: "native", Mode: "coolify-github-app", ApplicationID: app.ID, Repository: "owner/repository", RepositoryID: 123, Branch: "main", Components: []source.Component{{Name: "web"}}}
	push := source.Push{After: strings.Repeat("a", 40)}
	event, e := c.ReceiveSource(domain.WithPrincipal(ctx, p), hook, "delivery-native-1", []byte("signed fixture"), push)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.AdvanceSource(ctx, event.ID); e != nil {
		t.Fatal(e)
	}
	event, _ = c.SourceEvent(ctx, event.ID)
	if event.State != "observed" || event.Builds["web"].Provider.ID != "native-deployment" || f.deploys != 0 || len(f.resources) != 1 {
		t.Fatal("native integration changed provider ownership or deployed")
	}
	current, _ := c.Store.Application(ctx, app.ID)
	if current.Manifest.Components[0].Management != "observe" {
		t.Fatal("source management handed off implicitly")
	}
}

func TestLiveCoolifyGitHubAppPinnedCommit(t *testing.T) {
	if os.Getenv("OAP_LIVE_COOLIFY") != "1" {
		t.Skip("opt-in isolated native GitHub App staging deployment")
	}
	var fixture struct {
		UUID   string `json:"uuid"`
		Commit string `json:"commit"`
	}
	raw, e := os.ReadFile("../../.local/native-github-fixture.json")
	if e != nil {
		t.Fatal("create the isolated native fixture first")
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.UUID == "" || fixture.Commit == "" {
		t.Fatal("native fixture metadata required")
	}
	target := domain.Target{ID: "fixture", Name: "Coolify source staging", Operator: "coolify", URL: os.Getenv("COOLIFY_URL"), ProjectID: os.Getenv("COOLIFY_PROJECT_ID"), ServerID: os.Getenv("COOLIFY_SERVER_ID"), Environment: os.Getenv("COOLIFY_ENVIRONMENT")}
	if target.Environment != "staging" {
		t.Fatal("staging required")
	}
	adapter, e := coolify.New(target, os.Getenv("COOLIFY_TOKEN"))
	if e != nil {
		t.Fatal(e)
	}
	resource, e := adapter.Inspect(context.Background(), fixture.UUID)
	if e != nil || resource.Description != "OpenAppPlatform:integration:github-native" {
		t.Fatal("isolated native ownership mismatch")
	}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	c.Store.SaveTarget(ctx, target)
	app, e := c.CreateApplication(ctx, domain.Manifest{Name: "native-live-" + domain.NewID()[:8], TargetID: "fixture", Environment: "staging", Components: []domain.Component{{Name: "web", ResourceID: resource.ID, Management: "observe", Image: resource.Image, Port: resource.Port}}})
	if e != nil {
		t.Fatal(e)
	}
	service := access.Service{Pool: c.Store.Pool}
	user, _ := service.Register(ctx, "owner@example.invalid", "Owner", "native-staging-password", "")
	token, _ := service.Issue(ctx, user, "agent", "Native tracker", "read", app.ID, time.Hour)
	principal, _ := service.Principal(ctx, token)
	hook := source.Hook{ID: "live-native", Mode: "coolify-github-app", ApplicationID: app.ID, Repository: "mohsalsaleem/OpenAppPlatform", RepositoryID: 1411193511, Branch: "main", Components: []source.Component{{Name: "web"}}}
	push := source.Push{After: fixture.Commit}
	event, e := c.ReceiveSource(domain.WithPrincipal(ctx, principal), hook, "live-native-"+domain.NewID(), []byte("isolated pinned source fixture"), push)
	if e != nil {
		t.Fatal(e)
	}
	// This explicit staging deploy uses Coolify's configured GitHub App to clone
	// the pinned revision. OAP's native lane only observes the provider operation.
	provider, e := adapter.Deploy(ctx, fixture.UUID)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("native staging provider=%s commit=%s", provider, fixture.Commit)
	deadline := time.Now().Add(6 * time.Minute)
	last := ""
	for time.Now().Before(deadline) {
		_ = c.AdvanceSource(ctx, event.ID)
		event, e = c.SourceEvent(ctx, event.ID)
		if e != nil {
			t.Fatal(e)
		}
		if event.State != last {
			t.Log("native observer", event.State)
			last = event.State
		}
		if event.State == "observed" {
			break
		}
		if event.State == "attention" {
			t.Fatal(event.Error)
		}
		time.Sleep(2 * time.Second)
	}
	if event.State != "observed" || event.Builds["web"].Provider == nil || event.Builds["web"].Provider.ID != provider || event.Builds["web"].Provider.Commit != fixture.Commit {
		t.Fatalf("native exact-commit observation incomplete: %s", event.State)
	}
	current, _ := c.Store.Application(ctx, app.ID)
	if current.Manifest.Components[0].Management != "observe" {
		t.Fatal("native ownership changed")
	}
	t.Logf("retained native GitHub App fixture=%s; exact commit and healthy runtime observed", fixture.UUID)
}

func TestSupersededAndOutOfOrderSourceEventsCannotBuildOrEnqueue(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	service := access.Service{Pool: c.Store.Pool}
	user, _ := service.Register(ctx, "owner@example.invalid", "Owner", "source-order-password", "")
	token, _ := service.Issue(ctx, user, "agent", "Ordered source", "operate", app.ID, time.Hour)
	p, _ := service.Principal(ctx, token)
	ctx = domain.WithPrincipal(ctx, p)
	h := source.Hook{ID: "ordered", ApplicationID: app.ID, Repository: "owner/repository", RepositoryID: 123, Branch: "main", Components: []source.Component{{Name: "web", Context: ".", ImageRepository: "registry.example/owner/web"}}}
	builder := &fixtureBuilder{}
	c.SourceBuilder = builder
	a := source.Push{After: strings.Repeat("a", 40)}
	first, e := c.ReceiveSource(ctx, h, "ordered-delivery-1", []byte("first"), a)
	if e != nil {
		t.Fatal(e)
	}
	b := source.Push{Before: a.After, After: strings.Repeat("b", 40)}
	second, e := c.ReceiveSource(ctx, h, "ordered-delivery-2", []byte("second"), b)
	if e != nil {
		t.Fatal(e)
	}
	_ = c.AdvanceSource(ctx, first.ID)
	first, _ = c.SourceEvent(ctx, first.ID)
	if first.State != "attention" || builder.calls != 0 {
		t.Fatal("superseded event built")
	}
	bad := source.Push{Before: a.After, After: strings.Repeat("c", 40)}
	outOfOrder, e := c.ReceiveSource(ctx, h, "ordered-delivery-3", []byte("out-of-order"), bad)
	if e != nil || outOfOrder.State != "attention" {
		t.Fatal("out-of-order push accepted")
	}
	_ = c.AdvanceSource(ctx, second.ID)
	_ = c.AdvanceSource(ctx, second.ID)
	second, _ = c.SourceEvent(ctx, second.ID)
	if second.State != "released" || builder.calls != 1 {
		t.Fatal("current head did not release")
	}
}
