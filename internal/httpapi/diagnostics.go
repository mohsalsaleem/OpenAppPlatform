package httpapi

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
	"time"
)

func (s *Server) diagnosticRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		app, err := s.Controller.Store.Application(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		releases := []domain.Deployment{}
		records, err := s.Controller.Store.Pool.Query(r.Context(), "SELECT id FROM oap_deployments WHERE application_id=$1 ORDER BY created_at DESC LIMIT 2", app.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ids := []string{}
		for records.Next() {
			var id string
			if err = records.Scan(&id); err != nil {
				records.Close()
				fail(w, err)
				return
			}
			ids = append(ids, id)
		}
		err = records.Err()
		records.Close()
		if err != nil {
			fail(w, err)
			return
		}
		for _, id := range ids {
			d, err := s.Controller.Store.Deployment(r.Context(), id)
			if err != nil {
				fail(w, err)
				return
			}
			releases = append(releases, d)
		}
		var comparison *domain.ReleaseComparison
		if len(releases) > 0 {
			from := releases[0]
			if len(releases) > 1 {
				from = releases[1]
			}
			value := domain.CompareReleases(from, releases[0])
			comparison = &value
		}
		events := []map[string]any{}
		eventRows, err := s.Controller.Store.Pool.Query(r.Context(), "SELECT id,deployment_id,state,operation,commit_sha,created_at FROM oap_notification_outbox WHERE application_id=$1 ORDER BY created_at DESC LIMIT 20", app.ID)
		if err != nil {
			fail(w, err)
			return
		}
		defer eventRows.Close()
		for eventRows.Next() {
			var id, release, state, operation, commit string
			var at time.Time
			if err = eventRows.Scan(&id, &release, &state, &operation, &commit, &at); err != nil {
				fail(w, err)
				return
			}
			events = append(events, map[string]any{"id": id, "deploymentId": release, "state": state, "operation": operation, "commit": commit, "createdAt": at})
		}
		if err = eventRows.Err(); err != nil {
			fail(w, err)
			return
		}
		write(w, 200, map[string]any{"applicationId": app.ID, "definitionVersion": app.Version, "targetId": app.Manifest.TargetID, "environment": app.Manifest.Environment, "schemaVersion": 1, "generatedAt": time.Now().UTC(), "evidenceMode": "recorded", "untrustedEvidence": true, "limits": map[string]int{"releases": 2, "events": 20}, "missingEvidence": []string{"live runtime observations", "indexed logs and metrics", "repository patches", "backup safety for rollback"}, "comparison": comparison, "events": events, "notificationDelivery": "disabled"})
	})
}
