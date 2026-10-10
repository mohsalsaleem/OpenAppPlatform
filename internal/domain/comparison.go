package domain

import (
	"fmt"
	"sort"
	"strings"
)

type ReleaseArtifact struct {
	Component string `json:"component"`
	Image     string `json:"image"`
	Immutable bool   `json:"immutable"`
}
type ReleaseSummary struct {
	ID                string            `json:"id"`
	DefinitionVersion int64             `json:"definitionVersion"`
	State             string            `json:"state"`
	Operation         string            `json:"operation"`
	Commit            string            `json:"commit,omitempty"`
	Artifacts         []ReleaseArtifact `json:"artifacts"`
}
type ReleaseChange struct {
	Component string `json:"component"`
	Field     string `json:"field"`
	Before    string `json:"before"`
	After     string `json:"after"`
}
type ReleaseComparison struct {
	From    ReleaseSummary  `json:"from"`
	To      ReleaseSummary  `json:"to"`
	Changes []ReleaseChange `json:"changes"`
}

// CompareReleases returns frozen requested definitions, never runtime evidence or
// rollback approval. Variable and endpoint values do not enter the result.
func CompareReleases(from, to Deployment) ReleaseComparison {
	selected := func(d Deployment) map[string]bool {
		out := map[string]bool{}
		for _, step := range d.Steps {
			out[step.Component] = true
		}
		return out
	}
	summary := func(d Deployment) ReleaseSummary {
		out := ReleaseSummary{ID: d.ID, DefinitionVersion: d.DefinitionVersion, State: d.State, Operation: d.Operation, Artifacts: []ReleaseArtifact{}}
		if out.Operation == "" {
			out.Operation = "deploy"
		}
		if d.Source != nil {
			out.Commit = d.Source.Commit
		}
		included := selected(d)
		for _, component := range d.Manifest.Components {
			if included[component.Name] {
				out.Artifacts = append(out.Artifacts, ReleaseArtifact{Component: component.Name, Image: component.Image, Immutable: digestSuffix.MatchString(component.Image)})
			}
		}
		sort.Slice(out.Artifacts, func(i, j int) bool { return out.Artifacts[i].Component < out.Artifacts[j].Component })
		return out
	}
	result := ReleaseComparison{From: summary(from), To: summary(to), Changes: []ReleaseChange{}}
	before, after := map[string]Component{}, map[string]Component{}
	names := map[string]bool{}
	for _, c := range from.Manifest.Components {
		before[c.Name] = c
		names[c.Name] = true
	}
	for _, c := range to.Manifest.Components {
		after[c.Name] = c
		names[c.Name] = true
	}
	ordered := []string{}
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	oldSelection, newSelection := selected(from), selected(to)
	add := func(name, field, a, b string) {
		if a != b {
			result.Changes = append(result.Changes, ReleaseChange{Component: name, Field: field, Before: a, After: b})
		}
	}
	dependencies := func(c Component) string {
		names := append([]string(nil), c.DependsOn...)
		sort.Strings(names)
		if len(names) == 0 {
			return "None"
		}
		return strings.Join(names, ", ")
	}
	timeout := func(c Component) int {
		if c.Readiness != nil && c.Readiness.TimeoutSeconds != 0 {
			return c.Readiness.TimeoutSeconds
		}
		return 900
	}
	health := func(c Component) bool { return c.Readiness != nil && c.Readiness.RequireHealthy }
	for _, name := range ordered {
		a, aOK := before[name]
		b, bOK := after[name]
		if !aOK || !bOK {
			add(name, "component present", fmt.Sprint(aOK), fmt.Sprint(bOK))
			continue
		}
		add(name, "included in operation", fmt.Sprint(oldSelection[name]), fmt.Sprint(newSelection[name]))
		add(name, "requested image", a.Image, b.Image)
		add(name, "kind", a.Kind, b.Kind)
		management := func(c Component) string {
			if c.Management == "observe" {
				return "Observe-only"
			}
			return "OAP-managed"
		}
		add(name, "management", management(a), management(b))
		if a.ResourceID != b.ResourceID {
			previous, next := "OAP-owned instances", "OAP-owned instances"
			if a.ResourceID != "" {
				previous = "Existing resource binding"
			}
			if b.ResourceID != "" {
				next = "Different existing resource binding"
			}
			add(name, "resource binding", previous, next)
		}
		add(name, "internal port", fmt.Sprint(a.Port), fmt.Sprint(b.Port))
		add(name, "host port", fmt.Sprint(a.HostPort), fmt.Sprint(b.HostPort))
		add(name, "instances", fmt.Sprint(a.Instances), fmt.Sprint(b.Instances))
		add(name, "strategy", a.Strategy, b.Strategy)
		add(name, "startup dependencies", dependencies(a), dependencies(b))
		add(name, "require healthy", fmt.Sprint(health(a)), fmt.Sprint(health(b)))
		add(name, "observation timeout (seconds)", fmt.Sprint(timeout(a)), fmt.Sprint(timeout(b)))
		for _, maps := range []struct {
			field string
			a, b  map[string]string
		}{{"environment", a.Env, b.Env}, {"service connections", a.Services, b.Services}, {"service endpoints", a.ServiceEndpoints, b.ServiceEndpoints}} {
			keys := map[string]bool{}
			for key := range maps.a {
				keys[key] = true
			}
			for key := range maps.b {
				keys[key] = true
			}
			sorted := []string{}
			for key := range keys {
				sorted = append(sorted, key)
			}
			sort.Strings(sorted)
			for _, key := range sorted {
				av, aExists := maps.a[key]
				bv, bExists := maps.b[key]
				if aExists == bExists && av == bv {
					continue
				}
				previous := "Not set"
				next := "Removed"
				if aExists {
					previous = "Configured (value hidden)"
				}
				if bExists {
					next = "Added (value hidden)"
					if aExists {
						next = "Changed (value hidden)"
					}
				}
				add(name, maps.field+" · "+key, previous, next)
			}
		}
	}
	return result
}
