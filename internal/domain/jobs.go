package domain

import (
	"errors"
	"sort"
)

// ExpandJobDependencies includes migration jobs in a selected release without
// redeploying its other, already-running dependencies. The expanded selection
// must be shown and pinned in the reviewed plan before admission.
func ExpandJobDependencies(m Manifest, selection []string) ([]string, error) {
	components := map[string]Component{}
	for _, c := range m.Components {
		components[c.Name] = c
	}
	selected := map[string]bool{}
	original := map[string]bool{}
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		c, ok := components[name]
		if !ok {
			return errors.New("unknown selected component or dependency")
		}
		if seen[name] {
			return nil
		}
		seen[name] = true
		for _, dep := range c.DependsOn {
			if components[dep].Kind == "job" {
				selected[dep] = true
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range selection {
		if original[name] {
			return nil, errors.New("duplicate selected component")
		}
		original[name] = true
		selected[name] = true
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	out := []string{}
	for name := range selected {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
