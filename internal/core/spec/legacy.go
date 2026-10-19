package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// The legacy types mirror today's portable spec (oap.dev/v1alpha1) closely
// enough to decode it strictly. The rewrite does not import the old tree.
type legacyDocument struct {
	APIVersion     string              `json:"apiVersion"`
	Kind           string              `json:"kind"`
	Spec           *legacyManifest     `json:"spec"`
	RedactedFields map[string][]string `json:"redactedFields"`
}

type legacyManifest struct {
	Name        string            `json:"name"`
	Environment string            `json:"environment"`
	TargetID    string            `json:"targetId"`
	Ingress     *legacyIngress    `json:"ingress"`
	Components  []legacyComponent `json:"components"`
}

type legacyIngress struct {
	Domain string `json:"domain"`
	HTTPS  bool   `json:"https"`
	Routes []struct {
		Path        string `json:"path"`
		Component   string `json:"component"`
		StripPrefix bool   `json:"stripPrefix"`
	} `json:"routes"`
}

type legacyComponent struct {
	Name                  string            `json:"name"`
	Kind                  string            `json:"kind"`
	Image                 string            `json:"image"`
	Port                  int64             `json:"port"`
	HostPort              int64             `json:"hostPort"`
	Instances             int64             `json:"instances"`
	Strategy              string            `json:"strategy"`
	ResourceID            string            `json:"resourceId"`
	Management            string            `json:"management"`
	DatabaseEndpoint      string            `json:"databaseEndpoint"`
	RegistryCredentialRef string            `json:"registryCredentialRef"`
	Env                   map[string]string `json:"env"`
	SecretRefs            map[string]string `json:"secretRefs"`
	Services              map[string]string `json:"services"`
	ServiceEndpoints      map[string]string `json:"serviceEndpoints"`
	DependsOn             []string          `json:"dependsOn"`
	Volumes               []struct {
		Name      string `json:"name"`
		MountPath string `json:"mountPath"`
	} `json:"volumes"`
	Limits *struct {
		MemoryMiB int64 `json:"memoryMiB"`
		CPUMillis int64 `json:"cpuMillis"`
	} `json:"limits"`
	HealthCheck *struct {
		Mode               string `json:"mode"`
		Path               string `json:"path"`
		IntervalSeconds    int64  `json:"intervalSeconds"`
		TimeoutSeconds     int64  `json:"timeoutSeconds"`
		Retries            int64  `json:"retries"`
		StartPeriodSeconds int64  `json:"startPeriodSeconds"`
	} `json:"healthCheck"`
	Readiness *struct {
		RequireHealthy bool  `json:"requireHealthy"`
		TimeoutSeconds int64 `json:"timeoutSeconds"`
	} `json:"readiness"`
	Security *struct {
		AllowPrivilegeEscalation bool   `json:"allowPrivilegeEscalation"`
		ExceptionReason          string `json:"exceptionReason"`
	} `json:"security"`
}

// ConvertLegacy converts today's JSON spec, bare or wrapped in an
// oap.dev/v1alpha1 Application document, into a desired-state tree. Notes
// record information that was dropped on purpose; problems mean the spec
// can't be converted.
func ConvertLegacy(b []byte) (tree any, notes, problems []Problem) {
	m, probs := decodeLegacy(b)
	if len(probs) > 0 {
		return nil, nil, probs
	}
	if m.TargetID != "" {
		notes = append(notes, Problem{Path: "targetId", Why: "convert.dropped",
			Message: "targets are gone; an OAP server manages its own host"})
	}
	components := map[string]any{}
	for i, c := range m.Components {
		at := Index("components", i)
		if _, dup := components[c.Name]; dup {
			problems = append(problems, Problem{Path: at, Why: WhyDuplicateKey, Message: "component " + c.Name + " appears twice"})
			continue
		}
		out, ps := convertComponent(c, at)
		problems = append(problems, ps...)
		components[c.Name] = out
	}
	app := map[string]any{
		"api_version": "oap.dev/v1",
		"kind":        "Application",
		"name":        m.Name,
		"environment": m.Environment,
		"components":  components,
	}
	if in := m.Ingress; in != nil {
		routes := map[string]any{}
		for _, r := range in.Routes {
			route := map[string]any{"component": r.Component}
			if r.StripPrefix {
				route["strip_prefix"] = true
			}
			routes[r.Path] = route
		}
		ingress := map[string]any{"domain": in.Domain, "routes": routes}
		if in.HTTPS {
			ingress["https"] = true
		}
		app["ingress"] = ingress
	}
	if len(problems) > 0 {
		return nil, notes, problems
	}
	return app, notes, nil
}

