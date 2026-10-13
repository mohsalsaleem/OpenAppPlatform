package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type credentialFile struct {
	SchemaVersion int    `json:"schemaVersion"`
	Origin        string `json:"origin"`
	Session       string `json:"session,omitempty"`
	Token         string `json:"token,omitempty"`
	CredentialID  string `json:"credentialId,omitempty"`
}

func origin(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}
func readCredential(path, endpoint string) (credentialFile, *Failure) {
	var credential credentialFile
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 4096 {
		return credential, failure("invalid_credential_file", "Use a private regular credential file", false, "authenticate")
	}
	raw, err := os.ReadFile(path)
	want, valid := origin(endpoint)
	if err != nil || json.Unmarshal(raw, &credential) != nil || credential.SchemaVersion != 1 || !valid || credential.Origin != want || ((credential.Token == "") == (credential.Session == "")) {
		return credential, failure("credential_context_mismatch", "Credential origin or format does not match", false, "check_context")
	}
	return credential, nil
}

// Reserve before issuance: never overwrite credentials or send a request when
// the output location is unsafe. The caller removes an unfilled reservation.
func reserveCredential(path string) (*os.File, *Failure) {
	parent, err := os.Lstat(filepath.Dir(path))
	if path == "" || err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return nil, failure("private_output_required", "Choose a new credential file in an existing private directory (0700)", false, "prepare_private_directory")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, failure("credential_output_unavailable", "Credential output must be a new writable private file", false, "choose_new_output")
	}
	return file, nil
}
func saveCredential(file *os.File, credential credentialFile) *Failure {
	if err := json.NewEncoder(file).Encode(credential); err != nil {
		return failure("credential_save_failed", "Credential was issued but could not be saved; inspect owner audit and revoke it", false, "inspect_audit", "revoke_credential")
	}
	if err := file.Sync(); err != nil {
		return failure("credential_save_failed", "Credential persistence could not be confirmed; inspect owner audit and revoke it", false, "inspect_audit", "revoke_credential")
	}
	directory, err := os.Open(filepath.Dir(file.Name()))
	if err != nil {
		return failure("credential_save_failed", "Credential directory persistence could not be confirmed", false, "inspect_audit", "revoke_credential")
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return failure("credential_save_failed", "Credential directory persistence could not be confirmed", false, "inspect_audit", "revoke_credential")
	}
	return nil
}
func ownerAuthenticate(ctx context.Context, endpoint, command, setup string, body []byte) (credentialFile, json.RawMessage, *Failure) {
	credential := credentialFile{SchemaVersion: 1}
	base, valid := origin(endpoint)
	if !valid {
		return credential, nil, failure("invalid_endpoint", "Use HTTPS or loopback HTTP", false, "configure_endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/v1/auth/"+command, bytes.NewReader(body))
	if err != nil {
		return credential, nil, failure("invalid_request", "Invalid authentication request", false, "authenticate")
	}
	req.Header.Set("X-OAP-CSRF", "1")
	req.Header.Set("Content-Type", "application/json")
	if command == "setup" {
		if len(setup) < 24 {
			return credential, nil, failure("setup_secret_required", "Provide OAP_SETUP_TOKEN privately", false, "authenticate")
		}
		req.Header.Set("Authorization", "Setup "+setup)
	}
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return credential, nil, failure("authentication_unavailable", "Authentication outcome is unknown; inspect setup status or login before retrying", false, "inspect_auth_status", "login")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 || !json.Valid(raw) {
		return credential, nil, failure("invalid_response", "Authentication response could not be read", false, "inspect_auth_status")
	}
	if response.StatusCode != 200 {
		f := failure("authentication_rejected", "Owner authentication was rejected", false, "inspect_auth_status", "login")
		f.HTTPStatus = response.StatusCode
		return credential, nil, f
	}
	name := "__Host-oap-session"
	if strings.HasPrefix(base, "http://") {
		name = "oap-session"
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			credential.Session = cookie.Value
		}
	}
	if credential.Session == "" {
		return credential, nil, failure("invalid_response", "Owner session is missing", false, "login")
	}
	var principal struct {
		CredentialID string `json:"credentialId"`
	}
	_ = json.Unmarshal(raw, &principal)
	credential.CredentialID = principal.CredentialID
	credential.Origin = base
	return credential, json.RawMessage(raw), nil
}
