package docker

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) CheckRollbackConfiguration(ctx context.Context, ref string, component domain.Component) error {
	container, err := c.inspectContainer(ctx, ref)
	if err != nil {
		return errors.New("cannot verify rollback container")
	}
	if !c.owned(container) || len(container.Mounts) != 0 {
		return errors.New("rollback requires an owned container without mounted data")
	}
	expectedKey := strconv.Itoa(component.Port) + "/tcp"
	count := 0
	for key, bindings := range container.HostConfig.PortBindings {
		for _, binding := range bindings {
			count++
			if component.HostPort == 0 || key != expectedKey || binding.HostPort != strconv.Itoa(component.HostPort) || binding.HostIP != c.settings.HostBindIP {
				return errors.New("rollback host port configuration differs")
			}
		}
	}
	if (component.HostPort == 0 && count != 0) || (component.HostPort != 0 && count != 1) {
		return errors.New("rollback port bindings cannot be verified")
	}
	actual := map[string]string{}
	for _, value := range container.Config.Env {
		key, value, ok := strings.Cut(value, "=")
		if ok {
			actual[key] = value
		}
	}
	for key, value := range component.Env {
		if actualValue, exists := actual[key]; !exists || actualValue != value {
			return errors.New("rollback environment configuration differs")
		}
	}
	return nil
}

// CachedImageID exposes a read-only content address for already-cached images.
// It never pulls an image or modifies the daemon.
func (c *Client) CachedImageID(ctx context.Context, ref string) (string, error) {
	var result struct {
		ID string `json:"Id"`
	}
	if err := c.request(ctx, "GET", "/images/"+url.PathEscape(ref)+"/json", nil, &result); err != nil {
		return "", errors.New("cached image cannot be inspected")
	}
	if result.ID == "" {
		return "", errors.New("cached image has no content ID")
	}
	return result.ID, nil
}
