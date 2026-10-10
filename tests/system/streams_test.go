package system_test

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func streamRequest(t *testing.T, url, token string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return response, cancel
}
func nextSSE(t *testing.T, scanner *bufio.Scanner) (string, json.RawMessage) {
	t.Helper()
	event := ""
	data := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && event != "" {
			return event, json.RawMessage(data)
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatal("stream ended before event", scanner.Err())
	return "", nil
}
func TestApplicationStreamSnapshotsReconnectAndInstanceLogs(t *testing.T) {
	c, srv := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	response, cancel := streamRequest(t, srv.URL+"/api/v1/applications/"+app.ID+"/events", platformToken)
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("stream headers", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	event, raw := nextSSE(t, scanner)
	if event != "snapshot" || !strings.Contains(string(raw), app.ID) {
		t.Fatal("initial snapshot", event, string(raw))
	}
	release, err := c.Enqueue(ctx, app.ID, "stream-release-fixture")
	if err != nil {
		t.Fatal(err)
	}
	event, raw = nextSSE(t, scanner)
	if event != "snapshot" || !strings.Contains(string(raw), release.ID) {
		t.Fatal("release update absent", event)
	}
	cancel()
	response.Body.Close()
	reopened, stop := streamRequest(t, srv.URL+"/api/v1/applications/"+app.ID+"/events", platformToken)
	defer stop()
	defer reopened.Body.Close()
	event, raw = nextSSE(t, bufio.NewScanner(reopened.Body))
	if event != "snapshot" || !strings.Contains(string(raw), release.ID) {
		t.Fatal("reconnect lost current state")
	}
	if err = c.Store.Bind(ctx, "fixture", "log-ref", app.ID, "web", 1); err != nil {
		t.Fatal(err)
	}
	logs, done := streamRequest(t, srv.URL+"/api/v1/applications/"+app.ID+"/logs/web/stream?ordinal=1", platformToken)
	defer done()
	defer logs.Body.Close()
	event, raw = nextSSE(t, bufio.NewScanner(logs.Body))
	if event != "snapshot" || !strings.Contains(string(raw), "hello from fixture") {
		t.Fatal("log stream", event, string(raw))
	}
}
func TestScopedStreamRejectsOtherAppsAndClosesAfterCredentialRevocation(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	m := manifest()
	m.Name = "other-stream"
	other, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	service := access.Service{Pool: c.Store.Pool}
	owner, err := service.Register(ctx, "stream-owner@example.invalid", "Stream Owner", "stream-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := service.Issue(ctx, owner, "agent", "Read stream", "read", app.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Principal(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer srv.Close()
	foreign, stop := streamRequest(t, srv.URL+"/api/v1/applications/"+other.ID+"/events", token)
	foreign.Body.Close()
	stop()
	if foreign.StatusCode != 404 {
		t.Fatal("foreign app stream exposed", foreign.StatusCode)
	}
	response, cancel := streamRequest(t, srv.URL+"/api/v1/applications/"+app.ID+"/events", token)
	defer cancel()
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	event, _ := nextSSE(t, scanner)
	if event != "snapshot" {
		t.Fatal(event)
	}
	if _, err = c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET revoked_at=now() WHERE id=$1", principal.CredentialID); err != nil {
		t.Fatal(err)
	}
	event, _ = nextSSE(t, scanner)
	if event != "unauthorized" {
		t.Fatal("revoked credential continued receiving data", event)
	}
	if scanner.Scan() {
		t.Fatal("revoked stream did not close")
	}
}
