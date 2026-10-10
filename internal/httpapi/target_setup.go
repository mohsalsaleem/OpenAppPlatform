package httpapi

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/config"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"time"
)

func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/targets/scopes", func(w http.ResponseWriter, r *http.Request) {
		if !s.Preview {
			p, ok := domain.Identity(r.Context())
			if !ok || p.Role != "owner" || p.Kind != "session" {
				write(w, 403, map[string]string{"message": "Owner access is required"})
				return
			}
		}
		var v struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			SourceTargetID string `json:"sourceTargetId"`
			Environment    string `json:"environment"`
		}
		if decode(w, r, &v) != nil {
			write(w, 422, map[string]string{"message": "Use an existing operator connection and a new environment scope"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		source, e := s.Controller.Store.Target(ctx, v.SourceTargetID)
		if e != nil {
			fail(w, e)
			return
		}
		if source.Operator != "coolify" || v.Environment == source.Environment {
			write(w, 422, map[string]string{"message": "Select a different existing Coolify environment"})
			return
		}
		t := source
		t.ID = v.ID
		t.Name = v.Name
		t.Environment = v.Environment
		if t.Validate() != nil || config.OperatorCredentialReference(t.TokenEnv) != nil {
			write(w, 422, map[string]string{"message": "Valid target name, ID and environment are required"})
			return
		}
		adapter, e := s.Controller.Factory(t)
		if e == nil {
			_, e = adapter.Discover(ctx)
		}
		if e != nil {
			write(w, 502, map[string]string{"message": "Cannot verify this existing environment. Check its name, project and server credential access."})
			return
		}
		if e = s.Controller.Store.CreateTargetScope(ctx, t); e != nil {
			fail(w, e)
			return
		}
		write(w, 201, t)
	})

	mux.HandleFunc("GET /api/v1/targets/{id}/connection", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		adapter, e := s.Controller.Adapter(ctx, r.PathValue("id"))
		if e != nil {
			if errors.Is(e, domain.ErrNotFound) {
				fail(w, e)
				return
			}
			write(w, 502, map[string]string{"message": "Cannot verify target. Check its server configuration and credential reference."})
			return
		}
		resources, e := s.Controller.Discover(ctx, r.PathValue("id"))
		if e != nil {
			write(w, 502, map[string]string{"message": "Cannot verify target connection or configured project/environment. Check server credentials and scope."})
			return
		}
		_, native := adapter.(operator.NativeSourceObserver)
		write(w, 200, map[string]any{"checkedAt": time.Now().UTC(), "resourceCount": len(resources), "capabilities": adapter.Capabilities(), "nativeSourceObservation": native})
	})
	mux.HandleFunc("GET /api/v1/applications/{id}/layout", func(w http.ResponseWriter, r *http.Request) {
		app, e := s.Controller.Store.Application(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		type component struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		}
		components := []component{}
		for _, c := range app.Manifest.Components {
			components = append(components, component{c.Name, c.Kind})
		}
		write(w, 200, map[string]any{"name": app.Manifest.Name, "definitionVersion": app.Version, "components": components})
	})
}
