package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func validateRuntime(c Component, components map[string]Component) error {
	if len(c.Env)+len(c.Services) > 64 {
		return errors.New("provide at most 64 runtime variables per component")
	}
	bytes := 0
	for key, value := range c.Env {
		if !envName.MatchString(key) || strings.ContainsRune(value, 0) || len(value) > 4096 {
			return errors.New("environment names must be identifiers and values at most 4096 bytes without NUL")
		}
		bytes += len(key) + len(value)
	}
	for variable, name := range c.Services {
		target, ok := components[name]
		if !envName.MatchString(variable) || !ok || name == "localhost" {
			return errors.New("service variables must name an existing application component")
		}
		if _, exists := c.Env[variable]; exists {
			return errors.New("a variable cannot be both environment configuration and a service connection")
		}
		if c.ResourceID != "" || target.ResourceID != "" {
			return errors.New("service connections require managed components")
		}
		bytes += len(variable) + len(name)
	}
	if bytes > 16<<10 {
		return errors.New("runtime configuration exceeds 16 KiB")
	}
	if c.ResourceID != "" && (len(c.Env) > 0 || len(c.Services) > 0) {
		return errors.New("adopted resource environment must be managed through the operator")
	}
	return nil
}

// RuntimeComponent resolves app-local HTTP endpoints from the frozen release.
// Only adapters that advertise application DNS may use these values.
func RuntimeComponent(m Manifest, name string) (Component, error) {
	var component Component
	found := false
	for _, c := range m.Components {
		if c.Name == name {
			component = c
			found = true
			break
		}
	}
	if !found {
		return component, errors.New("unknown component")
	}
	env := map[string]string{}
	for k, v := range component.Env {
		env[k] = v
	}
	for variable, target := range component.Services {
		resolved := false
		for _, c := range m.Components {
			if c.Name == target {
				env[variable] = fmt.Sprintf("http://%s:%d", c.Name, c.Port)
				resolved = true
				break
			}
		}
		if !resolved {
			return component, errors.New("service target is absent from the release")
		}
	}
	component.Env = env
	return component, nil
}
