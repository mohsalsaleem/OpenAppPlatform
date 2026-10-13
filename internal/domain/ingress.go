package domain

import (
	"errors"
	"net"
	"regexp"
	"strings"
)

type IngressDefinition struct {
	Domain string         `json:"domain"`
	HTTPS  bool           `json:"https"`
	Routes []IngressRoute `json:"routes"`
}
type IngressRoute struct {
	Path        string `json:"path"`
	Component   string `json:"component"`
	StripPrefix bool   `json:"stripPrefix,omitempty"`
}

var hostname = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var routePath = regexp.MustCompile(`^/(?:[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*)?$`)

func (d IngressDefinition) Validate(m Manifest) error {
	if len(d.Domain) > 253 || !strings.Contains(d.Domain, ".") || net.ParseIP(d.Domain) != nil {
		return errors.New("ingress requires one lowercase DNS hostname")
	}
	for _, part := range strings.Split(d.Domain, ".") {
		if !hostname.MatchString(part) {
			return errors.New("ingress requires one lowercase DNS hostname")
		}
	}
	if len(d.Routes) < 1 || len(d.Routes) > 16 {
		return errors.New("ingress requires between 1 and 16 routes")
	}
	components := map[string]Component{}
	for _, c := range m.Components {
		components[c.Name] = c
	}
	paths := map[string]bool{}
	for _, r := range d.Routes {
		if len(r.Path) > 256 || !routePath.MatchString(r.Path) || paths[r.Path] || (r.Path == "/" && r.StripPrefix) {
			return errors.New("route paths must be unique literal segment prefixes; root cannot be stripped")
		}
		paths[r.Path] = true
		c, ok := components[r.Component]
		if !ok || c.Kind != "web" || c.Management != "" || c.ResourceID != "" || c.HostPort != 0 || c.Port < 1 || c.Port > 65535 {
			return errors.New("routes require owned web components with a private port")
		}
	}
	return nil
}
