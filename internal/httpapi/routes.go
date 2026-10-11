package httpapi

import (
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
)

func routeError(w http.ResponseWriter, e error) {
	if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrConflict) {
		fail(w, e)
		return
	}
	write(w, 422, map[string]string{"message": e.Error()})
}
func (s *Server) endpointRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/routes", func(w http.ResponseWriter, r *http.Request) {
		view, e := s.Controller.Routes(r.Context(), r.PathValue("id"))
		if e != nil {
			routeError(w, e)
			return
		}
		write(w, 200, view)
	})
	owner := func(w http.ResponseWriter, r *http.Request) bool {
		p, ok := domain.Identity(r.Context())
		if !ok || p.Role != "owner" || p.Kind != "session" {
			write(w, 403, map[string]string{"message": "A human workspace owner must review routing changes"})
			return false
		}
		return true
	}
	mux.HandleFunc("POST /api/v1/applications/{id}/endpoint-verifications", func(w http.ResponseWriter, r *http.Request) {
		if !owner(w, r) {
			return
		}
		var body struct {
			Name            string `json:"name"`
			ExpectedVersion int64  `json:"expectedVersion"`
		}
		if decode(w, r, &body) != nil {
			write(w, 400, map[string]string{"message": "A configured endpoint name and application version are required"})
			return
		}
		report, e := s.Controller.VerifyEndpoint(r.Context(), r.PathValue("id"), body.Name, body.ExpectedVersion)
		if e != nil {
			routeError(w, e)
			return
		}
		write(w, 200, report)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/route-reviews", func(w http.ResponseWriter, r *http.Request) {
		if !owner(w, r) {
			return
		}
		var body struct {
			Name            string `json:"name"`
			ExpectedVersion int64  `json:"expectedVersion"`
		}
		if decode(w, r, &body) != nil {
			write(w, 400, map[string]string{"message": "A route name and expected application version are required"})
			return
		}
		plan, e := s.Controller.ReviewRoute(r.Context(), r.PathValue("id"), body.Name, body.ExpectedVersion)
		if e != nil {
			routeError(w, e)
			return
		}
		write(w, 201, plan)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/route-publications/{plan}", func(w http.ResponseWriter, r *http.Request) {
		if !owner(w, r) {
			return
		}
		var body struct {
			Action string `json:"action"`
		}
		if decode(w, r, &body) != nil || (body.Action != "apply" && body.Action != "cancel") {
			write(w, 400, map[string]string{"message": "Choose apply or cancel for the reviewed publication"})
			return
		}
		plan, e := s.Controller.ApplyRoute(r.Context(), r.PathValue("id"), r.PathValue("plan"), body.Action == "cancel")
		if e != nil {
			routeError(w, e)
			return
		}
		write(w, 200, plan)
	})
}
