package httpapi

import (
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
)

func (s *Server) groupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/application-groups", func(w http.ResponseWriter, r *http.Request) {
		groups, e := s.Controller.Store.ApplicationGroups(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, groups)
	})
	mux.HandleFunc("POST /api/v1/application-groups/{id}/environments", func(w http.ResponseWriter, r *http.Request) {
		// Membership changes require a human owner; an app-scoped agent cannot gain
		// access to siblings by moving its environment into another logical app.
		if !s.Preview {
			p, ok := domain.Identity(r.Context())
			if !ok || p.Role != "owner" || p.Kind != "session" {
				write(w, 403, map[string]string{"message": "Owner access is required"})
				return
			}
		}
		var v struct {
			ApplicationID         string `json:"applicationId"`
			ExpectedGroupVersion  int64  `json:"expectedGroupVersion"`
			ExpectedSourceVersion int64  `json:"expectedSourceVersion"`
		}
		if decode(w, r, &v) != nil || v.ApplicationID == "" || v.ExpectedGroupVersion < 1 || v.ExpectedSourceVersion < 1 {
			write(w, 422, map[string]string{"message": "Select an existing environment and current application versions"})
			return
		}
		e := s.Controller.Store.LinkEnvironment(r.Context(), r.PathValue("id"), v.ApplicationID, v.ExpectedGroupVersion, v.ExpectedSourceVersion)
		if e != nil {
			if errors.Is(e, domain.ErrConflict) || errors.Is(e, domain.ErrNotFound) {
				fail(w, e)
			} else {
				write(w, 422, map[string]string{"message": e.Error()})
			}
			return
		}
		write(w, 200, map[string]string{"message": "Environment linked; workloads and permissions are preserved"})
	})
	mux.HandleFunc("POST /api/v1/application-groups/{id}/environments/{environment}/unlink", func(w http.ResponseWriter, r *http.Request) {
		if !s.Preview {
			p, ok := domain.Identity(r.Context())
			if !ok || p.Role != "owner" || p.Kind != "session" {
				write(w, 403, map[string]string{"message": "Owner access is required"})
				return
			}
		}
		var v struct {
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if decode(w, r, &v) != nil || v.ExpectedVersion < 1 {
			write(w, 422, map[string]string{"message": "Current application version is required"})
			return
		}
		if e := s.Controller.Store.UnlinkEnvironment(r.Context(), r.PathValue("id"), r.PathValue("environment"), v.ExpectedVersion); e != nil {
			fail(w, e)
			return
		}
		write(w, 200, map[string]string{"message": "Environment is standalone; workloads and permissions are preserved"})
	})

}
