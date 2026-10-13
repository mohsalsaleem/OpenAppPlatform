package ingress

import (
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"sort"
	"time"
)

// ApplicationRoutes is a controller-supplied owned application snapshot. Agents
// never supply runtime membership or a sibling application's configuration.
type ApplicationRoutes struct {
	ApplicationID string
	Manifest      domain.Manifest
	Members       []Member
}

// CompileServer builds a single dedicated proxy's complete configuration. Every
// participating application is explicit; omission removes its routes, so the
// future durable controller must aggregate all authorized desired applications.
// Applications may independently enable/disable HTTPS. No mutable Caddy config
// is imported or merged, and duplicate domain or application identity fails closed.
func CompileServer(applications []ApplicationRoutes, options Options, now time.Time) (Candidate, error) {
	if len(applications) < 1 || len(applications) > 32 {
		return Candidate{}, errors.New("shared ingress requires between 1 and 32 applications")
	}
	ordered := append([]ApplicationRoutes(nil), applications...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ApplicationID < ordered[j].ApplicationID })
	ids, domains := map[string]bool{}, map[string]bool{}
	addresses := map[string]bool{}
	servers := map[string]any{}
	tlsPolicies := []any{}
	var admin any
	for _, app := range ordered {
		if ids[app.ApplicationID] || app.Manifest.Ingress == nil || domains[app.Manifest.Ingress.Domain] {
			return Candidate{}, errors.New("shared ingress requires unique applications and domain ownership")
		}
		for _, member := range app.Members {
			if addresses[member.Address] {
				return Candidate{}, errors.New("different applications cannot share runtime route addresses")
			}
			addresses[member.Address] = true
		}
		ids[app.ApplicationID] = true
		domains[app.Manifest.Ingress.Domain] = true
		appOptions := options
		appOptions.InternalCA = options.InternalCA && app.Manifest.Ingress.HTTPS
		candidate, err := Compile(app.ApplicationID, app.Manifest, *app.Manifest.Ingress, app.Members, appOptions, now)
		if err != nil {
			return Candidate{}, err
		}
		var config map[string]any
		if err = json.Unmarshal(candidate.JSON(), &config); err != nil {
			return Candidate{}, err
		}
		admin = config["admin"]
		apps := config["apps"].(map[string]any)
		httpApp := apps["http"].(map[string]any)
		server := httpApp["servers"].(map[string]any)["oap"].(map[string]any)
		routes := server["routes"].([]any)
		routes = routes[:len(routes)-1] // one shared fallback below
		name := "oap-http"
		if app.Manifest.Ingress.HTTPS {
			name = "oap-https"
		}
		if existing, ok := servers[name]; ok {
			value := existing.(map[string]any)
			value["routes"] = append(value["routes"].([]any), routes...)
		} else {
			server["routes"] = routes
			servers[name] = server
		}
		if tlsApp, ok := apps["tls"]; ok {
			policies := tlsApp.(map[string]any)["automation"].(map[string]any)["policies"].([]any)
			tlsPolicies = append(tlsPolicies, policies...)
		}
	}
	for _, entry := range servers {
		server := entry.(map[string]any)
		routes := server["routes"].([]any)
		routes = append(routes, map[string]any{"handle": []any{map[string]any{"handler": "static_response", "status_code": 404}}, "terminal": true})
		server["routes"] = routes
	}
	apps := map[string]any{"http": map[string]any{"grace_period": int64(30 * time.Second), "http_port": options.HTTPPort, "https_port": options.HTTPSPort, "servers": servers}}
	if len(tlsPolicies) > 0 {
		apps["tls"] = map[string]any{"automation": map[string]any{"policies": tlsPolicies}}
	}
	raw, err := json.Marshal(map[string]any{"admin": admin, "apps": apps})
	if err != nil {
		return Candidate{}, err
	}
	if len(raw) > responseLimit {
		return Candidate{}, errors.New("shared ingress configuration exceeds size limit")
	}
	return Candidate{raw: raw, hash: Hash(raw)}, nil
}
