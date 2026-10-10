package domain

import "fmt"

// OrderedComponents preserves definition order where dependencies impose no order.
func (m Manifest) OrderedComponents() ([]Component, error) {
	components := map[string]Component{}
	for _, component := range m.Components {
		if len(component.DependsOn) > 15 {
			return nil, fmt.Errorf("provide at most 15 dependencies per component")
		}
		for _, name := range component.DependsOn {
			if !slug.MatchString(name) {
				return nil, fmt.Errorf("dependency names must be component slugs")
			}
		}
		components[component.Name] = component
	}
	state := map[string]int{}
	ordered := []Component{}
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return fmt.Errorf("dependency cycle at %s", name)
		}
		if state[name] == 2 {
			return nil
		}
		component, ok := components[name]
		if !ok {
			return fmt.Errorf("dependency %s is not an application component", name)
		}
		state[name] = 1
		seen := map[string]bool{}
		for _, dependency := range component.DependsOn {
			if dependency == name || seen[dependency] {
				return fmt.Errorf("dependencies for %s must be unique and cannot include itself", name)
			}
			seen[dependency] = true
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[name] = 2
		ordered = append(ordered, component)
		return nil
	}
	for _, component := range m.Components {
		if err := visit(component.Name); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
