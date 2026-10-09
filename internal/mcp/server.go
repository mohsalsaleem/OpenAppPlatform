// Package mcp exposes read-only application operations over newline JSON-RPC.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Server struct {
	URL, Token string
	HTTP       *http.Client
}
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9-]{1,100}$`)

func (s *Server) Validate() error {
	u, e := url.Parse(s.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid platform URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return errors.New("platform URL requires HTTPS except localhost")
	}
	if len(s.Token) < 24 {
		return errors.New("platform access token is required")
	}
	return nil
}
func tools() []tool {
	schema := func(field string) any {
		properties := map[string]any{}
		required := []string{}
		if field != "" {
			properties[field] = map[string]string{"type": "string"}
			required = append(required, field)
		}
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	return []tool{
		{"list_applications", "List applications and their desired configuration. This operation does not deploy anything.", schema("")},
		{"get_application", "Read an application's configuration and definition version.", schema("applicationId")},
		{"list_deployments", "Read recent releases, component outcomes, and frozen image references.", schema("applicationId")},
		{"get_deployment", "Read the durable deployment phases and observed provider state. Success is not ongoing health monitoring.", schema("deploymentId")},
		{"inspect_target", "Read resources inside one configured target. Does not create or modify resources.", schema("targetId")},
		{"plan_deployment", "Read the current definition and summarize standard deployment effects. This operation does not execute the plan.", schema("applicationId")},
	}
}
func (s *Server) read(ctx context.Context, path string) (json.RawMessage, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.URL, "/")+"/api/v1"+path, nil)
	if e != nil {
		return nil, errors.New("invalid platform request")
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, e := client.Do(req)
	if e != nil {
		return nil, errors.New("platform is unavailable")
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || len(b) > 2<<20 {
		return nil, errors.New("platform response exceeds limit or could not be read")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("platform returned HTTP %d", res.StatusCode)
	}
	if !json.Valid(b) {
		return nil, errors.New("platform returned invalid JSON")
	}
	return b, nil
}
func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var args struct {
		ApplicationID string `json:"applicationId"`
		DeploymentID  string `json:"deploymentId"`
		TargetID      string `json:"targetId"`
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	} else if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("tool arguments must be an object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&args); e != nil {
		return nil, errors.New("invalid tool arguments")
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, errors.New("tool arguments must contain one object")
	}
	field, value := "", ""
	switch name {
	case "list_applications":
		if args.ApplicationID != "" || args.DeploymentID != "" || args.TargetID != "" {
			return nil, errors.New("list_applications takes no arguments")
		}
		return s.read(ctx, "/applications")
	case "get_application", "list_deployments", "plan_deployment":
		field, value = "applicationId", args.ApplicationID
		if args.DeploymentID != "" || args.TargetID != "" {
			return nil, errors.New("unexpected tool arguments")
		}
	case "get_deployment":
		field, value = "deploymentId", args.DeploymentID
		if args.ApplicationID != "" || args.TargetID != "" {
			return nil, errors.New("unexpected tool arguments")
		}
	case "inspect_target":
		field, value = "targetId", args.TargetID
		if args.ApplicationID != "" || args.DeploymentID != "" {
			return nil, errors.New("unexpected tool arguments")
		}
	default:
		return nil, errors.New("unknown or mutating tool; this server is read-only")
	}
	if !identifier.MatchString(value) {
		return nil, fmt.Errorf("a valid %s is required", field)
	}
	switch name {
	case "get_application":
		return s.read(ctx, "/applications/"+url.PathEscape(value))
	case "list_deployments":
		return s.read(ctx, "/applications/"+url.PathEscape(value)+"/deployments")
	case "get_deployment":
		return s.read(ctx, "/deployments/"+url.PathEscape(value))
	case "inspect_target":
		return s.read(ctx, "/targets/"+url.PathEscape(value)+"/resources")
	case "plan_deployment":
		b, e := s.read(ctx, "/applications/"+url.PathEscape(value))
		if e != nil {
			return nil, e
		}
		var app struct {
			Version  int64           `json:"version"`
			Manifest json.RawMessage `json:"manifest"`
		}
		if e = json.Unmarshal(b, &app); e != nil {
			return nil, e
		}
		return map[string]any{"applicationId": value, "expectedVersion": app.Version, "definition": app.Manifest, "strategy": "standard", "effect": "Deployment uses the selected adapter's standard lifecycle. Existing managed components may restart and downtime is possible.", "executed": false}, nil
	}
	return nil, errors.New("unknown tool")
}
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if e := s.Validate(); e != nil {
		return e
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	encoder := json.NewEncoder(output)
	reply := func(id json.RawMessage, result any, code int, message string) error {
		response := map[string]any{"jsonrpc": "2.0", "id": id}
		if code != 0 {
			response["error"] = map[string]any{"code": code, "message": message}
		} else {
			response["result"] = result
		}
		return encoder.Encode(response)
	}
	for scanner.Scan() {
		if e := ctx.Err(); e != nil {
			return e
		}
		var r request
		if e := json.Unmarshal(scanner.Bytes(), &r); e != nil {
			if e = reply(json.RawMessage("null"), nil, -32700, "Invalid JSON-RPC message"); e != nil {
				return e
			}
			continue
		}
		if len(r.ID) == 0 {
			continue
		}
		var rpcID any
		if json.Unmarshal(r.ID, &rpcID) != nil {
			if e := reply(json.RawMessage("null"), nil, -32600, "Invalid request ID"); e != nil {
				return e
			}
			continue
		}
		switch rpcID.(type) {
		case nil, string, float64:
		default:
			if e := reply(json.RawMessage("null"), nil, -32600, "Invalid request ID"); e != nil {
				return e
			}
			continue
		}
		if r.JSONRPC != "2.0" {
			if e := reply(r.ID, nil, -32600, "JSON-RPC 2.0 is required"); e != nil {
				return e
			}
			continue
		}
		var result any
		code, message := 0, ""
		switch r.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]bool{"listChanged": false}}, "serverInfo": map[string]string{"name": "OpenAppPlatform", "version": "0.2.0-dev"}}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": tools()}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if e := json.Unmarshal(r.Params, &params); e != nil {
				code, message = -32602, "Invalid tool request"
				break
			}
			data, e := s.call(ctx, params.Name, params.Arguments)
			if e != nil {
				result = map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": e.Error()}}}
			} else {
				b, e := json.Marshal(data)
				if e != nil {
					return e
				}
				result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(b)}}}
			}
		default:
			code, message = -32601, "Method not found"
		}
		if e := reply(r.ID, result, code, message); e != nil {
			return e
		}
	}
	return scanner.Err()
}
