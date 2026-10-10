package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRollbackHTTPRespectsReadScopeAndRejectsUnreviewedMutation(t *testing.T) {
	c, f, app, old, _ := rollbackHistory(t)
	ctx := context.Background()
	service := access.Service{Pool: c.Store.Pool}
	owner, err := service.Register(ctx, "rollback-owner@example.invalid", "Owner", "rollback-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	read, err := service.Issue(ctx, owner, "agent", "Read", "read", app.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	operate, err := service.Issue(ctx, owner, "agent", "Operate", "operate", app.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer server.Close()
	call := func(method, path, token string, body any) (int, []byte) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "rollback-http-action")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var output json.RawMessage
		if err = json.NewDecoder(response.Body).Decode(&output); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, output
	}
	path := "/api/v1/applications/" + app.ID
	code, raw := call("GET", path+"/rollback-plan?releaseId="+old.ID+"&expectedVersion="+strconv.FormatInt(app.Version, 10), read, nil)
	if code != 200 {
		t.Fatal("read agent cannot inspect its plan", code)
	}
	var plan map[string]any
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	before := f.deploys
	body := map[string]any{"releaseId": old.ID, "planHash": plan["planHash"], "expectedVersion": app.Version, "acknowledgeDataCompatibility": true}
	if code, _ = call("POST", path+"/rollbacks", read, body); code != 403 {
		t.Fatal("read token can enqueue rollback", code)
	}
	body["acknowledgeDataCompatibility"] = false
	if code, _ = call("POST", path+"/rollbacks", operate, body); code != 422 {
		t.Fatal("unacknowledged rollback accepted", code)
	}
	if code, _ = call("GET", strings.Replace(path, app.ID, strings.Repeat("0", 32), 1)+"/rollback-plan?releaseId="+old.ID+"&expectedVersion=2", read, nil); code != 404 {
		t.Fatal("agent escaped app scope", code)
	}
	if f.deploys != before {
		t.Fatal("plan/denial mutated provider")
	}
}
