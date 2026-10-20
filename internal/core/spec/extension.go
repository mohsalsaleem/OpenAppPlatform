package spec

import (
	"fmt"
	"regexp"
	"sort"
)

// Extension is one installed extension in the desired state
// (extensions.yaml, by name): its binary and the owner's grants (R4.1).
type Extension struct {
	Name      string
	Path      string
	SHA256    string
	Points    []string
	Settings  map[string]string
	Secrets   []string
	MemoryMiB int
}

// ExtensionPoints are the points an extension can be installed for.
var ExtensionPoints = []string{"notifier"}

var (
	extensionName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	sha256Hex     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	absPath       = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	settingName   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	secretRef     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// Tree returns the extension as its desired-state tree.
func (e Extension) Tree() map[string]any {
	t := map[string]any{"path": e.Path, "sha256": e.SHA256, "points": toAny(e.Points), "memory_mib": int64(e.MemoryMiB)}
	if len(e.Settings) > 0 {
		s := map[string]any{}
		for k, v := range e.Settings {
			s[k] = v
		}
		t["settings"] = s
	}
	if len(e.Secrets) > 0 {
		t["secrets"] = toAny(e.Secrets)
	}
	return t
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Check returns the extension's problems.
func (e Extension) Check() []Problem {
	var out []Problem
	add := func(field, format string, args ...any) {
		out = append(out, Problem{Path: Join("extensions."+e.Name, field), Why: WhyPattern, Message: fmt.Sprintf(format, args...)})
	}
	if !extensionName.MatchString(e.Name) {
		add("", "an extension's name is lowercase letters, digits and dashes")
	}
	if !absPath.MatchString(e.Path) {
		add("path", "the binary's path is an absolute path on the server")
	}
	if !sha256Hex.MatchString(e.SHA256) {
		add("sha256", "the binary's sha256 is 64 lowercase hex digits")
	}
	if len(e.Points) == 0 {
		add("points", "name at least one extension point")
	}
	for _, p := range e.Points {
		known := false
		for _, k := range ExtensionPoints {
			known = known || p == k
		}
		if !known {
			add("points", "%q isn't an extension point; known: %v", p, ExtensionPoints)
		}
	}
	for k := range e.Settings {
		if !settingName.MatchString(k) {
			add("settings", "%q isn't a setting name", k)
		}
	}
	for _, s := range e.Secrets {
		if !secretRef.MatchString(s) {
			add("secrets", "%q isn't a secret name", s)
		}
	}
	if e.MemoryMiB < 16 || e.MemoryMiB > 1024 {
		add("memory_mib", "the memory cap is 16 to 1024 MiB")
	}
	return out
}

// ParseExtensions reads extensions.yaml's tree.
func ParseExtensions(tree any) (map[string]Extension, []Problem) {
	out := map[string]Extension{}
	m, _ := tree.(map[string]any)
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var probs []Problem
	for _, n := range names {
		t, _ := m[n].(map[string]any)
		e := Extension{Name: n}
		e.Path, _ = t["path"].(string)
		e.SHA256, _ = t["sha256"].(string)
		for _, p := range asList(t["points"]) {
			e.Points = append(e.Points, p)
		}
		e.Secrets = asList(t["secrets"])
		if s, ok := t["settings"].(map[string]any); ok {
			e.Settings = map[string]string{}
			for k, v := range s {
				e.Settings[k], _ = v.(string)
			}
		}
		switch v := t["memory_mib"].(type) {
		case int64:
			e.MemoryMiB = int(v)
		case float64:
			e.MemoryMiB = int(v)
		}
		probs = append(probs, e.Check()...)
		out[n] = e
	}
	return out, probs
}

func asList(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
