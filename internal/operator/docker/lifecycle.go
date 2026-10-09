package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"net/url"
	"strconv"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

func revision(s operator.Spec, imageID string) string {
	b, _ := json.Marshal(struct {
		Spec  operator.Spec
		Image string
	}{s, imageID})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (c *Client) network(ctx context.Context, ownership string) (string, error) {
	pieces := strings.Split(ownership, ":")
	if len(pieces) != 3 || pieces[0] != "OpenAppPlatform" || len(pieces[1]) != 32 {
		return "", errors.New("invalid application ownership identity")
	}
	name := "oap-" + c.target.ID + "-" + pieces[1][:12]
	var existing struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	e := c.request(ctx, "GET", "/networks/"+url.PathEscape(name), nil, &existing)
	if e == nil {
		if existing.Labels[prefix+"target"] != c.target.ID || existing.Labels[prefix+"application"] != pieces[1] {
			return "", errors.New("Docker network name belongs to another owner")
		}
		return name, nil
	}
	if !isStatus(e, 404) {
		return "", e
	}
	var result struct {
		ID string `json:"Id"`
	}
	e = c.request(ctx, "POST", "/networks/create", map[string]any{"Name": name, "Driver": "bridge", "CheckDuplicate": true, "Labels": map[string]string{prefix + "target": c.target.ID, prefix + "application": pieces[1], prefix + "environment": c.target.Environment}}, &result)
	return name, e
}
func (c *Client) Ensure(ctx context.Context, s operator.Spec) (operator.Resource, error) {
	imageID, e := c.image(ctx, s.Component.Image)
	if e != nil {
		return operator.Resource{}, e
	}
	rev := revision(s, imageID)
	active, e := c.inspectContainer(ctx, s.Name)
	if e == nil {
		if !c.owned(active) || active.Config.Labels[prefix+"owner"] != s.Ownership || active.Config.Labels[prefix+"reference"] != s.Name {
			return operator.Resource{}, errors.New("container name belongs to another owner")
		}
		if active.Config.Labels[prefix+"revision"] == rev {
			return c.projection(active), nil
		}
	} else if !isStatus(e, 404) {
		return operator.Resource{}, e
	}
	candidateName := s.Name + "-next"
	candidate, e := c.inspectContainer(ctx, candidateName)
	if e == nil {
		if !c.owned(candidate) || candidate.Config.Labels[prefix+"owner"] != s.Ownership {
			return operator.Resource{}, errors.New("candidate container belongs to another owner")
		}
		if candidate.Config.Labels[prefix+"revision"] == rev {
			return operator.Resource{ID: s.Name, Name: s.Name, Image: s.Component.Image, ArtifactKind: "image", Status: "prepared"}, nil
		}
		if e = c.removeStopped(ctx, candidate); e != nil {
			return operator.Resource{}, e
		}
	} else if !isStatus(e, 404) {
		return operator.Resource{}, e
	}
	network, e := c.network(ctx, s.Ownership)
	if e != nil {
		return operator.Resource{}, e
	}
	port := strconv.Itoa(s.Component.Port) + "/tcp"
	labels := map[string]string{prefix + "target": c.target.ID, prefix + "environment": c.target.Environment, prefix + "reference": s.Name, prefix + "owner": s.Ownership, prefix + "revision": rev, prefix + "artifact": s.Component.Image, prefix + "port": strconv.Itoa(s.Component.Port)}
	host := map[string]any{"NetworkMode": network, "RestartPolicy": map[string]string{"Name": "unless-stopped"}, "Memory": int64(128 << 20), "NanoCpus": int64(250000000)}
	if s.Component.HostPort != 0 {
		host["PortBindings"] = map[string]any{port: []map[string]string{{"HostIp": c.settings.HostBindIP, "HostPort": strconv.Itoa(s.Component.HostPort)}}}
	}
	input := map[string]any{"Image": imageID, "Labels": labels, "ExposedPorts": map[string]any{port: map[string]any{}}, "HostConfig": host, "NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{network: map[string]any{"Aliases": []string{s.Component.Name}}}}}
	var result struct {
		ID string `json:"Id"`
	}
	if e = c.request(ctx, "POST", "/containers/create?name="+url.QueryEscape(candidateName), input, &result); e != nil {
		return operator.Resource{}, e
	}
	if result.ID == "" {
		return operator.Resource{}, errors.New("Docker returned no candidate container ID")
	}
	return operator.Resource{ID: s.Name, Name: s.Name, Image: s.Component.Image, ArtifactKind: "image", Status: "prepared"}, nil
}
func (c *Client) Deploy(ctx context.Context, ref string) (string, error) {
	candidate, e := c.inspectContainer(ctx, ref+"-next")
	if isStatus(e, 404) {
		active, e := c.inspectContainer(ctx, ref)
		if e != nil {
			return "", e
		}
		if !c.owned(active) {
			return "", errors.New("container is outside target")
		}
		if !active.State.Running {
			if e = c.request(ctx, "POST", "/containers/"+url.PathEscape(active.ID)+"/start", nil, nil); e != nil {
				return "", e
			}
		}
		return active.ID, nil
	}
	if e != nil {
		return "", e
	}
	if !c.owned(candidate) || candidate.Config.Labels[prefix+"reference"] != ref {
		return "", errors.New("candidate is outside target")
	}
	old, e := c.inspectContainer(ctx, ref)
	if e == nil {
		if !c.owned(old) || old.Config.Labels[prefix+"owner"] != candidate.Config.Labels[prefix+"owner"] {
			return "", errors.New("active container ownership differs")
		}
		previous, e := c.inspectContainer(ctx, ref+"-previous")
		if e == nil {
			if e = c.removeStopped(ctx, previous); e != nil {
				return "", e
			}
		} else if !isStatus(e, 404) {
			return "", e
		}
		if old.State.Running {
			if e = c.request(ctx, "POST", "/containers/"+url.PathEscape(old.ID)+"/stop?t=10", nil, nil); e != nil {
				return "", e
			}
		}
		if e = c.request(ctx, "POST", "/containers/"+url.PathEscape(old.ID)+"/rename?name="+url.QueryEscape(ref+"-previous"), nil, nil); e != nil {
			return "", e
		}
	} else if !isStatus(e, 404) {
		return "", e
	}
	if e = c.request(ctx, "POST", "/containers/"+url.PathEscape(candidate.ID)+"/rename?name="+url.QueryEscape(ref), nil, nil); e != nil {
		return "", e
	}
	if e = c.request(ctx, "POST", "/containers/"+url.PathEscape(candidate.ID)+"/start", nil, nil); e != nil {
		return "", e
	}
	return candidate.ID, nil
}
func (c *Client) Observe(ctx context.Context, deploymentID, ref string) (operator.DeploymentStatus, error) {
	active, e := c.inspectContainer(ctx, ref)
	if e != nil {
		return operator.DeploymentStatus{}, e
	}
	if !c.owned(active) || active.ID != deploymentID {
		return operator.DeploymentStatus{}, errors.New("Docker instance changed while observing deployment")
	}
	status := c.projection(active).Status
	state := "running"
	if active.State.Running {
		if active.State.Health == nil || active.State.Health.Status == "healthy" {
			state = "succeeded"
		}
		if active.State.Health != nil && active.State.Health.Status == "unhealthy" {
			state = "failed"
		}
	} else if active.State.Status == "exited" || active.State.Status == "dead" {
		state = "failed"
	}
	return operator.DeploymentStatus{State: state, ResourceStatus: status}, nil
}
func (c *Client) Stop(ctx context.Context, ref string) error {
	if _, e := c.Inspect(ctx, ref); e != nil {
		return e
	}
	current, e := c.inspectContainer(ctx, ref)
	if e != nil {
		return e
	}
	if !current.State.Running {
		return nil
	}
	return c.request(ctx, "POST", "/containers/"+url.PathEscape(ref)+"/stop?t=10", nil, nil)
}

var _ operator.Adapter = (*Client)(nil)
