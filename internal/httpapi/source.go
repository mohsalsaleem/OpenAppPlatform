package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
)

var deliveryPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	h, ok := s.Controller.SourceHooks[r.PathValue("id")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	defer r.Body.Close()
	body, e := io.ReadAll(r.Body)
	if e != nil {
		write(w, 413, map[string]string{"message": "Webhook payload exceeds limit"})
		return
	}
	if !source.Verify(os.Getenv(h.SecretEnv), r.Header.Get("X-Hub-Signature-256"), body) {
		write(w, 401, map[string]string{"message": "Webhook signature is invalid"})
		return
	}
	if r.Header.Get("X-GitHub-Event") == "ping" {
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Header.Get("X-GitHub-Event") != "push" {
		write(w, 200, map[string]bool{"ignored": true})
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if !deliveryPattern.MatchString(delivery) {
		write(w, 400, map[string]string{"message": "A valid delivery ID is required"})
		return
	}
	var push source.Push
	if json.Unmarshal(body, &push) != nil {
		write(w, 400, map[string]string{"message": "Invalid push payload"})
		return
	}
	if !push.Valid(h) {
		write(w, 200, map[string]bool{"ignored": true})
		return
	}
	p, e := (access.Service{Pool: s.Controller.Store.Pool}).Principal(r.Context(), os.Getenv(h.CredentialEnv))
	if e != nil || p.Kind != "agent" || (h.Mode != "coolify-github-app" && !p.Operates(h.ApplicationID)) || p.ApplicationID != h.ApplicationID {
		write(w, 503, map[string]string{"message": "Source credential is unavailable or not authorized"})
		return
	}
	event, e := s.Controller.ReceiveSource(domain.WithPrincipal(r.Context(), p), h, delivery, body, push)
	if e != nil {
		fail(w, e)
		return
	}
	write(w, 202, event)
}
func (s *Server) sourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/source-events", func(w http.ResponseWriter, r *http.Request) {
		if _, e := s.Controller.Store.Application(r.Context(), r.PathValue("id")); e != nil {
			fail(w, e)
			return
		}
		rows, e := s.Controller.Store.Pool.Query(r.Context(), "SELECT id FROM oap_source_events WHERE application_id=$1 ORDER BY created_at DESC LIMIT 50", r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				fail(w, e)
				return
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			fail(w, e)
			return
		}
		events := []controller.SourceEvent{}
		for _, id := range ids {
			v, e := s.Controller.SourceEvent(r.Context(), id)
			if e != nil {
				fail(w, e)
				return
			}
			events = append(events, v)
		}
		write(w, 200, events)
	})
	mux.HandleFunc("POST /api/v1/source-events/{id}/recover", func(w http.ResponseWriter, r *http.Request) {
		p, ok := domain.Identity(r.Context())
		if !ok || p.Role != "owner" || p.Kind != "session" {
			write(w, 403, map[string]string{"message": "Owner review is required"})
			return
		}
		var request struct {
			ExpectedUpdatedAt   time.Time `json:"expectedUpdatedAt"`
			ExpectedVersion     int64     `json:"expectedVersion"`
			RetryBuild          bool      `json:"retryBuild"`
			ApproveSourceCommit bool      `json:"approveSourceCommit"`
		}
		if decode(w, r, &request) != nil {
			write(w, 400, map[string]string{"message": "Invalid source recovery request"})
			return
		}
		v, e := s.Controller.RecoverSource(r.Context(), r.PathValue("id"), request.ExpectedUpdatedAt, request.ExpectedVersion, request.RetryBuild, request.ApproveSourceCommit)
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, v)
	})
}
