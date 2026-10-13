// Package cli provides a non-interactive client of the public lifecycle API.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/docs"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

const maxBody = 2 << 20

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

type Failure struct {
	Code        string   `json:"code"`
	Message     string   `json:"message"`
	Retryable   bool     `json:"retryable"`
	NextActions []string `json:"nextActions"`
	HTTPStatus  int      `json:"httpStatus,omitempty"`
}
type Result struct {
	SchemaVersion  int      `json:"schemaVersion"`
	OK             bool     `json:"ok"`
	Data           any      `json:"data,omitempty"`
	Error          *Failure `json:"error,omitempty"`
	OperationID    string   `json:"operationId,omitempty"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
}
type Client struct {
	URL, Token, Session string
	HTTP                *http.Client
}

func failure(code, message string, retry bool, actions ...string) *Failure {
	return &Failure{Code: code, Message: message, Retryable: retry, NextActions: actions}
}
func (f *Failure) Error() string { return f.Message }

// Call never follows redirects, retries mutations, or emits transport/private errors.
func (c Client) Call(ctx context.Context, method, path string, body []byte, key string) (json.RawMessage, *Failure) {
	u, err := url.Parse(c.URL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return nil, failure("invalid_endpoint", "Use an HTTPS origin or a loopback HTTP origin", false, "configure_endpoint")
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return nil, failure("invalid_path", "Only public API paths are supported", false, "inspect_schema")
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, failure("invalid_request", "Cannot construct request", false, "inspect_schema")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" && c.Session != "" {
		return nil, failure("ambiguous_identity", "Select an agent token or an owner session, not both", false, "select_identity")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else if c.Session != "" {
		name := "__Host-oap-session"
		if u.Scheme == "http" {
			name = "oap-session"
		}
		req.AddCookie(&http.Cookie{Name: name, Value: c.Session})
		req.Header.Set("X-OAP-CSRF", "1")
	} else {
		return nil, failure("credentials_required", "Provide OAP_API_TOKEN or a private owner session file", false, "authenticate")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	hc := http.Client{Timeout: 20 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, failure("wait_timeout", "Deadline or interruption reached; inspect the existing operation", true, "inspect_operation", "wait_operation")
		}
		return nil, failure("transport_unavailable", "Request outcome is unknown; inspect before retrying a mutation with the same key", true, "inspect_operation", "retry_same_key")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return nil, failure("invalid_response", "Response is unavailable or exceeds the bounded response size", false, "inspect_operation")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &payload)
		f := failure("request_failed", "The API rejected this request", false, "inspect_schema")
		switch response.StatusCode {
		case 400, 422:
			f = failure("invalid_request", "Review request fields and runtime constraints", false, "inspect_schema", "inspect_capabilities")
		case 401:
			f = failure("unauthorized", "Authenticate with a current, enabled credential", false, "authenticate")
		case 403:
			f = failure("forbidden", "This identity cannot perform the requested action", false, "inspect_capabilities", "request_owner_grant")
		case 404:
			f = failure("not_found", "Resource is absent or outside this identity's scope", false, "check_context")
		case 409:
			f = failure("conflict", "Refresh the definition and operation state before replanning", false, "refresh_application", "inspect_operation", "create_new_plan")
		case 429:
			f = failure("rate_limited", "Wait before retrying; reuse the mutation key", true, "wait", "retry_same_key")
		default:
			if response.StatusCode >= 500 {
				f = failure("server_unavailable", "Inspect state before retrying a mutation with the same key", true, "inspect_operation", "retry_same_key")
			}
		}
		// Codes are bounded identifiers; server text may contain provider/user input.
		if idPattern.MatchString(payload.Code) {
			f.Code = payload.Code
		}
		f.HTTPStatus = response.StatusCode
		return nil, f
	}
	if !json.Valid(raw) {
		return nil, failure("invalid_response", "Expected one JSON response", false, "inspect_operation")
	}
	return json.RawMessage(raw), nil
}

// Wait observes an existing durable operation; timeout never cancels it.
func (c Client) Wait(ctx context.Context, id string, interval time.Duration) (json.RawMessage, *Failure) {
	if !idPattern.MatchString(id) {
		return nil, failure("invalid_operation", "Provide a valid operation ID", false, "check_context")
	}
	var last json.RawMessage
	for {
		raw, f := c.Call(ctx, "GET", "/api/v1/deployments/"+id, nil, "")
		if f != nil {
			if len(raw) == 0 {
				raw = last
			}
			return raw, f
		}
		last = raw
		var d domain.Deployment
		if json.Unmarshal(raw, &d) != nil || d.ID != id || d.State == "" {
			return nil, failure("invalid_response", "Operation identity or state is missing", false, "inspect_operation")
		}
		if domain.Terminal(d.State) {
			if d.State == "succeeded" {
				return raw, nil
			}
			return raw, failure("operation_"+d.State, "Operation stopped; inspect its evidence before choosing a recovery action", false, "inspect_operation", "inspect_instances", "inspect_capabilities")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return raw, failure("wait_timeout", "Wait ended; the durable operation may still be running", true, "inspect_operation", "wait_operation")
		case <-timer.C:
		}
	}
}
func readInput(path string, in io.Reader) ([]byte, error) {
	var reader io.Reader = in
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
	}
	raw, err := io.ReadAll(io.LimitReader(reader, 65537))
	if err != nil || len(raw) > 65536 || !json.Valid(raw) {
		return nil, errors.New("invalid input")
	}
	return raw, nil
}
func sessionFile(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 4096 {
		return "", errors.New("private regular file required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var v struct {
		Session string `json:"session"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Session == "" || strings.ContainsAny(v.Session, "\r\n;") {
		return "", errors.New("invalid session")
	}
	return v.Session, nil
}

