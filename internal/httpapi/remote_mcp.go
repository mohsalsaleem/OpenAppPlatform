package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/mcp"
)

func (s *Server) readRequest(r *http.Request) bool {
	if r.Method == "GET" || r.Method == "HEAD" {
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return !s.Preview && r.Method == "POST" && len(parts) == 5 && parts[2] == "applications" && parts[4] == "mcp"
}

type inProcessReadTransport struct {
	handler     http.Handler
	application string
}

func (t inProcessReadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// No network dial, forwarded host, user URL, arbitrary path or mutation lane.
	if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/api/v1/applications/"+t.application+"/") {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	}
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, r)
	return recorder.Result(), nil
}
func (s *Server) remoteMCPRoutes(mux *http.ServeMux) {
	// Registered even when disabled so disabled installations return 404 consistently.
	mux.HandleFunc("GET /api/v1/applications/{id}/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !s.RemoteMCP || s.Preview {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !s.RemoteMCP || s.Preview {
			http.NotFound(w, r)
			return
		}
		p, _ := domain.Identity(r.Context())
		if p.Kind != "agent" || p.ApplicationID != r.PathValue("id") {
			write(w, 403, map[string]string{"message": "An application-scoped agent token is required"})
			return
		}
		if !s.accessService().Rate(r.Context(), "remote-mcp:"+p.CredentialID) {
			write(w, 429, map[string]string{"message": "Remote inspection limit reached; retry later"})
			return
		}
		content, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		accept := r.Header.Get("Accept")
		if content != "application/json" || !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			write(w, 400, map[string]string{"message": "JSON content and JSON/event-stream acceptance are required"})
			return
		}
		version := r.Header.Get("MCP-Protocol-Version")
		if version == "" {
			version = "2025-03-26"
		}
		if version != "2025-11-25" && version != "2025-03-26" {
			write(w, 400, map[string]string{"message": "Unsupported MCP protocol version"})
			return
		}
		var message map[string]json.RawMessage
		if err := decode(w, r, &message); err != nil || string(message["jsonrpc"]) != `"2.0"` {
			write(w, 400, map[string]string{"message": "One JSON-RPC 2.0 object is required"})
			return
		}
		raw, err := json.Marshal(message)
		if err != nil {
			write(w, 400, map[string]string{"message": "Invalid RPC object"})
			return
		}
		var out bytes.Buffer
		server := mcp.Server{URL: "http://127.0.0.1", Token: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), AllowedTools: []string{"get_diagnostic_context", "inspect_instances"}, ProtocolVersion: version, HTTP: &http.Client{Timeout: 15 * time.Second, Transport: inProcessReadTransport{handler: s.auth(s.routes()), application: p.ApplicationID}}}
		if err = server.Serve(r.Context(), bytes.NewReader(raw), &out); err != nil {
			write(w, 400, map[string]string{"message": "RPC request failed"})
			return
		}
		if out.Len() == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write(out.Bytes())
	})
}
