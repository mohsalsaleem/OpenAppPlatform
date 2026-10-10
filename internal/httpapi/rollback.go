package httpapi

import (
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
	"strconv"
)

func rollbackError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		fail(w, err)
		return
	}
	var blocked *controller.RollbackValidationError
	if errors.As(err, &blocked) {
		write(w, 422, map[string]string{"message": blocked.Message})
		return
	}
	fail(w, err)
}
func (s *Server) rollbackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/rollback-plan", func(w http.ResponseWriter, r *http.Request) {
		version, err := strconv.ParseInt(r.URL.Query().Get("expectedVersion"), 10, 64)
		if err != nil {
			write(w, 400, map[string]string{"message": "expectedVersion is required"})
			return
		}
		plan, err := s.Controller.PlanRollback(r.Context(), r.PathValue("id"), r.URL.Query().Get("releaseId"), version)
		if err != nil {
			rollbackError(w, err)
			return
		}
		write(w, 200, plan)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/rollbacks", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			controller.RollbackRequest
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if decode(w, r, &request) != nil {
			write(w, 400, map[string]string{"message": "A reviewed rollback plan is required"})
			return
		}
		release, err := s.Controller.EnqueueRollback(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), request.RollbackRequest, request.ExpectedVersion)
		if err != nil {
			rollbackError(w, err)
			return
		}
		write(w, 202, release)
	})
}
