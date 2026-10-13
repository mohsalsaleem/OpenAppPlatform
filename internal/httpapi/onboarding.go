package httpapi

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
)

func (s *Server) onboardingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/access/onboarding-grants", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name            string   `json:"name"`
			TargetID        string   `json:"targetId"`
			Environment     string   `json:"environment"`
			Actions         []string `json:"actions"`
			MaxApplications int      `json:"maxApplications"`
		}
		if decode(w, r, &v) != nil {
			write(w, 400, map[string]string{"message": "Invalid onboarding grant"})
			return
		}
		p, _ := domain.Identity(r.Context())
		token, id, err := s.accessService().IssueOnboarding(r.Context(), p.UserID, v.Name, domain.AgentPermissions{TargetID: v.TargetID, Environment: v.Environment, Actions: v.Actions, CreateApplications: true, MaxApplications: v.MaxApplications})
		if err != nil {
			write(w, 422, map[string]string{"message": "Specify one Docker target/environment, explicit actions and an app limit of 1–10"})
			return
		}
		write(w, 201, map[string]any{"token": token, "credentialId": id, "expiresInSeconds": 86400})
	})
}
