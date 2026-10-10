package system_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/coolify"
)

func TestObservedAssemblyReservationsAndManagementBoundary(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{
		"existing-image":  {ID: "existing-image", Name: "Existing image", Image: "nginx:1.27-alpine", Port: 80, ArtifactKind: "image", Status: "running:healthy"},
		"existing-source": {ID: "existing-source", Name: "Source app", Port: 3000, ArtifactKind: "source", Status: "running:healthy"},
	}}
	c, srv := setup(t, f)
	m := domain.Manifest{Name: "assembled", Environment: "staging", TargetID: "fixture", Components: []domain.Component{
		{Name: "web", ResourceID: "existing-image", Image: "nginx:1.27-alpine", Port: 80, Management: "observe"},
		{Name: "api", ResourceID: "existing-source", Port: 3000, Management: "observe"},
	}}
	code, raw := request(t, srv, http.MethodPost, "/api/v1/applications", "", m)
	if code != 201 {
		t.Fatalf("assembly HTTP %d: %s", code, raw)
	}
	var app domain.Application
	json.Unmarshal(raw, &app)
	instances, err := c.Instances(context.Background(), app.ID)
	if err != nil || len(instances) != 2 || instances[0].Status != "running:healthy" {
		t.Fatalf("observed health unavailable: %+v %v", instances, err)
	}
	code, _ = request(t, srv, http.MethodGet, "/api/v1/applications/"+app.ID+"/logs/api", "", nil)
	if code != 200 {
		t.Fatal("source logs unavailable")
	}
	items, err := c.Discover(context.Background(), "fixture")
	if err != nil || len(items) != 2 {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ApplicationID != app.ID {
			t.Fatal("reservation missing")
		}
	}
	m.Name = "duplicate"
	code, _ = request(t, srv, http.MethodPost, "/api/v1/applications", "", m)
	if code != 409 {
		t.Fatalf("duplicate binding HTTP %d", code)
	}
	if _, err = c.Enqueue(context.Background(), app.ID, "observed-deploy"); err == nil {
		t.Fatal("observe deployment allowed")
	}
	if _, err = c.EnqueueRestart(context.Background(), app.ID, "observed-restart", controller.RestartRequest{Component: "api", Ordinal: 1}, app.Version); err == nil {
		t.Fatal("observe restart allowed")
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].Management = ""
	if _, err = c.UpdateApplication(context.Background(), app.ID, changed, app.Version); err == nil {
		t.Fatal("configuration bypassed handoff")
	}
	if _, err = c.EnableManagement(context.Background(), app.ID, "api", app.Version); err == nil {
		t.Fatal("source handoff allowed")
	}
	if _, err = c.EnableManagement(context.Background(), app.ID, "web", app.Version+1); err == nil {
		t.Fatal("stale version allowed")
	}
	changedResource := f.resources["existing-image"]
	changedResource.Port = 81
	f.resources["existing-image"] = changedResource
	if _, err = c.EnableManagement(context.Background(), app.ID, "web", app.Version); err == nil {
		t.Fatal("configuration drift accepted")
	}
	changedResource.Port = 80
	changedResource.Description = "OpenAppPlatform:other:web"
	f.resources["existing-image"] = changedResource
	if _, err = c.EnableManagement(context.Background(), app.ID, "web", app.Version); err == nil {
		t.Fatal("foreign OAP ownership accepted")
	}
	changedResource.Description = ""
	f.resources["existing-image"] = changedResource
	enabled, err := c.EnableManagement(context.Background(), app.ID, "web", app.Version)
	if err != nil || enabled.Version != app.Version+1 || enabled.Manifest.Components[0].Management != "" {
		t.Fatalf("image handoff failed: %v", err)
	}
	if _, err = c.Enqueue(context.Background(), app.ID, "mixed-deploy"); err == nil {
		t.Fatal("mixed observed app deployed")
	}
	var releases int
	c.Store.Pool.QueryRow(context.Background(), "SELECT count(*) FROM oap_deployments").Scan(&releases)
	if f.deploys != 0 || len(f.resources) != 2 || releases != 0 {
		t.Fatal("assembly/handoff mutated provider or created a release")
	}
}

