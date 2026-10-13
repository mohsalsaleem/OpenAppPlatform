package httpapi

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"net/http"
	"strconv"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func incidentError(w http.ResponseWriter, err error) {
	if errors.Is(err, access.ErrDenied) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		actionError(w, err)
		return
	}
	write(w, 503, map[string]any{"code": "incident_unavailable", "message": "Incident metadata is unavailable; check platform health and refresh", "retryable": true, "nextActions": []string{"check_platform_health", "refresh_incident"}})
}

func (s *Server) incidentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/incident", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		for key, values := range r.URL.Query() {
			if (key != "component" && key != "ordinal") || len(values) != 1 {
				write(w, 400, map[string]string{"code": "invalid_incident_selection", "message": "Use only one component and ordinal selection"})
				return
			}
		}
		ordinal := 0
		if value := r.URL.Query().Get("ordinal"); value != "" {
			var err error
			ordinal, err = strconv.Atoi(value)
			if err != nil || ordinal < 1 || ordinal > 4 {
				write(w, 400, map[string]string{"code": "invalid_incident_selection", "message": "ordinal must be 1..4 with a component"})
				return
			}
		}
		component := r.URL.Query().Get("component")
		if len(component) > 48 || (component == "" && ordinal != 0) {
			write(w, 400, map[string]string{"code": "invalid_incident_selection", "message": "Select a component when choosing an ordinal"})
			return
		}
		p, _ := domain.Identity(r.Context())
		allowed, rateErr := s.accessService().RateChecked(r.Context(), "incident:"+p.CredentialID)
		if rateErr != nil {
			incidentError(w, rateErr)
			return
		}
		if !allowed {
			write(w, 429, map[string]string{"code": "rate_limited", "message": "Incident sampling limit reached; reuse fresh evidence or wait"})
			return
		}
		incident, err := s.Controller.Incident(r.Context(), r.PathValue("id"), component, ordinal)
		if err != nil {
			incidentError(w, err)
			return
		}
		incident.Logs.Text = redactToken(incident.Logs.Text, s.Token)
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, incident)
	})
}
