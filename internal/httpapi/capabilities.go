package httpapi

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"time"
)

type actionCapability struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// This is eligibility evidence, not admission: the controller rechecks all preconditions.
func applicationActions(app domain.Application, caps operator.Capabilities, p domain.Principal, preview bool) map[string]map[string]actionCapability {
	out := map[string]map[string]actionCapability{}
	for _, component := range app.Manifest.Components {
		actions := map[string]actionCapability{}
		for _, action := range []string{"deploy", "restart", "configure"} {
			decision := actionCapability{Allowed: true, Reason: "eligible; admission rechecks definition, inputs, ownership and operation conflicts"}
			switch {
			case !preview && !p.Allows(app.ID, action, app.Manifest.TargetID, app.Manifest.Environment):
				decision = actionCapability{Reason: "credential does not permit this action in this application context"}
			case component.Kind == "database" || component.Management == "observe":
				decision = actionCapability{Reason: "native resource configuration and lifecycle remain externally owned"}
			case action == "deploy" && !caps.Standard:
				decision = actionCapability{Reason: "runtime does not support standard deployment"}
			case action == "restart" && !caps.Restart:
				decision = actionCapability{Reason: "runtime does not support restart"}
			}
			actions[action] = decision
		}
		out[component.Name] = actions
	}
	return out
}
func (s *Server) applicationCapabilities(w http.ResponseWriter, r *http.Request) {
	app, e := s.Controller.Store.Application(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	adapter, e := s.Controller.Adapter(r.Context(), app.Manifest.TargetID)
	if e != nil {
		fail(w, e)
		return
	}
	p, _ := domain.Identity(r.Context())
	caps := adapter.Capabilities()
	write(w, 200, map[string]any{"schemaVersion": 1, "generatedAt": time.Now().UTC(), "applicationId": app.ID, "definitionVersion": app.Version, "targetId": app.Manifest.TargetID, "environment": app.Manifest.Environment, "runtime": caps, "actions": applicationActions(app, caps, p, s.Preview), "admissionRequired": true, "coverage": []string{"deploy", "restart", "configure"}, "missingEvidence": []string{"active operation conflicts", "resolved runtime inputs", "current runtime ownership drift"}})
}