func TestLiveCoolifyObserveOnlyAssembly(t *testing.T) {
	if os.Getenv("OAP_LIVE_COOLIFY") != "1" {
		t.Skip("opt-in read-only Coolify staging verification")
	}
	target := domain.Target{ID: "fixture", Name: "Coolify staging", Operator: "coolify", URL: os.Getenv("COOLIFY_URL"), ProjectID: os.Getenv("COOLIFY_PROJECT_ID"), ServerID: os.Getenv("COOLIFY_SERVER_ID"), Environment: os.Getenv("COOLIFY_ENVIRONMENT")}
	if target.Environment != "staging" || target.ProjectID == "" {
		t.Fatal("explicit staging scope required")
	}
	// Every provider call passes through a GET-only boundary. Accidental mutation
	// fails locally and never reaches the operator.
	providerURL := target.URL
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	var writes atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			w.WriteHeader(405)
			return
		}
		upstream, e := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(providerURL, "/")+r.URL.RequestURI(), nil)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		upstream.Header.Set("Authorization", "Bearer "+os.Getenv("COOLIFY_TOKEN"))
		response, e := client.Do(upstream)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		io.Copy(w, io.LimitReader(response.Body, 2<<20+1))
	}))
	defer proxy.Close()
	target.URL = proxy.URL
	adapter, err := coolify.New(target, os.Getenv("COOLIFY_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := setup(t, adapter)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	resources, err := adapter.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := domain.Manifest{Name: "observed-" + domain.NewID()[:8], Environment: target.Environment, TargetID: target.ID}
	before := map[string]operator.Resource{}
	snapshots := map[string][32]byte{}
	selected := map[string]string{"image": os.Getenv("OAP_ACCEPTANCE_IMAGE_RESOURCE"), "source": os.Getenv("OAP_ACCEPTANCE_SOURCE_RESOURCE")}
	if selected["image"] == "" || selected["source"] == "" || selected["image"] == selected["source"] {
		t.Fatal("explicit distinct image and source staging resource IDs required")
	}
	kinds := map[string]bool{}
	for _, r := range resources {
		if selected[r.ArtifactKind] != r.ID {
			continue
		}
		actual, err := adapter.Inspect(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		m.Components = append(m.Components, domain.Component{Name: fmt.Sprintf("service-%d", len(m.Components)+1), ResourceID: actual.ID, Image: actual.Image, Port: actual.Port, Management: "observe"})
		before[actual.ID] = actual
		kinds[actual.ArtifactKind] = true
		snapshots[actual.ID] = coolifyConfigurationFingerprint(t, ctx, proxy.URL, actual.ID)
		if len(m.Components) == 2 {
			break
		}
	}
	if len(m.Components) != 2 || !kinds["image"] || !kinds["source"] {
		t.Fatal("selected staging image and source services required")
	}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	live, err := c.Instances(ctx, app.ID)
	if err != nil || len(live) != 2 {
		t.Fatalf("live inspection: %v", err)
	}
	for _, instance := range live {
		if instance.Error != "" || instance.Resource == nil {
			t.Fatal("live observation failed")
		}
		if _, err = adapter.Logs(ctx, instance.ResourceID, 20); err != nil {
			t.Fatal(err)
		}
		after, err := adapter.Inspect(ctx, instance.ResourceID)
		if err != nil {
			t.Fatal(err)
		}
		prior := before[instance.ResourceID]
		if after.Image != prior.Image || after.Description != prior.Description || after.Port != prior.Port || after.URL != prior.URL || snapshots[instance.ResourceID] != coolifyConfigurationFingerprint(t, ctx, proxy.URL, instance.ResourceID) {
			t.Fatal("operator configuration changed")
		}
	}
	if _, err = c.Enqueue(ctx, app.ID, "live-observed-blocked"); err == nil {
		t.Fatal("observe app deployed")
	}
	for _, component := range app.Manifest.Components {
		if _, err = c.EnqueueRestart(ctx, app.ID, "blocked-"+component.Name, controller.RestartRequest{Component: component.Name, Ordinal: 1}, app.Version); err == nil {
			t.Fatal("observed restart allowed")
		}
		if before[component.ResourceID].ArtifactKind == "source" {
			if _, err = c.EnableManagement(ctx, app.ID, component.Name, app.Version); err == nil {
				t.Fatal("native source handoff allowed")
			}
		}
	}
	groups, err := c.Store.ApplicationGroups(ctx)
	if err != nil || len(groups) != 1 || len(groups[0].Environments) != 1 || groups[0].Environments[0].ID != app.ID {
		t.Fatal("logical environment projection changed identity")
	}
	var releases int
	if err = c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_deployments").Scan(&releases); err != nil || releases != 0 || writes.Load() != 0 {
		t.Fatal("grouping attempted provider mutation or created releases")
	}
	t.Log("explicit existing staging image/source grouped; health/logs and native source/trigger/domain/variable/storage fingerprints preserved; deploy/restart/source handoff blocked; zero provider writes")
}

// Compare only persistent application configuration plus full environment/storage
// records. Runtime status and update timestamps may change independently. Secrets
// stay in memory and assertions never print provider bodies or fingerprint inputs.
func coolifyConfigurationFingerprint(t *testing.T, ctx context.Context, base, id string) [32]byte {
	t.Helper()
	read := func(path string) any {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/applications/"+url.PathEscape(id)+path, nil)
		if err != nil {
			t.Fatal("invalid snapshot request")
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal("snapshot request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("snapshot endpoint unavailable", response.StatusCode)
		}
		var value any
		if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&value); err != nil {
			t.Fatal("invalid snapshot response")
		}
		return value
	}
	application, ok := read("").(map[string]any)
	if !ok {
		t.Fatal("invalid application snapshot")
	}
	stable := map[string]any{}
	for _, key := range []string{"uuid", "name", "description", "environment_id", "server_id", "destination_id", "source_id", "build_pack", "git_repository", "git_branch", "git_commit_sha", "dockerfile_location", "base_directory", "publish_directory", "fqdn", "ports_exposes", "ports_mappings", "docker_registry_image_name", "docker_registry_image_tag", "custom_docker_run_options", "settings"} {
		stable[key] = application[key]
	}
	// Provider collection order and timestamps are not configuration identity.
	var normalize func(any) any
	normalize = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			result := map[string]any{}
			for key, item := range v {
				if key != "updated_at" && key != "created_at" {
					result[key] = normalize(item)
				}
			}
			return result
		case []any:
			items := []string{}
			for _, item := range v {
				raw, _ := json.Marshal(normalize(item))
				items = append(items, string(raw))
			}
			sort.Strings(items)
			return items
		default:
			return value
		}
	}
	stable["settings"] = normalize(stable["settings"])
	stable["variables"] = normalize(read("/envs"))
	stable["storages"] = normalize(read("/storages"))
	raw, err := json.Marshal(stable)
	if err != nil {
		t.Fatal("invalid configuration snapshot")
	}
	return sha256.Sum256(raw)
}

