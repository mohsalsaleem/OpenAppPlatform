package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var digestSuffix = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)

// Kind values for components.
const (
	KindWeb    = "web"
	KindWorker = "worker"
	KindJob    = "job"
	KindData   = "data"
)

// Application is a validated application spec with defaults applied.
type Application struct {
	APIVersion  string               `json:"api_version"`
	Kind        string               `json:"kind"`
	Name        string               `json:"name"`
	Environment string               `json:"environment"`
	Ingress     *Ingress             `json:"ingress,omitempty"`
	Components  map[string]Component `json:"components"`
}

type Ingress struct {
	Domain string           `json:"domain"`
	HTTPS  bool             `json:"https,omitempty"`
	Routes map[string]Route `json:"routes"`
}

type Route struct {
	Component   string `json:"component"`
	StripPrefix bool   `json:"strip_prefix,omitempty"`
}

type Component struct {
	Kind               string            `json:"kind"`
	Image              string            `json:"image"`
	RegistryCredential string            `json:"registry_credential,omitempty"`
	Port               int               `json:"port,omitempty"`
	HostPort           int               `json:"host_port,omitempty"`
	Instances          int               `json:"instances,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	Secrets            map[string]string `json:"secrets,omitempty"`
	After              []string          `json:"after,omitempty"`
	Uses               map[string]Use    `json:"uses,omitempty"`
	Volumes            map[string]string `json:"volumes,omitempty"`
	Resources          *Resources        `json:"resources,omitempty"`
	Health             *Health           `json:"health,omitempty"`
	Readiness          *Readiness        `json:"readiness,omitempty"`
	Security           *Security         `json:"security,omitempty"`
}

// Use is a typed connection to another component.
type Use struct {
	Type     string `json:"type"`
	Env      string `json:"env,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

type Resources struct {
	MemoryMiB int `json:"memory_mib"`
	CPUMillis int `json:"cpu_millis"`
}

type Health struct {
	Mode               string `json:"mode"`
	Path               string `json:"path,omitempty"`
	IntervalSeconds    int    `json:"interval_seconds,omitempty"`
	TimeoutSeconds     int    `json:"timeout_seconds,omitempty"`
	Retries            int    `json:"retries,omitempty"`
	StartPeriodSeconds int    `json:"start_period_seconds,omitempty"`
}

type Readiness struct {
	RequireHealthy bool `json:"require_healthy,omitempty"`
	TimeoutSeconds int  `json:"timeout_seconds,omitempty"`
}

type Security struct {
	AllowPrivilegeEscalation bool   `json:"allow_privilege_escalation,omitempty"`
	ExceptionReason          string `json:"exception_reason,omitempty"`
}

var (
	schemaOnce sync.Once
	schema     *Schema
	schemaErr  error
)

func applicationSchema() (*Schema, error) {
	schemaOnce.Do(func() { schema, schemaErr = CompileSchema(ApplicationSchema) })
	return schema, schemaErr
}

// ParseApplication validates a tree against the schema and the semantic rules
// and returns the typed application with defaults applied.
func ParseApplication(tree any) (*Application, []Problem) {
	s, err := applicationSchema()
	if err != nil {
		return nil, []Problem{{Why: WhySchema, Message: err.Error()}}
	}
	if probs := s.Validate(tree); len(probs) > 0 {
		return nil, probs
	}
	raw, err := Canonical(tree)
	if err != nil {
		return nil, []Problem{{Why: WhySchema, Message: err.Error()}}
	}
	var app Application
	if err := json.Unmarshal(raw, &app); err != nil {
		return nil, []Problem{{Why: WhySchema, Message: err.Error()}}
	}
	app.applyDefaults()
	if probs := app.check(); len(probs) > 0 {
		return nil, probs
	}
	return &app, nil
}

func (a *Application) applyDefaults() {
	for name, c := range a.Components {
		if c.Instances == 0 {
			c.Instances = 1
		}
		if c.Kind == KindWorker && c.Health == nil {
			c.Health = &Health{Mode: "image"}
		}
		if h := c.Health; h != nil && h.Mode == "http" {
			if h.IntervalSeconds == 0 {
				h.IntervalSeconds = 10
			}
			if h.TimeoutSeconds == 0 {
				h.TimeoutSeconds = 3
			}
			if h.Retries == 0 {
				h.Retries = 3
			}
		}
		a.Components[name] = c
	}
}

// Names returns component names in sorted order.
func (a *Application) Names() []string {
	names := make([]string, 0, len(a.Components))
	for n := range a.Components {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (a *Application) check() []Problem {
	var out []Problem
	add := func(path, why, format string, args ...any) {
		out = append(out, Problem{Path: path, Why: why, Message: fmt.Sprintf(format, args...)})
	}
	hostPorts := map[int]string{}
	for _, name := range a.Names() {
		c := a.Components[name]
		at := Join("components", name)
		switch c.Kind {
		case KindWeb:
			if c.Port == 0 {
				add(Join(at, "port"), WhyKindRule, "web components need a port")
			}
		case KindWorker:
			if c.Port != 0 || c.HostPort != 0 {
				add(at, WhyKindRule, "workers have no ports")
			}
			if c.Health.Mode != "image" {
				add(Join(at, "health"), WhyKindRule, "workers use image health checks")
			}
		case KindJob:
			if c.Port != 0 || c.HostPort != 0 || c.Instances != 1 || c.Health != nil || len(c.Volumes) > 0 ||
				(c.Readiness != nil && c.Readiness.RequireHealthy) {
				add(at, WhyKindRule, "jobs run one process with no ports, volumes or health checks; readiness only sets the timeout")
			}
		case KindData:
			if c.Instances != 1 || c.HostPort != 0 {
				add(at, WhyKindRule, "data components run one instance and are not published on the host")
			}
			if len(c.Volumes) == 0 {
				add(Join(at, "volumes"), WhyKindRule, "data components need at least one volume")
			}
		}
		if c.HostPort != 0 {
			if c.Instances != 1 {
				add(Join(at, "host_port"), WhyConflict, "a host port needs exactly one instance")
			}
			if other, taken := hostPorts[c.HostPort]; taken {
				add(Join(at, "host_port"), WhyConflict, "host port %d is also used by %s", c.HostPort, other)
			}
			hostPorts[c.HostPort] = name
		}
		if h := c.Health; h != nil {
			if h.Mode == "image" && (h.Path != "" || h.IntervalSeconds+h.TimeoutSeconds+h.Retries+h.StartPeriodSeconds != 0) {
				add(Join(at, "health"), WhyConflict, "image health checks have no HTTP settings")
			}
			if h.Mode == "http" && (h.Path == "" || strings.HasPrefix(h.Path, "//")) {
				add(Join(Join(at, "health"), "path"), WhyKindRule, "HTTP health checks need a local absolute path")
			}
			if h.Mode == "http" && c.Port == 0 {
				add(Join(at, "health"), WhyKindRule, "HTTP health checks need a port")
			}
		}
		if strings.Contains(c.Image, "@") && !digestSuffix.MatchString(c.Image) {
			add(Join(at, "image"), WhyPattern, "an image pinned with @ needs a full sha256 digest")
		}
		if s := c.Security; s != nil && s.AllowPrivilegeEscalation && s.ExceptionReason == "" {
			add(Join(Join(at, "security"), "exception_reason"), WhyRequired, "privilege escalation needs an exception reason")
		}
		seen := map[string]bool{}
		for i, dep := range c.After {
			at := Index(Join(at, "after"), i)
			if _, ok := a.Components[dep]; !ok || dep == name {
				add(at, WhyUnknownComponent, "must name another component of this application")
			} else if seen[dep] {
				add(at, WhyConflict, "%s is listed twice", dep)
			} else if _, connected := c.Uses[dep]; connected {
				add(at, WhyConflict, "%s is already ordered by uses", dep)
			}
			seen[dep] = true
		}
		out = append(out, a.checkVariables(name, c)...)
		out = append(out, a.checkUses(name, c)...)
	}
	out = append(out, a.checkIngress()...)
	if cycle := a.cycle(); cycle != nil {
		add("components", WhyCycle, "components depend on each other in a cycle: %s", strings.Join(cycle, " → "))
	}
	Sort(out)
	return out
}

func (a *Application) checkVariables(name string, c Component) []Problem {
	var out []Problem
	at := Join("components", name)
	owner := map[string]string{}
	claim := func(variable, source string) {
		if prev, ok := owner[variable]; ok {
			out = append(out, Problem{Path: at, Why: WhyConflict,
				Message: fmt.Sprintf("variable %s is set by both %s and %s", variable, prev, source)})
			return
		}
		owner[variable] = source
	}
	size := 0
	for _, k := range sortedKeys(c.Env) {
		claim(k, "env")
		size += len(k) + len(c.Env[k])
		if strings.ContainsRune(c.Env[k], 0) {
			out = append(out, Problem{Path: Join(Join(at, "env"), k), Why: WhyPattern, Message: "values cannot contain NUL"})
		}
	}
	for _, k := range sortedKeys(c.Secrets) {
		claim(k, "secrets")
		size += len(k) + len(c.Secrets[k])
	}
	for _, target := range sortedKeys(c.Uses) {
		if u := c.Uses[target]; u.Env != "" {
			claim(u.Env, "uses."+target)
			size += len(u.Env) + len(target) + len(u.Endpoint)
		}
	}
	if size > 16<<10 {
		out = append(out, Problem{Path: at, Why: WhyTooLarge, Message: "runtime variables exceed 16 KiB"})
	}
	return out
}

func (a *Application) checkUses(name string, c Component) []Problem {
	var out []Problem
	for _, target := range sortedKeys(c.Uses) {
		u := c.Uses[target]
		at := Join(Join(Join("components", name), "uses"), target)
		t, ok := a.Components[target]
		if !ok || target == name {
			out = append(out, Problem{Path: at, Why: WhyUnknownComponent, Message: "must name another component of this application"})
			continue
		}
		want := map[string][]string{"http": {KindWeb}, "database": {KindData}, "queue": {KindData, KindWeb}}[u.Type]
		if !contains(want, t.Kind) {
			out = append(out, Problem{Path: at, Why: WhyKindRule,
				Message: fmt.Sprintf("a %s connection needs a %s component; %s is %s", u.Type, strings.Join(want, " or "), target, t.Kind)})
		}
		if u.Endpoint != "" && u.Env == "" {
			out = append(out, Problem{Path: Join(at, "endpoint"), Why: WhyConflict, Message: "an endpoint is only used with env"})
		}
		if u.Env != "" && u.Type != "http" {
			out = append(out, Problem{Path: Join(at, "env"), Why: WhyKindRule, Message: "only http connections inject a URL; use secrets for credentials"})
		}
	}
	return out
}

func (a *Application) checkIngress() []Problem {
	if a.Ingress == nil {
		return nil
	}
	var out []Problem
	if isIPv4(a.Ingress.Domain) {
		out = append(out, Problem{Path: "ingress.domain", Why: WhyPattern, Message: "must be a DNS name, not an IP address"})
	}
	for _, path := range sortedKeys(a.Ingress.Routes) {
		r := a.Ingress.Routes[path]
		at := Join("ingress.routes", path)
		c, ok := a.Components[r.Component]
		switch {
		case !ok:
			out = append(out, Problem{Path: Join(at, "component"), Why: WhyUnknownComponent, Message: "no such component"})
		case c.Kind != KindWeb || c.HostPort != 0:
			out = append(out, Problem{Path: Join(at, "component"), Why: WhyKindRule, Message: "routes need a web component without a host port"})
		}
		if path == "/" && r.StripPrefix {
			out = append(out, Problem{Path: Join(at, "strip_prefix"), Why: WhyConflict, Message: "the root route can't strip its prefix"})
		}
	}
	return out
}

// Dependencies returns the components n must follow: those it uses and those
// it lists in after, sorted and limited to components that exist.
func (a *Application) Dependencies(n string) []string {
	c := a.Components[n]
	set := map[string]bool{}
	for dep := range c.Uses {
		set[dep] = true
	}
	for _, dep := range c.After {
		set[dep] = true
	}
	var out []string
	for dep := range set {
		if _, ok := a.Components[dep]; ok && dep != n {
			out = append(out, dep)
		}
	}
	sort.Strings(out)
	return out
}

// cycle returns one dependency cycle through uses and after, or nil.
func (a *Application) cycle() []string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var found []string
	var visit func(string) bool
	visit = func(n string) bool {
		state[n] = visiting
		stack = append(stack, n)
		for _, next := range a.Dependencies(n) {
			switch state[next] {
			case visiting:
				for i, s := range stack {
					if s == next {
						found = append(append([]string{}, stack[i:]...), next)
					}
				}
				return true
			case unvisited:
				if visit(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return false
	}
	for _, n := range a.Names() {
		if state[n] == unvisited && visit(n) {
			return found
		}
	}
	return nil
}

// Order returns component names so that every component comes after its
// dependencies. Among components that are ready, the first by name goes
// next.
func (a *Application) Order() []string {
	var out []string
	placed := map[string]bool{}
	for len(out) < len(a.Components) {
		next := ""
		for _, n := range a.Names() {
			if !placed[n] && a.usesPlaced(n, placed) {
				next = n
				break
			}
		}
		if next == "" {
			return nil // a cycle; ParseApplication rejects these
		}
		out = append(out, next)
		placed[next] = true
	}
	return out
}

func (a *Application) usesPlaced(n string, placed map[string]bool) bool {
	for _, dep := range a.Dependencies(n) {
		if !placed[dep] {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return true
}
