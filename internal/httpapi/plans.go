package httpapi

import (
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
)

func (s *Server) planRoutes(mux *http.ServeMux) {
	if s.Preview {
		return
	}
	reject := func(w http.ResponseWriter, err error) {
		if errors.Is(err, access.ErrDenied) {
			write(w, 403, map[string]string{"message": "This identity cannot perform this plan action"})
		} else if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrNotFound) {
			fail(w, err)
		} else {
			write(w, 422, map[string]string{"message": "Plan rejected; check current version, component selection and immutable images"})
		}
	}
	mux.HandleFunc("POST /api/v1/applications/{id}/plans", func(w http.ResponseWriter, r *http.Request) {
		p, _ := domain.Identity(r.Context())
		if !s.accessService().Rate(r.Context(), "plan:"+p.CredentialID) {
			write(w, 429, map[string]string{"message": "Plan creation limit reached; retry later"})
			return
		}
		var v struct {
			ExpectedVersion int64             `json:"expectedVersion"`
			Components      []string          `json:"components"`
			Images          map[string]string `json:"images"`
		}
		if err := decode(w, r, &v); err != nil {
			write(w, 400, map[string]string{"message": "Invalid deployment plan request"})
			return
		}
		out, err := s.Controller.CreateDeploymentPlan(r.Context(), r.PathValue("id"), v.ExpectedVersion, v.Components, v.Images)
		if err != nil {
			reject(w, err)
			return
		}
		write(w, 201, out)
	})
	mux.HandleFunc("GET /api/v1/applications/{id}/plans", func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Controller.Store.Pool.Query(r.Context(), "SELECT id FROM oap_deployment_plans WHERE application_id=$1 ORDER BY created_at DESC LIMIT 20", r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			fail(w, err)
			return
		}
		out := []controller.DeploymentPlan{}
		for _, id := range ids {
			v, e := s.Controller.DeploymentPlan(r.Context(), r.PathValue("id"), id)
			if e != nil {
				fail(w, e)
				return
			}
			out = append(out, v)
		}
		write(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/applications/{id}/plans/{plan}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Controller.DeploymentPlan(r.Context(), r.PathValue("id"), r.PathValue("plan"))
		if err != nil {
			reject(w, err)
			return
		}
		write(w, 200, v)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/plans/{plan}/approve", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			PlanHash string `json:"planHash"`
		}
		if err := decode(w, r, &v); err != nil {
			write(w, 400, map[string]string{"message": "Exact plan hash is required"})
			return
		}
		out, err := s.Controller.ApproveDeploymentPlan(r.Context(), r.PathValue("id"), r.PathValue("plan"), v.PlanHash)
		if err != nil {
			reject(w, err)
			return
		}
		write(w, 200, out)
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/plans/{plan}/execute", func(w http.ResponseWriter, r *http.Request) {
		var v struct{}
		if err := decode(w, r, &v); err != nil {
			write(w, 400, map[string]string{"message": "Execution accepts an empty object; plan inputs cannot be overridden"})
			return
		}
		out, err := s.Controller.ExecuteDeploymentPlan(r.Context(), r.PathValue("id"), r.PathValue("plan"))
		if err != nil {
			reject(w, err)
			return
		}
		write(w, 202, out)
	})
}
