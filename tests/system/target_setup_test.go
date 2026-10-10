package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"strings"
	"testing"
)

func TestConnectionCheckAndLayoutAreReadOnlyAndBounded(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{"native": {ID: "native", Name: "Existing", ArtifactKind: "source", Status: "running:healthy"}}}
	c, srv := setup(t, f)
	m := manifest()
	m.Name = "layout-source"
	m.Components[0].Env = map[string]string{"PRIVATE_MARKER": "do-not-copy-this-value"}
	m.Components[0].ServiceEndpoints = map[string]string{"web": "https://private.invalid/?token=not-a-template"}
	app, e := c.Store.CreateApplication(context.Background(), m)
	if e != nil {
		t.Fatal(e)
	}
	source, _ := c.Store.Target(context.Background(), "fixture")
	source.URL = "https://operator.invalid"
	source.ProjectID = "project"
	source.ServerID = "server"
	source.TokenEnv = "COOLIFY_TOKEN"
	c.Store.SaveTarget(context.Background(), source)
	code, raw := request(t, srv, http.MethodPost, "/api/v1/targets/scopes", "", map[string]string{"id": "new-environment", "name": "Existing preview", "sourceTargetId": "fixture", "environment": "preview"})
	if code != 201 || strings.Contains(string(raw), "tokenEnv") {
		t.Fatal("safe scope registration failed", code)
	}
	derived, e := c.Store.Target(context.Background(), "new-environment")
	if e != nil || derived.URL != source.URL || derived.TokenEnv != source.TokenEnv || derived.ProjectID != source.ProjectID {
		t.Fatal("trusted authority changed")
	}
	code, _ = request(t, srv, http.MethodPost, "/api/v1/targets/scopes", "", map[string]string{"id": "another-alias", "name": "Duplicate", "sourceTargetId": "fixture", "environment": "preview"})
	if code != 409 {
		t.Fatal("duplicate scope accepted", code)
	}
	code, _ = request(t, srv, http.MethodPost, "/api/v1/targets/scopes", "", map[string]string{"id": "inject", "name": "Inject", "sourceTargetId": "fixture", "environment": "another", "url": "https://attacker.invalid"})
	if code != 422 {
		t.Fatal("browser could change operator authority")
	}
	code, raw = request(t, srv, http.MethodGet, "/api/v1/targets/fixture/connection", "", nil)
	if code != 200 || f.deploys != 0 || len(f.resources) != 1 {
		t.Fatal("connection check mutated provider", code)
	}
	var check struct {
		ResourceCount int `json:"resourceCount"`
	}
	json.Unmarshal(raw, &check)
	if check.ResourceCount != 1 {
		t.Fatal("wrong resource scope")
	}
	code, raw = request(t, srv, http.MethodGet, "/api/v1/applications/"+app.ID+"/layout", "", nil)
	if code != 200 || strings.Contains(string(raw), "do-not-copy") || strings.Contains(string(raw), "private.invalid") || strings.Contains(string(raw), "nginx") {
		t.Fatal("layout exposed configuration")
	}
	var layout struct {
		DefinitionVersion int64            `json:"definitionVersion"`
		Components        []map[string]any `json:"components"`
	}
	json.Unmarshal(raw, &layout)
	if layout.DefinitionVersion != app.Version || len(layout.Components) != 1 || len(layout.Components[0]) != 2 || layout.Components[0]["name"] != "web" {
		t.Fatal("layout contract changed")
	}
}