func decodeLegacy(b []byte) (*legacyManifest, []Problem) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, []Problem{{Why: WhyJSONSyntax, Message: err.Error()}}
	}
	strict := func(v any) error {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		return dec.Decode(v)
	}
	if _, wrapped := probe["spec"]; wrapped {
		var doc legacyDocument
		if err := strict(&doc); err != nil {
			return nil, []Problem{{Why: WhyLegacyFormat, Message: err.Error()}}
		}
		if doc.APIVersion != "oap.dev/v1alpha1" || doc.Kind != "Application" || doc.Spec == nil {
			return nil, []Problem{{Why: WhyLegacyFormat, Message: "want an oap.dev/v1alpha1 Application"}}
		}
		if len(doc.RedactedFields) > 0 {
			return nil, []Problem{{Path: "redactedFields", Why: WhyLegacyFormat,
				Message: "refill the omitted values before converting an exported spec"}}
		}
		return doc.Spec, nil
	}
	var m legacyManifest
	if err := strict(&m); err != nil {
		return nil, []Problem{{Why: WhyLegacyFormat, Message: err.Error()}}
	}
	return &m, nil
}

func convertComponent(c legacyComponent, at string) (map[string]any, []Problem) {
	var problems []Problem
	unsupported := func(field, format string, args ...any) {
		problems = append(problems, Problem{Path: Join(at, field), Why: WhyLegacyUnsupported, Message: fmt.Sprintf(format, args...)})
	}
	if c.Management != "" || c.ResourceID != "" {
		unsupported("management", "adopting an existing resource was Coolify-only and is not carried over; Docker is the only operator")
	}
	if c.DatabaseEndpoint != "" {
		unsupported("databaseEndpoint", "observed databases are not carried over; run the database as a data component")
	}
	if c.Strategy != "" && c.Strategy != "standard" {
		unsupported("strategy", "only the standard strategy exists")
	}
	kind := c.Kind
	switch kind {
	case "":
		kind = KindWeb
	case "database":
		unsupported("kind", "database components linked existing resources; use kind data with an image and a volume")
	case KindWeb, KindWorker, KindJob:
	default:
		unsupported("kind", "unknown kind %q", kind)
	}
	out := map[string]any{"kind": kind, "image": c.Image}
	if c.Port > 0 {
		out["port"] = c.Port
	}
	if c.HostPort > 0 {
		out["host_port"] = c.HostPort
	}
	if c.Instances > 0 {
		out["instances"] = c.Instances
	}
	if c.RegistryCredentialRef != "" {
		out["registry_credential"] = c.RegistryCredentialRef
	}
	if len(c.Env) > 0 {
		out["env"] = stringMap(c.Env)
	}
	if len(c.SecretRefs) > 0 {
		out["secrets"] = stringMap(c.SecretRefs)
	}
	uses := map[string]any{}
	for _, variable := range sortedKeys(c.Services) {
		target := c.Services[variable]
		if _, dup := uses[target]; dup {
			unsupported("services", "two variables connect to %s; the new format allows one per component", target)
			continue
		}
		use := map[string]any{"type": "http", "env": variable}
		if endpoint := c.ServiceEndpoints[target]; endpoint != "" {
			use["endpoint"] = endpoint
		}
		uses[target] = use
	}
	// dependsOn is ordering; it becomes a connection only where services
	// already connect to the same component.
	var after []any
	for _, dep := range c.DependsOn {
		if _, connected := uses[dep]; !connected {
			after = append(after, dep)
		}
	}
	if len(after) > 0 {
		out["after"] = after
	}
	if len(uses) > 0 {
		out["uses"] = uses
	}
	if len(c.Volumes) > 0 {
		volumes := map[string]any{}
		for _, v := range c.Volumes {
			volumes[v.Name] = v.MountPath
		}
		out["volumes"] = volumes
	}
	if l := c.Limits; l != nil {
		out["resources"] = map[string]any{"memory_mib": l.MemoryMiB, "cpu_millis": l.CPUMillis}
	}
	if h := c.HealthCheck; h != nil {
		health := map[string]any{"mode": h.Mode}
		setString(health, "path", h.Path)
		setInt(health, "interval_seconds", h.IntervalSeconds)
		setInt(health, "timeout_seconds", h.TimeoutSeconds)
		setInt(health, "retries", h.Retries)
		setInt(health, "start_period_seconds", h.StartPeriodSeconds)
		out["health"] = health
	}
	if r := c.Readiness; r != nil {
		readiness := map[string]any{}
		if r.RequireHealthy {
			readiness["require_healthy"] = true
		}
		setInt(readiness, "timeout_seconds", r.TimeoutSeconds)
		out["readiness"] = readiness
	}
	if s := c.Security; s != nil {
		security := map[string]any{}
		if s.AllowPrivilegeEscalation {
			security["allow_privilege_escalation"] = true
		}
		setString(security, "exception_reason", s.ExceptionReason)
		out["security"] = security
	}
	return out, problems
}

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func setString(m map[string]any, key, v string) {
	if v != "" {
		m[key] = v
	}
}

func setInt(m map[string]any, key string, v int64) {
	if v != 0 {
		m[key] = v
	}
}
