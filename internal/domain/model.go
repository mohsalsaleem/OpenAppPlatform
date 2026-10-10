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
var digestSuffix = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)

var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

type Component struct {
	ServiceEndpoints map[string]string `json:"serviceEndpoints,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Services         map[string]string `json:"services,omitempty"`
	Name             string            `json:"name"`
	Kind             string            `json:"kind"`
	Image            string            `json:"image"`
	Port             int               `json:"port"`
	HostPort         int               `json:"hostPort,omitempty"`
	Instances        int               `json:"instances"`
	Strategy         string            `json:"strategy"`
	ResourceID       string            `json:"resourceId,omitempty"`
}
type Manifest struct {
	Name        string      `json:"name"`
	Environment string      `json:"environment"`
	TargetID    string      `json:"targetId"`
	Components  []Component `json:"components"`
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
		if !slug.MatchString(c.Name) || names[c.Name] {
			return errors.New("component names must be unique lowercase slugs")
		}
		names[c.Name] = true
		if c.Kind == "" {
			c.Kind = "web"
		}
		if c.Kind != "web" {
			return errors.New("this milestone supports web components; worker and job lifecycles are not implemented")
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
		if c.Port < 1 || c.Port > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
		if c.Image == "" || len(c.Image) > 512 || !imageCharacters.MatchString(c.Image) || (strings.Contains(c.Image, "@") && !digestSuffix.MatchString(c.Image)) {
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
	return nil
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

type Application struct {
	ID        string    `json:"id"`
	Manifest  Manifest  `json:"manifest"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Version   int64     `json:"version"`
}
type Step struct {
	Action               string     `json:"action,omitempty"`
	RecoveryPhase        string     `json:"recoveryPhase,omitempty"`
	ObservationStartedAt *time.Time `json:"observationStartedAt,omitempty"`
	Component            string     `json:"component"`
	Ordinal              int        `json:"ordinal"`
	Phase                string     `json:"phase"`
	ResourceID           string     `json:"resourceId,omitempty"`
	RemoteDeploymentID   string     `json:"remoteDeploymentId,omitempty"`
	Observed             string     `json:"observed,omitempty"`
	Error                string     `json:"error,omitempty"`
}
type Deployment struct {
	Operation         string    `json:"operation,omitempty"`
	ID                string    `json:"id"`
	ApplicationID     string    `json:"applicationId"`
	DefinitionVersion int64     `json:"definitionVersion"`
	State             string    `json:"state"`
	Manifest          Manifest  `json:"manifest"`
	Steps             []Step    `json:"steps"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
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
	for _, c := range m.Components {
		for n := 1; n <= c.Instances; n++ {
			steps = append(steps, Step{Component: c.Name, Ordinal: n, Phase: "pending", ResourceID: c.ResourceID})
		}
	}
	return steps
}
func Terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "attention"
}
