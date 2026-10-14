// Package domain defines the operator-independent application contract.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var imageCharacters = regexp.MustCompile(`^[A-Za-z0-9/._:@-]+$`)
var cachedImageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var digestSuffix = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)

var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// ReadinessPolicy gates release completion using operator-reported runtime health.
// It does not configure a native probe or contact arbitrary network endpoints.
type ReadinessPolicy struct {
	RequireHealthy bool `json:"requireHealthy,omitempty"`
	TimeoutSeconds int  `json:"timeoutSeconds,omitempty"`
}

type RegistryAuth struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	ServerAddress string `json:"serveraddress"`
}

type ContainerSecurity struct {
	AllowPrivilegeEscalation bool   `json:"allowPrivilegeEscalation"`
	ExceptionReason          string `json:"exceptionReason,omitempty"`
}
type Component struct {
	Security *ContainerSecurity `json:"security,omitempty"`

	RegistryCredentialRef string            `json:"registryCredentialRef,omitempty"`
	RegistryAuth          *RegistryAuth     `json:"-"`
	Volumes               []VolumeMount     `json:"volumes,omitempty"`
	Limits                *ResourceLimits   `json:"limits,omitempty"`
	DatabaseEndpoint      string            `json:"databaseEndpoint,omitempty"`
	HealthCheck           *HealthCheck      `json:"healthCheck,omitempty"`
	DependsOn             []string          `json:"dependsOn,omitempty"`
	Readiness             *ReadinessPolicy  `json:"readiness,omitempty"`
	Management            string            `json:"management,omitempty"`
	ServiceEndpoints      map[string]string `json:"serviceEndpoints,omitempty"`
	SecretRefs            map[string]string `json:"secretRefs,omitempty"`
	Env                   map[string]string `json:"env,omitempty"`
	Services              map[string]string `json:"services,omitempty"`
	Name                  string            `json:"name"`
	Kind                  string            `json:"kind"`
	Image                 string            `json:"image"`
	Port                  int               `json:"port"`
	HostPort              int               `json:"hostPort,omitempty"`
	Instances             int               `json:"instances"`
	Strategy              string            `json:"strategy"`
	ResourceID            string            `json:"resourceId,omitempty"`
}
type Manifest struct {
	Ingress     *IngressDefinition `json:"ingress,omitempty"`
	Name        string             `json:"name"`
	Environment string             `json:"environment"`
	TargetID    string             `json:"targetId"`
	Components  []Component        `json:"components"`
}

