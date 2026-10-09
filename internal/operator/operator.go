package operator

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

type Capabilities struct {
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
