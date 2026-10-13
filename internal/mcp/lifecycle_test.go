package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLifecycleExplicitOptInAndFixedRoutes(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/v1/action-plans/plan-1/execute" {
			t.Error("unexpected route", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 32) {
			t.Error("missing agent auth")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		w.Write([]byte(`{"id":"plan-1","result":{"operationId":"operation-1"}}`))
	}))
	defer api.Close()
	s := Server{URL: api.URL, Token: strings.Repeat("a", 32)}
	if _, err := s.call(context.Background(), "execute_action_plan", json.RawMessage(`{"planId":"plan-1"}`)); err == nil || calls != 0 {
		t.Fatal("default server executed")
	}
	s.Operate = true
	s.AllowedTools = []string{"inspect_instances"}
	if _, err := s.call(context.Background(), "execute_action_plan", json.RawMessage(`{"planId":"plan-1"}`)); err == nil || calls != 0 {
		t.Fatal("allowlist bypass")
	}
	s.AllowedTools = nil
	for _, args := range []string{`{"planId":"../escape"}`, `{"planId":"plan-1","request":{"action":"create"}}`, `null`, `{"planId":"plan-1","url":"https://attacker.invalid"}`} {
		if _, err := s.call(context.Background(), "execute_action_plan", json.RawMessage(args)); err == nil || calls != 0 {
			t.Fatal("invalid arguments reached API")
		}
	}
	if _, err := s.call(context.Background(), "execute_action_plan", json.RawMessage(`{"planId":"plan-1"}`)); err != nil || calls != 1 {
		t.Fatal("valid execution failed", err)
	}
}
