package operator

import (
	"context"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"time"
)

type Capabilities struct {
	ImageRollback     bool `json:"imageRollback"`
	ManagementHandoff bool `json:"managementHandoff"`
	Retirement        bool `json:"retirement"`
	Restart           bool `json:"restart"`
	Environment       bool `json:"environment"`
	ServiceEndpoints  bool `json:"serviceEndpoints"`
	ApplicationDNS    bool `json:"applicationDns"`
	Standard          bool `json:"standard"`
	Rolling           bool `json:"rolling"`
	BlueGreen         bool `json:"blueGreen"`
	Discovery         bool `json:"discovery"`
	ImmutableImages   bool `json:"immutableImages"`
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
	Variables       VariableJournal `json:"-"`
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
		if len(c.ServiceEndpoints) > 0 && !caps.ServiceEndpoints {
			return fmt.Errorf("target does not support explicit service endpoints for %s", c.Name)
		}
		for _, name := range c.Services {
			if !caps.ApplicationDNS && (!caps.ServiceEndpoints || c.ServiceEndpoints[name] == "") {
				return fmt.Errorf("target requires an explicit endpoint for service %s in %s", name, c.Name)
			}
		}
	}
	return nil
}

// Retirer stops exact owned instances and verifies their identity without deleting them.
type Retirer interface {
	Retire(context.Context, string, string) (string, error)
	ObserveRetirement(context.Context, string, string, string) (DeploymentStatus, error)
}

type VariableOwnership struct{ UUID, Hash, Intent string }
type VariableJournal interface {
	Reserve(context.Context, string) error
	Variables(context.Context, string) (map[string]VariableOwnership, error)
	Begin(context.Context, string, string, string, string) error
	Commit(context.Context, string, string, string, string) error
	Forget(context.Context, string, string, string) error
}

// NativeSourceObserver follows an existing operator-owned GitHub deployment.
// It never starts a build, deploys, or changes trigger ownership.
type NativeSourceDeployment struct {
	ID             string `json:"id"`
	Commit         string `json:"commit"`
	State          string `json:"state"`
	ResourceStatus string `json:"resourceStatus"`
}
type NativeSourceObserver interface {
	FindSourceDeployment(context.Context, string, string, string, string, time.Time) (NativeSourceDeployment, error)
}

// RollbackSafetyChecker reads current runtime fields that preparation could change.
// It must not mutate provider configuration or return sensitive field values.
type RollbackSafetyChecker interface {
	CheckRollbackConfiguration(context.Context, string, domain.Component) error
}
