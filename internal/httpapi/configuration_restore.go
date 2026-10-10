package httpapi

import (
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"net/http"
	"strconv"
)

func (s *Server) configurationRestoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/configuration-restore-plan", func(w http.ResponseWriter, r *http.Request) {
		version, err := strconv.ParseInt(r.URL.Query().Get("expectedVersion"), 10, 64)
		if err != nil {
			write(w, 400, map[string]string{"message": "expectedVersion is required"})
			return
		}
		plan, err := s.Controller.PlanConfigurationRestore(r.Context(), r.PathValue("id"), r.URL.Query().Get("releaseId"), version)
		if err != nil {
			rollbackError(w, err)
			return
		}
		write(w, 200, plan)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/configuration-restores", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			controller.ConfigurationRestoreRequest
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if decode(w, r, &request) != nil {
			write(w, 400, map[string]string{"message": "A reviewed configuration restore plan is required"})
			return
		}
		app, err := s.Controller.RestoreConfiguration(r.Context(), r.PathValue("id"), request.ConfigurationRestoreRequest, request.ExpectedVersion)
		if err != nil {
			if errors.Is(err, access.ErrDenied) {
				write(w, 403, map[string]string{"message": "Operate access is required"})
				return
			}
			rollbackError(w, err)
			return
		}
		write(w, 200, app)
	})
}
