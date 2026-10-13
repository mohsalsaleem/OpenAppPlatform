package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func actionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, access.ErrDenied):
		write(w, 403, map[string]string{"code": "forbidden", "message": "This identity cannot perform the requested specification/action operation"})
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrNotFound):
		fail(w, err)
	default:
		write(w, 422, map[string]string{"code": "action_rejected", "message": "Check the versioned specification, allowed action and current preconditions"})
	}
}
func (s *Server) specificationRoutes(mux *http.ServeMux) {
	if s.Preview {
		return
	}
	mux.HandleFunc("POST /api/v1/specifications/validate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ApplicationID string                          `json:"applicationId,omitempty"`
			Specification domain.ApplicationSpecification `json:"specification"`
		}
		if decode(w, r, &req) != nil {
			write(w, 400, map[string]string{"code": "invalid_specification", "message": "Supply one versioned specification"})
			return
		}
		if err := s.Controller.ValidateSpecification(r.Context(), req.Specification, req.ApplicationID); err != nil {
			actionError(w, err)
			return
		}
		write(w, 200, map[string]any{"schemaVersion": 1, "valid": true, "executed": false, "coverage": "structure_and_runtime_capabilities", "missingEvidence": []string{"secret availability", "native ownership/state", "admission conflicts", "application behavior"}})
	})
	mux.HandleFunc("GET /api/v1/applications/{id}/specification", func(w http.ResponseWriter, r *http.Request) {
		app, err := s.Controller.Store.Application(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, domain.ExportSpecification(app.Manifest))
	})
	mux.HandleFunc("POST /api/v1/applications/{id}/specification/diff", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Specification   domain.ApplicationSpecification `json:"specification"`
			ExpectedVersion int64                           `json:"expectedVersion"`
		}
		if decode(w, r, &req) != nil {
			write(w, 400, map[string]string{"message": "Supply specification and expectedVersion"})
			return
		}
		app, err := s.Controller.Store.Application(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		if req.ExpectedVersion != app.Version {
			fail(w, domain.ErrConflict)
			return
		}
		if err = s.Controller.ValidateSpecification(r.Context(), req.Specification, app.ID); err != nil {
			actionError(w, err)
			return
		}
		write(w, 200, map[string]any{"schemaVersion": 1, "untrustedEvidence": true, "executed": false, "expectedVersion": app.Version, "diff": domain.SpecificationDiff(app, req.Specification.Spec), "missingEvidence": []string{"final configuration admission", "runtime/secret availability"}})
	})
	mux.HandleFunc("POST /api/v1/action-plans", func(w http.ResponseWriter, r *http.Request) {
		p, _ := domain.Identity(r.Context())
		if !s.accessService().Rate(r.Context(), "action-plan:"+p.CredentialID) {
			write(w, 429, map[string]string{"message": "Plan creation limit reached"})
			return
		}
		var req controller.ActionRequest
		if decode(w, r, &req) != nil {
			write(w, 400, map[string]string{"message": "Supply one supported action request"})
			return
		}
		plan, err := s.Controller.CreateActionPlan(r.Context(), req)
		if err != nil {
			actionError(w, err)
			return
		}
		write(w, 201, plan)
	})
	mux.HandleFunc("GET /api/v1/action-plans/{plan}", func(w http.ResponseWriter, r *http.Request) {
		plan, err := s.Controller.ActionPlan(r.Context(), r.PathValue("plan"))
		if err != nil {
			actionError(w, err)
			return
		}
		write(w, 200, plan)
	})
	mux.HandleFunc("GET /api/v1/action-plans/{plan}/input", func(w http.ResponseWriter, r *http.Request) {
		p, _ := domain.Identity(r.Context())
		if p.Kind != "session" || p.Role != "owner" {
			actionError(w, access.ErrDenied)
			return
		}
		plan, err := s.Controller.ActionPlan(r.Context(), r.PathValue("plan"))
		if err != nil {
			actionError(w, err)
			return
		}
		// Owner-only explicit inspection of supplied literals. Secret/registry
		// references are not resolved and cannot reveal their stored values.
		write(w, 200, map[string]any{"untrustedEvidence": true, "secretValuesResolved": false, "input": json.RawMessage(plan.Payload), "planHash": plan.Hash})
	})
	mux.HandleFunc("POST /api/v1/action-plans/{plan}/approve", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			PlanHash string `json:"planHash"`
		}
		if decode(w, r, &req) != nil {
			write(w, 400, map[string]string{"message": "Supply exact planHash"})
			return
		}
		plan, err := s.Controller.ApproveActionPlan(r.Context(), r.PathValue("plan"), req.PlanHash)
		if err != nil {
			actionError(w, err)
			return
		}
		write(w, 200, plan)
	})
	mux.HandleFunc("POST /api/v1/action-plans/{plan}/execute", func(w http.ResponseWriter, r *http.Request) {
		var req struct{}
		if decode(w, r, &req) != nil {
			write(w, 400, map[string]string{"message": "Execute accepts only an empty object"})
			return
		}
		plan, err := s.Controller.ExecuteActionPlan(r.Context(), r.PathValue("plan"))
		if err != nil {
			actionError(w, err)
			return
		}
		status := 200
		if plan.Result != nil && plan.Result.OperationID != "" {
			status = 202
		}
		write(w, status, plan)
	})
}
