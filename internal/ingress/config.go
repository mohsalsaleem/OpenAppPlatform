// Package ingress compiles bounded application routes for a dedicated OAP-owned
// Caddy instance. It is not yet wired into application admission or deployment.
package ingress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

// Definition is the proposed application ingress contract, separate until the
// controller can enforce domain ownership and durably reconcile publications.
type Definition struct {
	Domain string  `json:"domain"`
	HTTPS  bool    `json:"https"`
	Routes []Route `json:"routes"`
}
type Route struct {
	Path        string `json:"path"`
	Component   string `json:"component"`
	StripPrefix bool   `json:"stripPrefix,omitempty"`
}

// Member must come from exact owned runtime bindings, never agent-supplied URLs.
// The future controller remains responsible for binding identity/authority.
type Member struct {
	ApplicationID, TargetID, Component, Address string
	Ordinal                                     int
	Ready                                       bool
	ObservedAt                                  time.Time
}

// Options are host configuration, never fields controlled by an application.
type Options struct {
	AdminSocket         string
	HTTPPort, HTTPSPort int
	// InternalCA is only for offline fixture HTTPS, not public certificate issuance.
	InternalCA bool
}
type Candidate struct {
	raw  []byte
	hash string
}

func (c Candidate) JSON() []byte { return append([]byte(nil), c.raw...) }
func (c Candidate) Hash() string { return c.hash }

var hostname = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var routePath = regexp.MustCompile(`^/(?:[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*)?$`)

func (d Definition) Validate(m domain.Manifest) error {
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
	components := map[string]domain.Component{}
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

// Compile produces one complete dedicated-proxy configuration. Input order does
// not affect its hash. /api matches /api and /api/*, never /apiculture. The most
// specific path wins. Unready members are excluded; stale observations block.
func Compile(appID string, m domain.Manifest, d Definition, members []Member, o Options, now time.Time) (Candidate, error) {
	fail := func(message string) (Candidate, error) { return Candidate{}, errors.New(message) }
	if appID == "" || len(appID) > 128 || m.TargetID == "" {
		return fail("application and target identity are required")
	}
	if err := d.Validate(m); err != nil {
		return Candidate{}, err
	}
	if !strings.HasPrefix(o.AdminSocket, "/run/oap-ingress/") || strings.Contains(o.AdminSocket, "..") || strings.ContainsAny(o.AdminSocket, "|\x00\r\n") || len(o.AdminSocket) > 100 {
		return fail("admin socket must be a private /run/oap-ingress path")
	}
	if o.HTTPPort < 1 || o.HTTPPort > 65535 || o.HTTPSPort < 1 || o.HTTPSPort > 65535 || o.HTTPPort == o.HTTPSPort {
		return fail("distinct valid HTTP and HTTPS listener ports are required")
	}
	if !d.HTTPS && o.InternalCA {
		return fail("internal CA requires HTTPS")
	}
	if len(members) > 64 {
		return fail("ingress membership exceeds 64 instances")
	}
	components := map[string]domain.Component{}
	for _, c := range m.Components {
		components[c.Name] = c
	}
	seen := map[string]bool{}
	addresses := map[string]bool{}
	ready := map[string][]string{}
	for _, member := range members {
		c, ok := components[member.Component]
		if !ok || member.ApplicationID != appID || member.TargetID != m.TargetID || c.Management != "" || c.ResourceID != "" || c.Kind != "web" || member.Ordinal < 1 || member.Ordinal > c.Instances {
			return fail("member does not match desired owned web instance")
		}
		key := fmt.Sprintf("%s/%d", member.Component, member.Ordinal)
		if seen[key] {
			return fail("duplicate instance membership")
		}
		seen[key] = true
		if member.ObservedAt.IsZero() || now.Sub(member.ObservedAt) > 15*time.Second || member.ObservedAt.After(now.Add(time.Second)) {
			return fail("route membership requires fresh runtime evidence")
		}
		host, port, err := net.SplitHostPort(member.Address)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsPrivate() || ip.IsLoopback() || port != strconv.Itoa(c.Port) || addresses[member.Address] {
			return fail("member requires a unique private runtime IP and declared component port")
		}
		addresses[member.Address] = true
		if member.Ready {
			ready[member.Component] = append(ready[member.Component], member.Address)
		}
	}
	routes := append([]Route(nil), d.Routes...)
	sort.Slice(routes, func(i, j int) bool {
		if len(routes[i].Path) != len(routes[j].Path) {
			return len(routes[i].Path) > len(routes[j].Path)
		}
		return routes[i].Path < routes[j].Path
	})
	native := []any{}
	for _, r := range routes {
		backends := ready[r.Component]
		if len(backends) == 0 {
			return fail("each route requires at least one ready instance")
		}
		sort.Strings(backends)
		upstreams := []any{}
		for _, address := range backends {
			upstreams = append(upstreams, map[string]any{"dial": address})
		}
		handlers := []any{}
		if r.StripPrefix {
			handlers = append(handlers, map[string]any{"handler": "rewrite", "strip_path_prefix": r.Path})
		}
		handlers = append(handlers, map[string]any{"handler": "reverse_proxy", "upstreams": upstreams, "load_balancing": map[string]any{"selection_policy": map[string]any{"policy": "round_robin"}}, "health_checks": map[string]any{"passive": map[string]any{"fail_duration": int64(10 * time.Second), "max_fails": 1}}, "stream_close_delay": int64(30 * time.Second)})
		match := map[string]any{"host": []string{d.Domain}}
		if r.Path != "/" {
			match["path"] = []string{r.Path, r.Path + "/*"}
		}
		native = append(native, map[string]any{"match": []any{match}, "handle": handlers, "terminal": true})
	}
	// A default 404 prevents unknown hosts from reaching any application.
	native = append(native, map[string]any{"handle": []any{map[string]any{"handler": "static_response", "status_code": 404}}, "terminal": true})
	port := o.HTTPPort
	if d.HTTPS {
		port = o.HTTPSPort
	}
	server := map[string]any{"listen": []string{":" + strconv.Itoa(port)}, "routes": native}
	apps := map[string]any{"http": map[string]any{"http_port": o.HTTPPort, "https_port": o.HTTPSPort, "servers": map[string]any{"oap": server}}}
	if !d.HTTPS {
		server["automatic_https"] = map[string]any{"disable": true}
	} else {
		issuer := map[string]any{"module": "acme", "ca": "https://acme-v02.api.letsencrypt.org/directory"}
		if o.InternalCA {
			issuer = map[string]any{"module": "internal"}
		}
		apps["tls"] = map[string]any{"automation": map[string]any{"policies": []any{map[string]any{"subjects": []string{d.Domain}, "issuers": []any{issuer}}}}}
	}
	raw, err := json.Marshal(map[string]any{"admin": map[string]any{"listen": "unix/" + o.AdminSocket + "|0600", "config": map[string]any{"persist": true}}, "apps": apps})
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{raw: raw, hash: Hash(raw)}, nil
}

// Hash normalizes JSON object order/whitespace; array order remains significant.
func Hash(raw []byte) string {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ""
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ""
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
