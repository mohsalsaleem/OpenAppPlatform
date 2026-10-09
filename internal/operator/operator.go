package operator

import (
	"context"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

type Capabilities struct {
	Retirement      bool `json:"retirement"`
	Restart         bool `json:"restart"`
	Environment     bool `json:"environment"`
	ApplicationDNS  bool `json:"applicationDns"`
	Standard        bool `json:"standard"`
	Rolling         bool `json:"rolling"`
	BlueGreen       bool `json:"blueGreen"`
	Discovery       bool `json:"discovery"`
	ImmutableImages bool `json:"immutableImages"`
}
type Resource struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Status       string `json:"status"`
	URL          string `json:"url,omitempty"`
	Image        string `json:"image,omitempty"`
	ArtifactKind string `json:"artifactKind,omitempty"`
	Port         int    `json:"port,omitempty"`
	Environment  string `json:"environment,omitempty"`
	ProjectID    string `json:"projectId,omitempty"`
}
type Spec struct {
	Name, Ownership string
	Component       domain.Component
}
type DeploymentStatus struct {
	State          string
	ResourceStatus string
}
type Adapter interface {
	Capabilities() Capabilities
	Discover(context.Context) ([]Resource, error)
	Ensure(context.Context, Spec) (Resource, error)
	Inspect(context.Context, string) (Resource, error)
	Deploy(context.Context, string) (string, error)
	Observe(context.Context, string, string) (DeploymentStatus, error)
	Logs(context.Context, string, int) (string, error)
}
type Factory func(domain.Target) (Adapter, error)

// Restarter restarts an existing runtime without applying a new definition.
type Restarter interface {
	Restart(context.Context, string) (string, error)
}

func ValidateRuntime(m domain.Manifest, caps Capabilities) error {
	for _, c := range m.Components {
		if len(c.Env) > 0 && !caps.Environment {
			return fmt.Errorf("target does not support managed environment variables for %s", c.Name)
		}
		if len(c.Services) > 0 && !caps.ApplicationDNS {
			return fmt.Errorf("target does not support application DNS connections for %s; configure an operator endpoint instead", c.Name)
		}
	}
	return nil
}

// Retirer stops exact owned instances and verifies their identity without deleting them.
type Retirer interface {
	Retire(context.Context, string, string) (string, error)
	ObserveRetirement(context.Context, string, string, string) (DeploymentStatus, error)
}
