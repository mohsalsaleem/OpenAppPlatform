package httpapi

import (
	"encoding/hex"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
)

func (s *Server) comparisonRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/release-comparison", func(w http.ResponseWriter, r *http.Request) {
		fromID, toID := r.URL.Query().Get("from"), r.URL.Query().Get("to")
		valid := func(id string) bool { _, err := hex.DecodeString(id); return len(id) == 32 && err == nil }
		if !valid(fromID) || !valid(toID) || fromID == toID {
			write(w, 400, map[string]string{"message": "Choose two distinct release IDs"})
			return
		}
		if _, err := s.Controller.Store.Application(r.Context(), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		from, err := s.Controller.Store.Deployment(r.Context(), fromID)
		if err != nil {
			fail(w, err)
			return
		}
		if from.ApplicationID != r.PathValue("id") {
			fail(w, domain.ErrNotFound)
			return
		}
		to, err := s.Controller.Store.Deployment(r.Context(), toID)
		if err != nil {
			fail(w, err)
			return
		}
		if to.ApplicationID != r.PathValue("id") {
			fail(w, domain.ErrNotFound)
			return
		}
		write(w, 200, domain.CompareReleases(from, to))
	})
}
