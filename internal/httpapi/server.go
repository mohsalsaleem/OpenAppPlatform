package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

type Server struct {
	Controller *controller.Controller
	Token      string
	WebDir     string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if e := s.Controller.Store.Pool.Ping(ctx); e != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	mux.Handle("/api/", s.auth(s.routes()))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if s.WebDir == "" {
			http.NotFound(w, r)
			return
		}
		clean := filepath.Clean(r.URL.Path)
		p := filepath.Join(s.WebDir, clean)
		if st, e := os.Stat(p); e == nil && !st.IsDir() {
			http.ServeFile(w, r, p)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.WebDir, "index.html"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || s.Token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) != 1 {
			write(w, 401, map[string]string{"code": "unauthorized", "message": "A valid platform access token is required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, e error) {
	status := 500
	code := "internal_error"
	message := "The operation failed. Inspect the controller logs."
	switch {
	case errors.Is(e, domain.ErrNotFound):
		status = 404
		code = "not_found"
		message = "Resource not found"
	case errors.Is(e, domain.ErrConflict):
		status = 409
		code = "conflict"
		message = "An application or active deployment conflicts with this request"
	}
	write(w, status, map[string]string{"code": code, "message": message})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(dst); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("provide exactly one JSON document")
	}
	return nil
}
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	c := s.Controller
	mux.HandleFunc("GET /api/v1/meta", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"name": "OpenAppPlatform", "version": "0.2.0-dev", "strategies": []string{"standard"}, "adapters": []string{"coolify", "docker"}})
	})
	mux.HandleFunc("GET /api/v1/targets", func(w http.ResponseWriter, r *http.Request) {
		x, e := c.Store.Targets(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, x)
	})
	mux.HandleFunc("GET /api/v1/targets/{id}/resources", func(w http.ResponseWriter, r *http.Request) {
		a, e := c.Adapter(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		x, e := a.Discover(r.Context())
		if e != nil {
			write(w, 502, map[string]string{"code": "operator_unavailable", "message": e.Error()})
			return
		}
		write(w, 200, x)
	})
	mux.HandleFunc("GET /api/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		x, e := c.Store.Applications(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, x)
	})
	mux.HandleFunc("POST /api/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		var m domain.Manifest
		if e := decode(w, r, &m); e != nil {
			write(w, 400, map[string]string{"code": "invalid_manifest", "message": e.Error()})
			return
		}
		if e := m.Validate(); e != nil {
			write(w, 422, map[string]string{"code": "invalid_manifest", "message": e.Error()})
			return
		}
		a, e := c.CreateApplication(r.Context(), m)
		if e != nil {
			if errors.Is(e, domain.ErrConflict) || errors.Is(e, domain.ErrNotFound) {
				fail(w, e)
			} else {
				write(w, 422, map[string]string{"code": "application_rejected", "message": e.Error()})
			}
			return
		}
		write(w, 201, a)
	})
	mux.HandleFunc("GET /api/v1/applications/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, e := c.Store.Application(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, a)
	})
	mux.HandleFunc("PUT /api/v1/applications/{id}", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Manifest        domain.Manifest `json:"manifest"`
			ExpectedVersion int64           `json:"expectedVersion"`
		}
		if e := decode(w, r, &request); e != nil {
			write(w, 400, map[string]string{"code": "invalid_configuration", "message": e.Error()})
			return
		}
		app, e := c.UpdateApplication(r.Context(), r.PathValue("id"), request.Manifest, request.ExpectedVersion)
		if e != nil {
			if errors.Is(e, domain.ErrConflict) || errors.Is(e, domain.ErrNotFound) {
				fail(w, e)
			} else {
				write(w, 422, map[string]string{"code": "configuration_rejected", "message": e.Error()})
			}
			return
		}
		write(w, 200, app)
	})
	mux.HandleFunc("GET /api/v1/applications/{id}/deployments", func(w http.ResponseWriter, r *http.Request) {
		if _, e := c.Store.Application(r.Context(), r.PathValue("id")); e != nil {
			fail(w, e)
			return
		}
		d, e := c.Store.Deployments(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, d)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/deployments", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Images          map[string]string `json:"images"`
			ExpectedVersion int64             `json:"expectedVersion"`
		}
		if r.ContentLength != 0 {
			if e := decode(w, r, &request); e != nil {
				write(w, 400, map[string]string{"code": "invalid_release", "message": e.Error()})
				return
			}
		}
		d, e := c.EnqueueVersion(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), request.Images, request.ExpectedVersion)
		if e != nil {
			if errors.Is(e, domain.ErrConflict) || errors.Is(e, domain.ErrNotFound) {
				fail(w, e)
			} else {
				write(w, 422, map[string]string{"code": "deployment_rejected", "message": e.Error()})
			}
			return
		}
		write(w, 202, d)
	})
	mux.HandleFunc("GET /api/v1/deployments/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, e := c.Store.Deployment(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, d)
	})
	mux.HandleFunc("GET /api/v1/applications/{id}/logs/{component}", func(w http.ResponseWriter, r *http.Request) {
		appID := r.PathValue("id")
		ds, e := c.Store.Deployments(r.Context(), appID)
		if e != nil {
			fail(w, e)
			return
		}
		if len(ds) == 0 {
			write(w, 404, map[string]string{"code": "no_instance", "message": "Deploy this component first"})
			return
		}
		a, e := c.Adapter(r.Context(), ds[0].Manifest.TargetID)
		if e != nil {
			fail(w, e)
			return
		}
		for _, step := range ds[0].Steps {
			if step.Component == r.PathValue("component") && step.ResourceID != "" {
				logs, e := a.Logs(r.Context(), step.ResourceID, 100)
				if e != nil {
					write(w, 502, map[string]string{"code": "logs_unavailable", "message": e.Error()})
					return
				}
				logs = strings.ReplaceAll(logs, s.Token, "[REDACTED]")
				write(w, 200, map[string]string{"logs": logs})
				return
			}
		}
		write(w, 404, map[string]string{"code": "no_instance", "message": "No deployed instance for this component"})
	})
	return mux
}
