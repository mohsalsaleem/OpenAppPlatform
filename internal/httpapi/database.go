package httpapi

import (
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
)

func (s *Server) databaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/applications/{id}/databases", func(w http.ResponseWriter, r *http.Request) {
		if !s.Preview {
			p, ok := domain.Identity(r.Context())
			if !ok || p.Role != "owner" || p.Kind != "session" {
				write(w, 403, map[string]string{"message": "Owner access is required"})
				return
			}
		}
		var v controller.DatabaseLinkRequest
		if err := decode(w, r, &v); err != nil {
			write(w, 400, map[string]string{"message": "Provide the reviewed database link"})
			return
		}
		a, err := s.Controller.LinkDatabase(r.Context(), r.PathValue("id"), v)
		if err != nil {
			if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrNotFound) {
				fail(w, err)
			} else {
				write(w, 422, map[string]string{"message": err.Error()})
			}
			return
		}
		write(w, 200, a)
	})
}