func TestCoolifyConfigurationFingerprintTracksAuthorityAndSecrets(t *testing.T) {
	variable := "private-a"
	trigger := true
	runtime := "running:healthy"
	reversed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any
		switch {
		case strings.HasSuffix(r.URL.Path, "/envs"):
			items := []any{map[string]any{"key": "SECRET", "value": variable}, map[string]any{"key": "MODE", "value": "test"}}
			if reversed {
				items[0], items[1] = items[1], items[0]
			}
			result = items
		case strings.HasSuffix(r.URL.Path, "/storages"):
			result = map[string]any{"file_storages": []any{}, "persistent_storages": []any{map[string]any{"mount_path": "/data", "name": "existing-volume"}}}
		default:
			result = map[string]any{"uuid": "fixture", "fqdn": "https://existing.test", "status": runtime, "updated_at": time.Now().String(), "settings": map[string]any{"is_auto_deploy_enabled": trigger}}
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	baseline := coolifyConfigurationFingerprint(t, context.Background(), server.URL, "fixture")
	runtime = "restarting"
	reversed = true
	if coolifyConfigurationFingerprint(t, context.Background(), server.URL, "fixture") != baseline {
		t.Fatal("runtime/list order changed configuration identity")
	}
	variable = "private-b"
	if coolifyConfigurationFingerprint(t, context.Background(), server.URL, "fixture") == baseline {
		t.Fatal("secret change was not detected")
	}
	variable = "private-a"
	trigger = false
	if coolifyConfigurationFingerprint(t, context.Background(), server.URL, "fixture") == baseline {
		t.Fatal("native trigger change was not detected")
	}
}
