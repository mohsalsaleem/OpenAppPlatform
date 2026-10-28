package ops

import (
	"sort"
	"strconv"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// Ref returns the ref of one instance of a component as currently specified.
func Ref(app, component string, c spec.Component, ordinal int) InstanceRef {
	return InstanceRef{Application: app, Component: component, Ordinal: ordinal, Release: model.Release(c)}
}

// InstanceSpecFor returns what the runtime needs to start one instance.
// Connection URLs (uses with env) are resolved to the target's service name.
func InstanceSpecFor(app *spec.Application, component string, ordinal int) InstanceSpec {
	c := app.Components[component]
	s := InstanceSpec{
		Ref:        Ref(app.Name, component, c, ordinal),
		Image:      c.Image,
		Env:        map[string]string{},
		SecretRefs: c.Secrets,
		Port:       c.Port,
		HostPort:   c.HostPort,
		Volumes:    c.Volumes,
	}
	for k, v := range c.Env {
		s.Env[k] = v
	}
	for target, u := range c.Uses {
		if u.Env == "" {
			continue
		}
		url := u.Endpoint
		if url == "" {
			url = ServiceURL(app.Name, target, app.Components[target].Port)
		}
		s.Env[u.Env] = url
	}
	if r := c.Resources; r != nil {
		s.MemoryMiB, s.CPUMillis = r.MemoryMiB, r.CPUMillis
	}
	if h := c.Health; h != nil {
		s.Health = &HealthCheck{HTTPPath: h.Path, IntervalSeconds: h.IntervalSeconds, TimeoutSeconds: h.TimeoutSeconds,
			Retries: h.Retries, StartPeriod: time.Duration(h.StartPeriodSeconds) * time.Second}
	}
	return s
}

// ServiceHost is the name a component's instances answer to on the
// installation's network, whatever their release.
func ServiceHost(app, component string) string {
	return component + "." + app + ".oap.internal"
}

// ServiceURL is the in-network address of a component's instances.
func ServiceURL(app, component string, port int) string {
	return "http://" + ServiceHost(app, component) + ":" + strconv.Itoa(port)
}

// JobSpecFor returns what the runtime needs to run a job component once.
func JobSpecFor(app *spec.Application, component string) JobSpec {
	c := app.Components[component]
	timeout := 10 * time.Minute
	if c.Readiness != nil && c.Readiness.TimeoutSeconds > 0 {
		timeout = time.Duration(c.Readiness.TimeoutSeconds) * time.Second
	}
	env := map[string]string{}
	for k, v := range c.Env {
		env[k] = v
	}
	return JobSpec{Ref: Ref(app.Name, component, c, 1), Image: c.Image, Env: env, SecretRefs: c.Secrets, Timeout: timeout}
}

// Members returns the route members for one instance of a component: one per
// route that targets it.
func Members(app *spec.Application, ref InstanceRef) []RouteMember {
	if app == nil || app.Ingress == nil {
		return nil
	}
	c := app.Components[ref.Component]
	var paths []string
	for p, r := range app.Ingress.Routes {
		if r.Component == ref.Component {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var out []RouteMember
	for _, p := range paths {
		out = append(out, RouteMember{Instance: ref, Host: app.Ingress.Domain, PathPrefix: p,
			StripPrefix: app.Ingress.Routes[p].StripPrefix, Port: c.Port})
	}
	return out
}

// WithMembers returns cfg plus members, without duplicates, in a stable order.
func WithMembers(cfg EdgeConfig, members ...RouteMember) EdgeConfig {
	out := EdgeConfig{}
	seen := map[RouteMember]bool{}
	for _, m := range append(append([]RouteMember{}, cfg.Members...), members...) {
		if !seen[m] {
			seen[m] = true
			out.Members = append(out.Members, m)
		}
	}
	sortMembers(out.Members)
	return out
}

// WithoutInstance returns cfg with every member for the instance removed.
func WithoutInstance(cfg EdgeConfig, ref InstanceRef) EdgeConfig {
	out := EdgeConfig{}
	for _, m := range cfg.Members {
		if m.Instance != ref {
			out.Members = append(out.Members, m)
		}
	}
	return out
}

// HasInstance reports whether any member routes to the instance.
func HasInstance(cfg EdgeConfig, ref InstanceRef) bool {
	for _, m := range cfg.Members {
		if m.Instance == ref {
			return true
		}
	}
	return false
}

func sortMembers(ms []RouteMember) {
	sort.Slice(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.PathPrefix != b.PathPrefix {
			return a.PathPrefix < b.PathPrefix
		}
		ai, bi := a.Instance, b.Instance
		if ai.Application != bi.Application {
			return ai.Application < bi.Application
		}
		if ai.Component != bi.Component {
			return ai.Component < bi.Component
		}
		if ai.Ordinal != bi.Ordinal {
			return ai.Ordinal < bi.Ordinal
		}
		return ai.Release < bi.Release
	})
}
