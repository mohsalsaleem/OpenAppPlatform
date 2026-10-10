// Package coolify implements the bounded, target-scoped Coolify REST adapter.
package coolify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type Client struct {
	base, token string
	target      domain.Target
	http        *http.Client
}

func New(t domain.Target, token string) (*Client, error) {
	u, e := url.Parse(t.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid Coolify endpoint")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return nil, errors.New("Coolify requires HTTPS except for localhost")
	}
	if token == "" {
		return nil, errors.New("Coolify token environment variable is missing")
	}
	return &Client{base: strings.TrimRight(t.URL, "/"), token: token, target: t, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Capabilities() operator.Capabilities {
	return operator.Capabilities{HTTPHealthChecks: true, ImageRollback: true, ManagementHandoff: true, Standard: true, Discovery: true, ImmutableImages: true, Restart: true, Environment: true, ServiceEndpoints: true}
}
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, c.base+"/api/v1"+path, body)
	if e != nil {
		return errors.New("invalid Coolify request")
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	res, e := c.http.Do(r)
	if e != nil {
		return errors.New("Coolify connection failed or timed out")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Coolify %s %s returned HTTP %d", method, path, res.StatusCode)
	}
	if output == nil {
		return nil
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if e != nil {
		return errors.New("cannot read Coolify response")
	}
	if len(b) > 2<<20 {
		return errors.New("Coolify response exceeds limit")
	}
	if e = json.Unmarshal(b, output); e != nil {
		return errors.New("invalid Coolify response")
	}
	return nil
}

type application struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	FQDN          string `json:"fqdn"`
	Image         string `json:"docker_registry_image_name"`
	Tag           string `json:"docker_registry_image_tag"`
	BuildPack     string `json:"build_pack"`
	Ports         string `json:"ports_exposes"`
	EnvironmentID int    `json:"environment_id"`
}

func resource(a application) operator.Resource {
	image := a.Image
	if a.Tag != "" && (!strings.Contains(image, "@") || strings.HasSuffix(image, "@sha256")) {
		image += ":" + a.Tag
	}
	port, _ := strconv.Atoi(strings.Split(a.Ports, ",")[0])
	kind := "source"
	if a.BuildPack == "dockerimage" {
		kind = "image"
	}
	return operator.Resource{Port: port, ArtifactKind: kind, ID: a.UUID, Name: a.Name, Description: a.Description, Status: a.Status, URL: a.FQDN, Image: image}
}
func (c *Client) Discover(ctx context.Context) ([]operator.Resource, error) {
	var env struct {
		ID           int           `json:"id"`
		Name         string        `json:"name"`
		Applications []application `json:"applications"`
	}
	if e := c.request(ctx, "GET", "/projects/"+url.PathEscape(c.target.ProjectID)+"/"+url.PathEscape(c.target.Environment), nil, &env); e != nil {
		return nil, e
	}
	out := []operator.Resource{}
	for _, a := range env.Applications {
		r := resource(a)
		r.Environment = c.target.Environment
		r.ProjectID = c.target.ProjectID
		out = append(out, r)
	}
	return out, nil
}
func (c *Client) Inspect(ctx context.Context, id string) (operator.Resource, error) {
	// Scope through environment discovery before exposing resource details.
	resources, e := c.Discover(ctx)
	if e != nil {
		return operator.Resource{}, e
	}
	found := false
	for _, r := range resources {
		if r.ID == id {
			found = true
			break
		}
	}
	if !found {
		return operator.Resource{}, errors.New("resource is outside this target environment")
	}
	var a application
	if e = c.request(ctx, "GET", "/applications/"+url.PathEscape(id), nil, &a); e != nil {
		return operator.Resource{}, e
	}
	r := resource(a)
	r.ProjectID = c.target.ProjectID
	r.Environment = c.target.Environment
	return r, nil
}
func splitImage(image string) (string, string) {
	if before, digest, ok := strings.Cut(image, "@sha256:"); ok {
		return before + "@sha256", digest
	}
	i := strings.LastIndex(image, ":")
	if i > strings.LastIndex(image, "/") {
		return image[:i], image[i+1:]
	}
	return image, "latest"
}
func (c *Client) Ensure(ctx context.Context, s operator.Spec) (operator.Resource, error) {

	resources, e := c.Discover(ctx)
	if e != nil {
		return operator.Resource{}, e
	}
	var found *operator.Resource
	for _, r := range resources {
		if r.Name == s.Name {
			if r.Description != s.Ownership {
				return operator.Resource{}, errors.New("resource name belongs to another owner")
			}
			if found != nil {
				return operator.Resource{}, errors.New("duplicate owned resource names require reconciliation")
			}
			copy := r
			found = &copy
		}
	}
	image, tag := splitImage(s.Component.Image)
	if found != nil {
		if found.ArtifactKind != "image" {
			return operator.Resource{}, errors.New("owned resource build strategy changed; reconciliation required")
		}
		if e = c.syncVariables(ctx, found.ID, s); e != nil {
			return operator.Resource{}, e
		}
		// Adopted resources never enter this path. Only controller-owned image workloads can be patched.
		data := map[string]any{"docker_registry_image_name": image, "docker_registry_image_tag": tag, "ports_exposes": strconv.Itoa(s.Component.Port), "ports_mappings": ""}
		if s.Component.HostPort != 0 {
			data["ports_mappings"] = fmt.Sprintf("%d:%d", s.Component.HostPort, s.Component.Port)
		}
		applyHealthCheck(data, s.Component)
		if e = c.request(ctx, "PATCH", "/applications/"+url.PathEscape(found.ID), data, nil); e != nil {
			return operator.Resource{}, e
		}
		return *found, nil
	}
	data := map[string]any{"name": s.Name, "description": s.Ownership, "project_uuid": c.target.ProjectID, "server_uuid": c.target.ServerID, "environment_name": c.target.Environment, "docker_registry_image_name": image, "ports_exposes": strconv.Itoa(s.Component.Port), "instant_deploy": false, "limits_memory": "128m", "limits_cpus": "0.25"}
	if tag != "" {
		data["docker_registry_image_tag"] = tag
	}
	if s.Component.HostPort != 0 {
		data["ports_mappings"] = fmt.Sprintf("%d:%d", s.Component.HostPort, s.Component.Port)
	}
	applyHealthCheck(data, s.Component)
	var result struct {
		UUID string `json:"uuid"`
	}
	if e = c.request(ctx, "POST", "/applications/dockerimage", data, &result); e != nil {
		return operator.Resource{}, e
	}
	if result.UUID == "" {
		return operator.Resource{}, errors.New("Coolify create returned no resource id")
	}
	if e = c.syncVariables(ctx, result.UUID, s); e != nil {
		return operator.Resource{}, e
	}
	return operator.Resource{ID: result.UUID, Name: s.Name, Description: s.Ownership, Image: s.Component.Image}, nil
}
func (c *Client) Deploy(ctx context.Context, id string) (string, error) {
	var result struct {
		Deployments []struct {
			UUID string `json:"deployment_uuid"`
		} `json:"deployments"`
	}
	if e := c.request(ctx, "POST", "/deploy", map[string]any{"uuid": id, "force": true}, &result); e != nil {
		return "", e
	}
	if len(result.Deployments) != 1 || result.Deployments[0].UUID == "" {
		return "", errors.New("Coolify did not return one deployment id")
	}
	return result.Deployments[0].UUID, nil
}
func deploymentState(s string) string {
	switch s {
	case "finished":
		return "succeeded"
	case "failed", "cancelled", "canceled":
		return "failed"
	default:
		return "running"
	}
}
func (c *Client) Observe(ctx context.Context, id, resourceID string) (operator.DeploymentStatus, error) {
	// Coolify omits numeric IDs from resource projections. Verify association via
	// resource-scoped history instead of trusting a global deployment lookup.
	resource, err := c.Inspect(ctx, resourceID)
	if err != nil {
		return operator.DeploymentStatus{}, err
	}
	for skip := 0; skip < 100; skip += 20 {
		var result struct {
			Count       int `json:"count"`
			Deployments []struct {
				UUID   string `json:"deployment_uuid"`
				Status string `json:"status"`
			} `json:"deployments"`
		}
		path := "/deployments/applications/" + url.PathEscape(resourceID) + "?take=20&skip=" + strconv.Itoa(skip)
		if err = c.request(ctx, "GET", path, nil, &result); err != nil {
			return operator.DeploymentStatus{}, err
		}
		for _, deployment := range result.Deployments {
			if deployment.UUID == id {
				return operator.DeploymentStatus{State: deploymentState(deployment.Status), ResourceStatus: resource.Status}, nil
			}
		}
		if len(result.Deployments) < 20 || skip+len(result.Deployments) >= result.Count {
			break
		}
	}
	return operator.DeploymentStatus{}, errors.New("provider deployment is not in this resource's recent history; inspect the operator")
}
func (c *Client) Logs(ctx context.Context, id string, lines int) (string, error) {
	if lines < 1 || lines > 200 {
		lines = 100
	}
	if _, e := c.Inspect(ctx, id); e != nil {
		return "", e
	}
	var res struct {
		Logs string `json:"logs"`
	}
	if e := c.request(ctx, "GET", "/applications/"+url.PathEscape(id)+"/logs?lines="+strconv.Itoa(lines), nil, &res); e != nil {
		return "", e
	}
	return strings.ReplaceAll(res.Logs, c.token, "[REDACTED]"), nil
}

func (c *Client) Restart(ctx context.Context, id string) (string, error) {
	if _, e := c.Inspect(ctx, id); e != nil {
		return "", e
	}
	var result struct {
		UUID string `json:"deployment_uuid"`
	}
	if e := c.request(ctx, "POST", "/applications/"+url.PathEscape(id)+"/restart", nil, &result); e != nil {
		return "", e
	}
	if result.UUID == "" {
		return "", errors.New("Coolify did not return a restart deployment ID")
	}
	return result.UUID, nil
}
