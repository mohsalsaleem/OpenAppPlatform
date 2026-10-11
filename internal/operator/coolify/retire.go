package coolify

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

var managedImageOwner = regexp.MustCompile(`^OpenAppPlatform:[a-f0-9]{32}:[a-z][a-z0-9-]{0,47}$`)

func (c *Client) retirementResource(ctx context.Context, id, owner string) (operator.Resource, error) {
	if !managedImageOwner.MatchString(owner) {
		return operator.Resource{}, errors.New("retirement requires an OAP managed image owner")
	}
	r, err := c.Inspect(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Description != owner || r.ArtifactKind != "image" {
		return r, errors.New("retirement resource ownership or artifact strategy changed")
	}
	return r, nil
}

// Coolify's queued stop API returns no operation ID. The exact resource UUID
// is a confirmation reference, not a claimed native deployment/job identity.
func (c *Client) Retire(ctx context.Context, id, owner string) (string, error) {
	if _, err := c.retirementResource(ctx, id, owner); err != nil {
		return "", err
	}
	if err := c.request(ctx, "POST", "/applications/"+url.PathEscape(id)+"/stop?docker_cleanup=false", nil, nil); err != nil {
		return "", err
	}
	return id, nil
}

func (c *Client) ObserveRetirement(ctx context.Context, remote, id, owner string) (operator.DeploymentStatus, error) {
	if remote != id {
		return operator.DeploymentStatus{}, errors.New("retirement confirmation must reference the exact native resource")
	}
	r, err := c.retirementResource(ctx, id, owner)
	if err != nil {
		return operator.DeploymentStatus{}, err
	}
	state := strings.Split(r.Status, ":")[0]
	switch state {
	case "exited", "stopped":
		return operator.DeploymentStatus{State: "succeeded", ResourceStatus: r.Status}, nil
	case "running", "restarting", "starting":
		return operator.DeploymentStatus{State: "running", ResourceStatus: r.Status}, nil
	default:
		return operator.DeploymentStatus{}, errors.New("native retirement state is unknown; inspect the operator")
	}
}