// Run writes exactly one JSON envelope. Exit: 0 success, 2 input, 3 denied,
// 4 conflict/rejected, 5 unavailable, 6 stopped operation, 7 wait timeout.
func Run(ctx context.Context, args []string, getenv func(string) string, in io.Reader, out io.Writer) int {
	result := Result{SchemaVersion: 1}
	finish := func(data any, f *Failure, code int) int {
		result.OK = f == nil
		result.Data = data
		result.Error = f
		_ = json.NewEncoder(out).Encode(result)
		return code
	}
	fs := flag.NewFlagSet("oap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	base := getenv("OAP_URL")
	if base == "" {
		base = "http://127.0.0.1:8787"
	}
	endpoint := fs.String("url", base, "API origin")
	app := fs.String("app", "", "application ID")
	target := fs.String("target", "", "assert target ID")
	environment := fs.String("environment", "", "assert environment")
	file := fs.String("file", "", "JSON file or - for stdin")
	key := fs.String("key", "", "stable mutation key")
	operation := fs.String("operation", "", "operation ID")
	timeout := fs.Duration("timeout", 2*time.Minute, "bounded wait duration")
	credential := fs.String("credential-file", getenv("OAP_CREDENTIAL_FILE"), "origin-bound private credential")
	output := fs.String("out", "", "new private credential file")
	component := fs.String("component", "", "select incident logs for one component")
	ordinal := fs.Int("ordinal", 0, "select incident log instance (1..4)")
	plan := fs.String("plan", "", "deployment plan ID")
	credentialID := fs.String("credential", "", "agent credential ID")
	session := fs.String("session-file", getenv("OAP_SESSION_FILE"), "private owner session JSON")
	if fs.Parse(args) != nil || len(fs.Args()) != 1 || *timeout <= 0 || *timeout > 30*time.Minute {
		return finish(nil, failure("invalid_arguments", "Provide flags before one command; use help for command contracts", false, "help"), 2)
	}
	command := fs.Args()[0]
	if command != "incident" && (*component != "" || *ordinal != 0) {
		return finish(nil, failure("invalid_arguments", "Component/ordinal flags are only supported by incident; release selection belongs in the action request", false, "inspect_schema"), 2)
	}

	if command == "schema" {
		return finish(json.RawMessage(docs.OpenAPI), nil, 0)
	}
	if command == "help" {
		return finish(map[string]any{"commands": []string{"help", "schema", "meta", "targets", "capabilities", "apps", "create", "get", "update", "instances", "diagnostics", "deploy", "operation", "wait", "verify", "setup", "login", "grant", "app-grant", "revoke", "agent-control", "set-agent-control", "plan", "get-plan", "approve", "execute-plan", "target-capabilities", "resources", "whoami", "validate-spec", "diff-spec", "export-spec", "incident", "action-plan", "get-action-plan", "review-action-input", "approve-action", "execute-action"}, "flagsBeforeCommand": true, "contextFlags": []string{"url", "app", "target", "environment", "operation", "component", "ordinal"}, "input": "--file path.json or --file -; deploy requires --key and expectedVersion > 0; agent create requires --key", "auth": "OAP_API_TOKEN or --session-file; no credentials in flags/output", "wait": "--timeout 1s..30m; observes without cancelling", "verification": "current instance health only, not end-to-end application behavior", "exitCodes": map[string]int{"success": 0, "input": 2, "denied": 3, "rejected": 4, "unavailable": 5, "operationStopped": 6, "timeout": 7}}, nil, 0)
	}
	c := Client{URL: *endpoint, Token: getenv("OAP_API_TOKEN")}
	if *session != "" {
		var err error
		c.Session, err = sessionFile(*session)
		if err != nil {
			return finish(nil, failure("invalid_session_file", "Use a private regular JSON file containing the owner session", false, "authenticate"), 2)
		}
	}
	emitFailure := func(data any, f *Failure) int {
		code := 4
		switch {
		case f.Code == "wait_timeout":
			code = 7
		case strings.HasPrefix(f.Code, "operation_"):
			code = 6
		case f.HTTPStatus == 401 || f.HTTPStatus == 403 || f.HTTPStatus == 404 || f.Code == "credentials_required":
			code = 3
		case f.Retryable || f.Code == "invalid_response":
			code = 5
		case f.HTTPStatus == 0:
			code = 2
		}
		return finish(data, f, code)
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if *credential != "" {
		if c.Token != "" || c.Session != "" {
			return emitFailure(nil, failure("ambiguous_identity", "Select one credential source", false, "select_identity"))
		}
		stored, f := readCredential(*credential, *endpoint)
		if f != nil {
			return emitFailure(nil, f)
		}
		c.Token = stored.Token
		c.Session = stored.Session
	}
	if command == "setup" || command == "login" || command == "grant" || command == "app-grant" {
		if *file == "" {
			return emitFailure(nil, failure("input_required", "Provide authentication/grant arguments as private JSON", false, "inspect_schema"))
		}
		body, err := readInput(*file, in)
		if err != nil {
			return emitFailure(nil, failure("invalid_input", "Provide one bounded JSON document", false, "inspect_schema"))
		}
		if command != "grant" && command != "app-grant" && (c.Token != "" || c.Session != "") {
			return emitFailure(nil, failure("ambiguous_identity", "Owner login/setup uses its own input, not an existing identity", false, "select_identity"))
		}
		reserved, f := reserveCredential(*output)
		if f != nil {
			return emitFailure(nil, f)
		}
		saved := false
		defer func() {
			reserved.Close()
			if !saved {
				os.Remove(*output)
			}
		}()
		var stored credentialFile
		var public any
		if command == "grant" || command == "app-grant" {
			grantPath := "/api/v1/access/onboarding-grants"
			if command == "app-grant" {
				grantPath = "/api/v1/access/credentials"
			}
			raw, f := c.Call(ctx, "POST", grantPath, body, "")
			if f != nil {
				return emitFailure(nil, f)
			}
			var issued struct {
				Token            string `json:"token"`
				CredentialID     string `json:"credentialId"`
				ExpiresInSeconds int    `json:"expiresInSeconds"`
			}
			if json.Unmarshal(raw, &issued) != nil || issued.Token == "" || issued.CredentialID == "" {
				return emitFailure(nil, failure("invalid_response", "Issued agent credential response is incomplete", false, "inspect_audit"))
			}
			base, _ := origin(*endpoint)
			stored = credentialFile{SchemaVersion: 1, Origin: base, Token: issued.Token, CredentialID: issued.CredentialID}
			public = map[string]any{"credentialId": issued.CredentialID, "expiresInSeconds": issued.ExpiresInSeconds}
		} else {
			var raw json.RawMessage
			stored, raw, f = ownerAuthenticate(ctx, *endpoint, command, getenv("OAP_SETUP_TOKEN"), body)
			if f != nil {
				return emitFailure(nil, f)
			}
			public = raw
		}
		if f = saveCredential(reserved, stored); f != nil {
			return emitFailure(map[string]string{"credentialId": stored.CredentialID}, f)
		}
		saved = true
		return finish(public, nil, 0)
	}

	if command == "wait" || command == "operation" {
		result.OperationID = *operation
		if !idPattern.MatchString(*operation) {
			return emitFailure(nil, failure("invalid_operation", "Provide --operation ID", false, "check_context"))
		}
		var raw json.RawMessage
		var f *Failure
		if *app != "" || *target != "" || *environment != "" {
			observation, requestFailure := c.Call(ctx, "GET", "/api/v1/deployments/"+*operation, nil, "")
			if requestFailure != nil {
				return emitFailure(nil, requestFailure)
			}
			var d domain.Deployment
			if json.Unmarshal(observation, &d) != nil || d.ID != *operation {
				return emitFailure(nil, failure("invalid_response", "Operation identity is missing", false, "inspect_operation"))
			}
			if (*app != "" && d.ApplicationID != *app) || (*target != "" && d.Manifest.TargetID != *target) || (*environment != "" && d.Manifest.Environment != *environment) {
				return emitFailure(nil, failure("context_mismatch", "Operation does not match requested application context", false, "check_context"))
			}
		}
		if command == "wait" {
			raw, f = c.Wait(ctx, *operation, time.Second)
		} else {
			raw, f = c.Call(ctx, "GET", "/api/v1/deployments/"+*operation, nil, "")
		}
		if f != nil {
			return emitFailure(raw, f)
		}
		return finish(raw, nil, 0)
	}
	method, path := "GET", "/api/v1/"+command
	switch command {
	case "meta", "targets":
	case "whoami":
		path = "/api/v1/auth/me"
	case "agent-control":
		path = "/api/v1/access/agent-control"
	case "set-agent-control":
		method = "PUT"
		path = "/api/v1/access/agent-control"
	case "revoke":
		if !idPattern.MatchString(*credentialID) {
			return emitFailure(nil, failure("invalid_credential", "Provide --credential ID", false, "check_context"))
		}
		method = "DELETE"
		path = "/api/v1/access/credentials/" + *credentialID
	case "target-capabilities", "resources":
		if !idPattern.MatchString(*target) {
			return emitFailure(nil, failure("invalid_target", "Provide --target ID", false, "check_context"))
		}
		path = "/api/v1/targets/" + *target + "/resources"
		if command == "target-capabilities" {
			path = "/api/v1/targets/" + *target + "/capabilities"
		}
	case "plan", "get-plan", "approve", "execute-plan":
		if !idPattern.MatchString(*app) {
			return emitFailure(nil, failure("invalid_application", "Provide --app ID", false, "check_context"))
		}
		path = "/api/v1/applications/" + *app + "/plans"
		if command != "plan" {
			if !idPattern.MatchString(*plan) {
				return emitFailure(nil, failure("invalid_plan", "Provide --plan ID", false, "check_context"))
			}
			path += "/" + *plan
		}
		if command == "plan" {
			method = "POST"
		}
		if command == "approve" {
			method = "POST"
			path += "/approve"
		}
		if command == "execute-plan" {
			method = "POST"
			path += "/execute"
		}
	case "validate-spec":
		method, path = "POST", "/api/v1/specifications/validate"
	case "action-plan":
		method, path = "POST", "/api/v1/action-plans"
	case "get-action-plan", "review-action-input", "approve-action", "execute-action":
		if !idPattern.MatchString(*plan) {
			return emitFailure(nil, failure("invalid_plan", "Provide --plan ID", false, "check_context"))
		}
		path = "/api/v1/action-plans/" + *plan
		if command == "review-action-input" {
			path += "/input"
		}
		if command == "approve-action" {
			method = "POST"
			path += "/approve"
		}
		if command == "execute-action" {
			method = "POST"
			path += "/execute"
		}
	case "export-spec", "diff-spec":
		if !idPattern.MatchString(*app) {
			return emitFailure(nil, failure("invalid_application", "Provide --app ID", false, "check_context"))
		}
		path = "/api/v1/applications/" + *app + "/specification"
		if command == "diff-spec" {
			method = "POST"
			path += "/diff"
		}
	case "apps":
		path = "/api/v1/applications"
	case "create":
		method = "POST"
		path = "/api/v1/applications"
	case "get", "update", "instances", "diagnostics", "capabilities", "verify", "deploy", "incident":
		if !idPattern.MatchString(*app) {
			return emitFailure(nil, failure("invalid_application", "Provide --app ID", false, "check_context"))
		}
		path = "/api/v1/applications/" + *app
		switch command {
		case "update":
			method = "PUT"
		case "instances", "diagnostics", "capabilities", "incident":
			path += "/" + command
		case "verify":
			path += "/instances"
		case "deploy":
			method = "POST"
			path += "/deployments"
		}
	default:
		return emitFailure(nil, failure("unknown_command", "Use help for supported commands", false, "help"))
	}
	if command == "incident" {
		if *ordinal < 0 || *ordinal > 4 || (*component == "" && *ordinal != 0) || (*component != "" && !idPattern.MatchString(*component)) {
			return emitFailure(nil, failure("invalid_incident_selection", "Select one component and optional ordinal 1..4", false, "check_context"))
		}
		query := url.Values{}
		if *component != "" {
			query.Set("component", *component)
		}
		if *ordinal > 0 {
			query.Set("ordinal", strconv.Itoa(*ordinal))
		}
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
	}
	var body []byte
	if method != "GET" && method != "DELETE" {
		if *file == "" {
			return emitFailure(nil, failure("input_required", "Provide --file JSON; configuration save and deploy are separate", false, "inspect_schema"))
		}
		var err error
		body, err = readInput(*file, in)
		if err != nil {
			return emitFailure(nil, failure("invalid_input", "Provide one JSON document up to 64 KiB", false, "inspect_schema"))
		}
		var v struct {
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		_ = json.Unmarshal(body, &v)
		if (command == "update" || command == "deploy" || command == "plan" || command == "set-agent-control") && v.ExpectedVersion < 1 {
			return emitFailure(nil, failure("version_required", "Supply expectedVersion > 0 from the current application", false, "refresh_application"))
		}
		if (command == "deploy" || (command == "create" && c.Token != "")) && (!idPattern.MatchString(*key) || strings.HasPrefix(*key, "plan:")) {
			return emitFailure(nil, failure("key_required", "Provide a stable --key for this release and reuse it on retries", false, "supply_idempotency_key"))
		}
	}
	// Assertions fail before mutation; they never choose a hidden default target.
	if (*target != "" || *environment != "") && command != "resources" && command != "target-capabilities" {
		var manifest domain.Manifest
		if command == "create" {
			_ = json.Unmarshal(body, &manifest)
		} else if command == "action-plan" || command == "validate-spec" {
			var input struct {
				ApplicationID string                           `json:"applicationId"`
				Specification *domain.ApplicationSpecification `json:"specification"`
			}
			if json.Unmarshal(body, &input) != nil {
				return emitFailure(nil, failure("invalid_input", "Invalid specification/action input", false, "inspect_schema"))
			}
			if input.Specification != nil {
				manifest = input.Specification.Spec
			} else if input.ApplicationID != "" && idPattern.MatchString(input.ApplicationID) {
				raw, f := c.Call(ctx, "GET", "/api/v1/applications/"+input.ApplicationID, nil, "")
				if f != nil {
					return emitFailure(nil, f)
				}
				var current domain.Application
				_ = json.Unmarshal(raw, &current)
				manifest = current.Manifest
			} else {
				return emitFailure(nil, failure("context_required", "Context assertions require specification or applicationId", false, "check_context"))
			}
		} else if *app != "" {
			raw, f := c.Call(ctx, "GET", "/api/v1/applications/"+*app, nil, "")
			if f != nil {
				return emitFailure(nil, f)
			}
			var a domain.Application
			_ = json.Unmarshal(raw, &a)
			manifest = a.Manifest
		} else {
			return emitFailure(nil, failure("context_required", "Context assertions require an application or create manifest", false, "check_context"))
		}
		if (*target != "" && manifest.TargetID != *target) || (*environment != "" && manifest.Environment != *environment) {
			return emitFailure(nil, failure("context_mismatch", "Requested application target/environment does not match", false, "check_context"))
		}
		if command == "update" {
			var v struct {
				Manifest domain.Manifest `json:"manifest"`
			}
			_ = json.Unmarshal(body, &v)
			if (*target != "" && v.Manifest.TargetID != *target) || (*environment != "" && v.Manifest.Environment != *environment) {
				return emitFailure(nil, failure("context_mismatch", "Updated target/environment does not match", false, "check_context"))
			}
		}
	}
	if command == "deploy" || command == "create" {
		result.IdempotencyKey = *key
	}
	raw, f := c.Call(ctx, method, path, body, *key)
	if f != nil {
		return emitFailure(nil, f)
	}
	if command == "deploy" || command == "execute-plan" {
		var v domain.Deployment
		if json.Unmarshal(raw, &v) != nil || v.ID == "" {
			return emitFailure(nil, failure("invalid_response", "Release admission response lacks an operation ID; inspect before retry", false, "inspect_operation"))
		}
		result.OperationID = v.ID
	}
	if command == "execute-action" {
		var receipt struct {
			Result *struct {
				OperationID string `json:"operationId"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &receipt) != nil || receipt.Result == nil {
			return emitFailure(nil, failure("invalid_response", "Action response lacks a committed receipt; inspect the plan before retry", false, "inspect_plan"))
		}
		result.OperationID = receipt.Result.OperationID
	}
	if command == "verify" {
		var instances []struct {
			Status  string `json:"status"`
			Retired bool   `json:"retired"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(raw, &instances) != nil {
			return emitFailure(nil, failure("invalid_response", "Invalid instance projection", false, "inspect_instances"))
		}
		count := 0
		healthy := true
		for _, i := range instances {
			if i.Retired {
				continue
			}
			count++
			if i.Error != "" || !(i.Status == "running" || i.Status == "healthy" || i.Status == "running:healthy") {
				healthy = false
			}
		}
		if count == 0 || !healthy {
			return emitFailure(raw, failure("verification_inconclusive", "Current instances are absent, unhealthy or unavailable", false, "inspect_instances", "inspect_diagnostics"))
		}
		return finish(map[string]any{"coverage": "current_instance_health", "applicationBehaviorVerified": false, "instances": json.RawMessage(raw)}, nil, 0)
	}
	return finish(raw, nil, 0)
}
