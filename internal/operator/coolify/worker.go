package coolify

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

// Coolify's image application model retains internal port metadata. Worker
// applications use a placeholder without a listener, domain or host mapping.
func nativePorts(c domain.Component) string {
	if c.Kind == "worker" {
		return "80"
	}
	return strconv.Itoa(c.Port)
}
func (c *Client) checkWorkerConfiguration(ctx context.Context, ref string) error {
	var fields struct {
		Domains  string `json:"fqdn"`
		Mappings string `json:"ports_mappings"`
		Ports    string `json:"ports_exposes"`
	}
	if err := c.request(ctx, "GET", "/applications/"+url.PathEscape(ref), nil, &fields); err != nil {
		return errors.New("worker native configuration cannot be verified")
	}
	if strings.TrimSpace(fields.Domains) != "" || strings.TrimSpace(fields.Mappings) != "" || fields.Ports != "80" {
		return errors.New("worker native routing or port configuration changed; review it in the operator")
	}
	return nil
}
