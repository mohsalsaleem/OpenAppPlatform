package httpapi

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
	"time"
)

func releaseControlError(w http.ResponseWriter, err error) {
	if errors.Is(err, access.ErrDenied) {
		write(w, 403, map[string]string{"message": "Owner session access is required"})
		return
	}
	var blocked *controller.ReleaseControlError
	if errors.As(err, &blocked) {
		write(w, 422, map[string]string{"message": blocked.Message})
		return
	}
	fail(w, err)
}
func (s *Server) releaseControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/deployments/{id}/control", func(w http.ResponseWriter, r *http.Request) {
		var request controller.StopReleaseRequest
		if decode(w, r, &request) != nil {
			write(w, 400, map[string]string{"message": "A reviewed release control is required"})
			return
		}
		if request.Mode == "abandon" && !s.releaseOwner(r) {
			write(w, 403, map[string]string{"message": "Owner session access is required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		release, err := s.Controller.StopRelease(ctx, r.PathValue("id"), request)
		if err != nil {
			releaseControlError(w, err)
			return
		}
		write(w, 200, release)
	})
	mux.HandleFunc("POST /api/v1/deployments/{id}/reconcile-abandoned", func(w http.ResponseWriter, r *http.Request) {
		if !s.releaseOwner(r) {
			write(w, 403, map[string]string{"message": "Owner session access is required"})
			return
		}
		var request controller.ReconcileReleaseRequest
		if decode(w, r, &request) != nil {
			write(w, 400, map[string]string{"message": "A reviewed reconciliation is required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		release, err := s.Controller.ReconcileAbandoned(ctx, r.PathValue("id"), request)
		if err != nil {
			releaseControlError(w, err)
			return
		}
		write(w, 200, release)
	})
}

func (s *Server) releaseOwner(r *http.Request) bool {
	if s.Preview {
		return true
	}
	p, ok := domain.Identity(r.Context())
	return ok && p.Role == "owner" && p.Kind == "session"
}
