package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRollbackToolsUseExactRoutesAndBoundedAuthority(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "GET" {
			if r.URL.Path != "/api/v1/applications/app-1/rollback-plan" || r.URL.Query().Get("releaseId") != "release-1" || r.URL.Query().Get("expectedVersion") != "2" {
				t.Error("incorrect rollback plan route")
			}
		} else {
			if r.Method != "POST" || r.URL.Path != "/api/v1/applications/app-1/rollbacks" || r.Header.Get("Idempotency-Key") != "rollback-1" {
				t.Error("incorrect rollback execution route")
			}
			var input map[string]any
			json.NewDecoder(r.Body).Decode(&input)
			if input["acknowledgeDataCompatibility"] != true || input["planHash"] != strings.Repeat("a", 64) {
				t.Error("rollback lost review parameters")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"operation-1","state":"queued"}`))
	}))
	defer api.Close()
	s := Server{URL: api.URL, Token: strings.Repeat("b", 32)}
	execute := json.RawMessage(`{"applicationId":"app-1","releaseId":"release-1","expectedVersion":2,"planHash":"` + strings.Repeat("a", 64) + `","acknowledgeDataCompatibility":true,"idempotencyKey":"rollback-1"}`)
	if _, err := s.call(context.Background(), "execute_rollback", execute); err == nil || calls != 0 {
		t.Fatal("default MCP executed rollback")
	}
	s.Operate = true
	s.AllowedTools = []string{"plan_rollback"}
	if _, err := s.call(context.Background(), "execute_rollback", execute); err == nil || calls != 0 {
		t.Fatal("allowlist bypass")
	}
	if _, err := s.call(context.Background(), "plan_rollback", json.RawMessage(`{"applicationId":"app-1","releaseId":"release-1","expectedVersion":2}`)); err != nil {
		t.Fatal(err)
	}
	s.AllowedTools = nil
	if _, err := s.call(context.Background(), "execute_rollback", json.RawMessage(strings.Replace(string(execute), `"acknowledgeDataCompatibility":true`, `"acknowledgeDataCompatibility":false`, 1))); err == nil || calls != 1 {
		t.Fatal("unacknowledged rollback reached API")
	}
	if _, err := s.call(context.Background(), "execute_rollback", execute); err != nil || calls != 2 {
		t.Fatal("reviewed rollback failed", err)
	}
}
