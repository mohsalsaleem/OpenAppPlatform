package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) streamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/applications/{id}/events", s.applicationEvents)
	mux.HandleFunc("GET /api/v1/applications/{id}/logs/{component}/stream", s.logEvents)
}

// Recheck live credentials throughout a stream, not merely at connection time.
func (s *Server) streamAllowed(ctx context.Context, app string) bool {
	if s.Preview {
		return true
	}
	original, ok := domain.Identity(ctx)
	if !ok {
		return false
	}
	current, err := s.accessService().ByID(ctx, original.CredentialID)
	return err == nil && current.WorkspaceID == "default" && (current.ApplicationID == "" || current.ApplicationID == app)
}
func streamMessage(w http.ResponseWriter, event, id string, data []byte) error {
	response := http.NewResponseController(w)
	if err := response.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\nid: %s\ndata: %s\n\n", event, id, data); err != nil {
		return err
	}
	return response.Flush()
}

// Streams carry replaceable snapshots. Reconnecting always receives current state,
// so no in-memory event bus or cursor retained across controller crashes is needed.
func (s *Server) snapshots(w http.ResponseWriter, r *http.Request, load func(context.Context) (any, error)) {
	if _, err := s.Controller.Store.Application(r.Context(), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		write(w, 503, map[string]string{"message": "Streaming is unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	last := ""
	for {
		sample, stop := context.WithTimeout(ctx, 10*time.Second)
		allowed := s.streamAllowed(sample, r.PathValue("id"))
		if !allowed {
			stop()
			_ = streamMessage(w, "unauthorized", "", []byte(`{"message":"Sign in to continue"}`))
			return
		}
		snapshot, err := load(sample)
		stop()
		if err != nil {
			_ = streamMessage(w, "unavailable", "", []byte(`{"message":"Live updates are temporarily unavailable"}`))
			return
		}
		data, err := json.Marshal(snapshot)
		if err != nil || len(data) > 4<<20 {
			return
		}
		sum := sha256.Sum256(data)
		id := hex.EncodeToString(sum[:])
		if id != last {
			if streamMessage(w, "snapshot", id, data) != nil {
				return
			}
			last = id
		} else {
			if streamMessage(w, "heartbeat", id, []byte(`{}`)) != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Server) applicationEvents(w http.ResponseWriter, r *http.Request) {
	s.snapshots(w, r, func(ctx context.Context) (any, error) {
		application, err := s.Controller.Store.Application(ctx, r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		deployments, err := s.Controller.Store.Deployments(ctx, application.ID)
		if err != nil {
			return nil, err
		}
		return struct {
			Application domain.Application  `json:"application"`
			Deployments []domain.Deployment `json:"deployments"`
		}{application, deployments}, nil
	})
}
func (s *Server) instanceLogs(ctx context.Context, appID, component string, ordinal int) (string, error) {
	app, err := s.Controller.Store.Application(ctx, appID)
	if err != nil {
		return "", err
	}
	bindings, err := s.Controller.Store.Bindings(ctx, app.ID)
	if err != nil {
		return "", err
	}
	for _, binding := range bindings {
		if binding.Component == component && binding.Ordinal == ordinal {
			adapter, err := s.Controller.Adapter(ctx, app.Manifest.TargetID)
			if err != nil {
				return "", err
			}
			logs, err := adapter.Logs(ctx, binding.ResourceID, 100)
			if err != nil {
				return "", err
			}
			return redactToken(logs, s.Token), nil
		}
	}
	return "", domain.ErrNotFound
}
func (s *Server) logEvents(w http.ResponseWriter, r *http.Request) {
	ordinal := 1
	if raw := r.URL.Query().Get("ordinal"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 4 {
			write(w, 400, map[string]string{"message": "ordinal must be between 1 and 4"})
			return
		}
		ordinal = n
	}
	// Validate the binding under a bounded deadline before opening a stream.
	initial, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := s.instanceLogs(initial, r.PathValue("id"), r.PathValue("component"), ordinal); err != nil {
		fail(w, err)
		return
	}
	s.snapshots(w, r, func(ctx context.Context) (any, error) {
		logs, err := s.instanceLogs(ctx, r.PathValue("id"), r.PathValue("component"), ordinal)
		return map[string]string{"logs": logs}, err
	})
}
