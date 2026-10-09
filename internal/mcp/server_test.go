package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPReadsThroughAuthenticatedAPIAndRejectsMutations(t *testing.T) {
	token := strings.Repeat("s", 32)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" {
			t.Fatal("mutation attempted")
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("auth missing")
		}
		w.Write([]byte(`[{"id":"application"}]`))
	}))
	defer srv.Close()
	s := Server{URL: srv.URL, Token: token}
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"list_applications\",\"arguments\":{}}}\n{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"deploy_application\",\"arguments\":{}}}\n")
	var output bytes.Buffer
	if e := s.Serve(context.Background(), input, &output); e != nil {
		t.Fatal(e)
	}
	if requests != 1 || strings.Contains(output.String(), token) {
		t.Fatal("unsafe requests or leaked token")
	}
	decoder := json.NewDecoder(&output)
	for range 3 {
		var response map[string]any
		if e := decoder.Decode(&response); e != nil {
			t.Fatal(e)
		}
		if response["jsonrpc"] != "2.0" {
			t.Fatal("wrong protocol")
		}
	}
}
func TestMCPRejectsUnsafePathsAndExtraArguments(t *testing.T) {
	s := Server{}
	for _, args := range []string{`{"applicationId":"../secrets"}`, `{"applicationId":"valid","targetId":"extra"}`, `{"applicationId":"valid","token":"secret"}`, `null`} {
		if _, e := s.call(context.Background(), "get_application", json.RawMessage(args)); e == nil {
			t.Fatal("unsafe arguments accepted")
		}
	}
}
func TestMCPDoesNotFollowCredentialRedirects(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer srv.Close()
	s := Server{URL: srv.URL, Token: strings.Repeat("s", 32)}
	if _, e := s.read(context.Background(), "/applications"); e == nil || hit {
		t.Fatal("followed credential redirect")
	}
}