func (m *Manifest) Validate() error {
	if !slug.MatchString(m.Name) || !slug.MatchString(m.Environment) {
		return errors.New("name and environment must be lowercase slugs, at most 48 characters")
	}
	if m.TargetID == "" {
		return errors.New("targetId is required")
	}
	if len(m.Components) == 0 || len(m.Components) > 16 {
		return errors.New("provide between 1 and 16 components")
	}
	names := map[string]bool{}
	for i := range m.Components {
		c := &m.Components[i]
		if c.RegistryCredentialRef != "" && (!slug.MatchString(c.RegistryCredentialRef) || c.ResourceID != "" || c.Management == "observe" || c.Kind == "database") {
			return errors.New("registry credential references require a lowercase reference slug and an OAP-owned image workload")
		}
		if !slug.MatchString(c.Name) || names[c.Name] {
			return errors.New("component names must be unique lowercase slugs")
		}
		names[c.Name] = true
		if err := c.ValidateStorageAndLimits(); err != nil {
			return err
		}
		if c.Management != "" && c.Management != "observe" {
			return errors.New("management must be observe or omitted for existing managed components")
		}
		if c.Management == "observe" && (c.ResourceID == "" || c.HostPort != 0 || len(c.Env)+len(c.SecretRefs)+len(c.Services)+len(c.ServiceEndpoints)+len(c.DependsOn) > 0) {
			return errors.New("observe-only components require a resourceId and cannot configure runtime settings")
		}
		if c.HealthCheck != nil {
			if c.Management == "observe" || c.ResourceID != "" {
				return errors.New("native health check overrides require OAP-owned resources")
			}
			if e := c.HealthCheck.Validate(); e != nil {
				return e
			}
		}
		if c.Readiness != nil {
			if c.Management == "observe" {
				return errors.New("observed component readiness remains owned by the operator")
			}
			if c.Readiness.TimeoutSeconds != 0 && (c.Readiness.TimeoutSeconds < 30 || c.Readiness.TimeoutSeconds > 1800) {
				return errors.New("readiness timeoutSeconds must be zero for the default or between 30 and 1800")
			}
		}
		if c.Kind == "" {
			c.Kind = "web"
		}
		if c.Kind != "web" && c.Kind != "worker" && c.Kind != "database" {
			return errors.New("kind must be web, worker or database; scheduled jobs are not implemented")
		}
		if c.Kind == "database" {
			if c.Management != "observe" || c.ResourceID == "" || c.Port != 0 || c.Instances > 1 || c.HealthCheck != nil || c.Readiness != nil {
				return errors.New("databases must link one observed existing resource without runtime overrides")
			}
			if err := ValidateDatabaseEndpoint(c.DatabaseEndpoint); err != nil {
				return err
			}
		} else if c.DatabaseEndpoint != "" {
			return errors.New("databaseEndpoint belongs to an observed database")
		}
		if c.Kind == "worker" {
			if c.Port != 0 || c.HostPort != 0 || c.ResourceID != "" {
				return errors.New("workers require OAP-owned image workloads without ports or existing resource bindings")
			}
			if c.HealthCheck != nil && c.HealthCheck.Mode != "image" {
				return errors.New("workers use non-HTTP image health checks")
			}
			if c.HealthCheck == nil {
				c.HealthCheck = &HealthCheck{Mode: "image"}
			}
		}
		if c.Instances == 0 {
			c.Instances = 1
		}
		if c.Instances < 1 || c.Instances > 4 {
			return errors.New("instances must be between 1 and 4")
		}
		if c.ResourceID != "" && c.Instances != 1 {
			return errors.New("an adopted resource represents one instance")
		}
		if c.Strategy == "" {
			c.Strategy = "standard"
		}
		if c.Strategy != "standard" {
			return errors.New("only standard deployment is supported; rolling and blue-green are not silently downgraded")
		}
		if c.HostPort != 0 && (c.HostPort < 1024 || c.HostPort > 65535) {
			return errors.New("hostPort must be zero or between 1024 and 65535")
		}
		if c.HostPort != 0 && c.Instances != 1 {
			return errors.New("hostPort requires one instance; shared port routing is not implemented")
		}
		if c.Security != nil {
			if c.ResourceID != "" || c.Management == "observe" || c.Kind == "database" {
				return errors.New("linked resource security remains managed by its operator")
			}
			if c.Security.AllowPrivilegeEscalation && (len(c.Security.ExceptionReason) < 12 || len(c.Security.ExceptionReason) > 240 || strings.ContainsAny(c.Security.ExceptionReason, "\n\r\x00")) {
				return errors.New("security exceptions require a reason of 12 to 240 bytes without line breaks")
			}
		}
		if c.Kind != "worker" && ((c.Port < 1 && !(c.Management == "observe" && c.Port == 0)) || c.Port > 65535) {
			return errors.New("port must be between 1 and 65535")
		}
		if (c.Image == "" && c.Management != "observe") || len(c.Image) > 512 || (c.Image != "" && !imageCharacters.MatchString(c.Image)) || (strings.Contains(c.Image, "@") && !digestSuffix.MatchString(c.Image)) {
			return errors.New("image is required")
		}
	}
	components := map[string]Component{}
	for _, c := range m.Components {
		components[c.Name] = c
	}
	for _, c := range m.Components {
		if err := validateRuntime(c, components); err != nil {
			return err
		}
	}
	if m.Ingress != nil {
		if err := m.Ingress.Validate(*m); err != nil {
			return err
		}
	}
	_, err := m.OrderedComponents()
	return err
}

type Target struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Operator    string          `json:"operator"`
	URL         string          `json:"url"`
	TokenEnv    string          `json:"-"`
	ProjectID   string          `json:"projectId"`
	ServerID    string          `json:"serverId"`
	Environment string          `json:"environment"`
	Settings    json.RawMessage `json:"settings,omitempty"`
}

func (t Target) Validate() error {
	if !slug.MatchString(t.ID) || t.Name == "" || !slug.MatchString(t.Operator) {
		return errors.New("target id, name and operator are required")
	}
	if !slug.MatchString(t.Environment) || t.URL == "" {
		return errors.New("target URL and environment are required")
	}
	return nil
}

type ApplicationGroup struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Version      int64         `json:"version"`
	Environments []Application `json:"environments"`
}

type Application struct {
	GroupID   string    `json:"groupId"`
	ID        string    `json:"id"`
	Manifest  Manifest  `json:"manifest"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Version   int64     `json:"version"`
}
type ProviderReconciliation struct {
	Component          string `json:"component"`
	Ordinal            int    `json:"ordinal"`
	RemoteDeploymentID string `json:"remoteDeploymentId"`
	State              string `json:"state,omitempty"`
}
type ReleaseControl struct {
	Mode         string                   `json:"mode"`
	Reason       string                   `json:"reason"`
	At           time.Time                `json:"at"`
	UserID       string                   `json:"userId,omitempty"`
	CredentialID string                   `json:"credentialId,omitempty"`
	RequestHash  string                   `json:"requestHash"`
	TargetHash   string                   `json:"targetHash"`
	ResolvedAt   *time.Time               `json:"resolvedAt,omitempty"`
	Operations   []ProviderReconciliation `json:"operations,omitempty"`
}
type Step struct {
	Control               *ReleaseControl `json:"control,omitempty"`
	RollbackTargetHash    string          `json:"rollbackTargetHash,omitempty"`
	RollbackFrom          string          `json:"rollbackFrom,omitempty"`
	RollbackResourceID    string          `json:"rollbackResourceId,omitempty"`
	RollbackExpectedImage string          `json:"rollbackExpectedImage,omitempty"`
	Action                string          `json:"action,omitempty"`
	RecoveryPhase         string          `json:"recoveryPhase,omitempty"`
	ObservationStartedAt  *time.Time      `json:"observationStartedAt,omitempty"`
	Component             string          `json:"component"`
	Ordinal               int             `json:"ordinal"`
	Phase                 string          `json:"phase"`
	ResourceID            string          `json:"resourceId,omitempty"`
	RemoteDeploymentID    string          `json:"remoteDeploymentId,omitempty"`
	Observed              string          `json:"observed,omitempty"`
	Error                 string          `json:"error,omitempty"`
}
type ReleaseSource struct {
	EventID    string `json:"eventId,omitempty"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Ref        string `json:"ref"`
	DeliveryID string `json:"deliveryId"`
}

// OperationQueueObservation contains bounded dispatch evidence only; it never
// contains job arguments, raw errors, credentials or sibling operations.
type OperationQueueObservation struct {
	State         string     `json:"state"`
	Attempt       *int       `json:"attempt,omitempty"`
	MaxAttempts   *int       `json:"maxAttempts,omitempty"`
	ScheduledAt   *time.Time `json:"scheduledAt,omitempty"`
	Paused        *bool      `json:"paused,omitempty"`
	LastErrorCode string     `json:"lastErrorCode,omitempty"`
	ObservedAt    time.Time  `json:"observedAt"`
}

type Deployment struct {
	Queue             *OperationQueueObservation `json:"queue,omitempty"`
	Control           *ReleaseControl            `json:"control,omitempty"`
	Source            *ReleaseSource             `json:"source,omitempty"`
	Operation         string                     `json:"operation,omitempty"`
	ID                string                     `json:"id"`
	ApplicationID     string                     `json:"applicationId"`
	DefinitionVersion int64                      `json:"definitionVersion"`
	State             string                     `json:"state"`
	Manifest          Manifest                   `json:"manifest"`
	Steps             []Step                     `json:"steps"`
	CreatedAt         time.Time                  `json:"createdAt"`
	UpdatedAt         time.Time                  `json:"updatedAt"`
}

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func ResourceName(appID, component string, ordinal int) string {
	return fmt.Sprintf("oap-%s-%s-%d", appID[:12], component, ordinal)
}
func InitialSteps(m Manifest) []Step {
	steps := []Step{}
	ordered, err := m.OrderedComponents()
	if err != nil {
		return steps
	}
	for _, c := range ordered {
		if c.Kind == "database" {
			continue
		}
		for n := 1; n <= c.Instances; n++ {
			steps = append(steps, Step{Component: c.Name, Ordinal: n, Phase: "pending", ResourceID: c.ResourceID})
		}
	}
	if m.Ingress != nil {
		steps = append(steps, Step{Component: "@ingress", Ordinal: 1, Action: "ingress", Phase: "pending"})
	}
	return steps
}
func Terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "attention" || state == "cancelled" || state == "abandoned"
}
