// Package caddy implements the Edge port on Caddy's admin API. OAP owns one
// HTTP server in Caddy's configuration; every other part of the
// configuration (the admin endpoint, other servers, TLS settings) is read
// and written back unchanged.
//
// The configuration carries its own generation and members: the server's
// "@id" is "oap-gen-<n>", and each upstream's "@id" encodes the route member
// it stands for, so Config reads back exactly what Apply wrote, even after
// OAP restarts.
//
// Draining: Caddy finishes requests already in flight when a reload removes
// their upstream, but it forgets the upstream's request count as soon as no
// configuration references the address. So Apply keeps every removed
// upstream that may still be serving in a parking route that never matches
// ("@id" "oapd.<address>"), until its count reaches 0. That keeps InFlight
// true for a removed instance, which drain relies on.
package caddy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// Edge routes OAP's traffic through one Caddy server.
type Edge struct {
	// Server is the name of OAP's server in apps.http.servers.
	Server string
	// Listen is the server's listen addresses.
	Listen []string
	// TLS turns on Caddy's automatic HTTPS for the routed hosts. Off, the
	// server serves plain HTTP only and never contacts a certificate authority.
	TLS bool
	// Host returns the host name the edge dials for an instance.
	Host func(ops.InstanceRef) string

	http *http.Client
}

const (
	genPrefix    = "oap-gen-"
	memberPrefix = "oapm."
	parkPrefix   = "oapd."
	// parkHost is a reserved name (RFC 2606) that no request carries.
	parkHost = "draining.oap.invalid"
	adminURL = "http://localhost:2019"
)

// New returns an edge whose admin API connection is made by dial.
func New(dial func(ctx context.Context) (net.Conn, error), host func(ops.InstanceRef) string) *Edge {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	return &Edge{Server: "oap", Listen: []string{":80"}, Host: host, http: &http.Client{Transport: tr}}
}

// Unix returns an edge whose admin API listens on a unix socket.
func Unix(socket string, host func(ops.InstanceRef) string) *Edge {
	return New(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}, host)
}

// TCP returns an edge whose admin API listens on a TCP address.
func TCP(addr string, host func(ops.InstanceRef) string) *Edge {
	return New(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}, host)
}

func (e *Edge) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, adminURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("caddy: %s %s: %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil && len(bytes.TrimSpace(b)) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

type server struct {
	ID     string   `json:"@id"`
	Listen []string `json:"listen"`
	Routes []route  `json:"routes"`
	// AutoHTTPS is set to disable automatic HTTPS when TLS is off.
	AutoHTTPS *autoHTTPS `json:"automatic_https,omitempty"`
}

type autoHTTPS struct {
	Disable bool `json:"disable"`
}

type route struct {
	Match    []match   `json:"match"`
	Handle   []handler `json:"handle"`
	Terminal bool      `json:"terminal"`
}

type match struct {
	Host []string `json:"host"`
	Path []string `json:"path,omitempty"`
}

type handler struct {
	Handler         string     `json:"handler"`
	StripPathPrefix string     `json:"strip_path_prefix,omitempty"`
	Upstreams       []upstream `json:"upstreams,omitempty"`
	LoadBalancing   *lb        `json:"load_balancing,omitempty"`
}

type upstream struct {
	ID   string `json:"@id"`
	Dial string `json:"dial"`
}

type lb struct {
	SelectionPolicy struct {
		Policy string `json:"policy"`
	} `json:"selection_policy"`
}

// memberID encodes a member and its position in the configuration.
func memberID(i int, m ops.RouteMember) string {
	b, _ := json.Marshal(m)
	return memberPrefix + strconv.Itoa(i) + "." + base64.RawURLEncoding.EncodeToString(b)
}

func parseMemberID(id string) (int, ops.RouteMember, bool) {
	rest, ok := strings.CutPrefix(id, memberPrefix)
	if !ok {
		return 0, ops.RouteMember{}, false
	}
	n, enc, ok := strings.Cut(rest, ".")
	i, err := strconv.Atoi(n)
	b, err2 := base64.RawURLEncoding.DecodeString(enc)
	var m ops.RouteMember
	if !ok || err != nil || err2 != nil || json.Unmarshal(b, &m) != nil {
		return 0, ops.RouteMember{}, false
	}
	return i, m, true
}

// Config reads OAP's server back from Caddy: the members and generation
// last applied. A Caddy without OAP's server is at generation 0.
func (e *Edge) Config(ctx context.Context) (ops.EdgeConfig, uint64, error) {
	cfg, gen, _, err := e.read(ctx)
	return cfg, gen, err
}

// read returns the members, generation and parked addresses.
func (e *Edge) read(ctx context.Context) (ops.EdgeConfig, uint64, []string, error) {
	// Reading a path that doesn't exist yet is an error in Caddy, so read the
	// whole configuration and look for the server in it.
	var whole struct {
		Apps struct {
			HTTP struct {
				Servers map[string]*server `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := e.call(ctx, http.MethodGet, "/config/", nil, &whole); err != nil {
		return ops.EdgeConfig{}, 0, nil, err
	}
	s := whole.Apps.HTTP.Servers[e.Server]
	if s == nil {
		return ops.EdgeConfig{}, 0, nil, nil
	}
	gen, err := strconv.ParseUint(strings.TrimPrefix(s.ID, genPrefix), 10, 64)
	if !strings.HasPrefix(s.ID, genPrefix) || err != nil {
		return ops.EdgeConfig{}, 0, nil, fmt.Errorf("caddy: server %q has no OAP generation (@id %q); it wasn't written by OAP", e.Server, s.ID)
	}
	type indexed struct {
		i int
		m ops.RouteMember
	}
	var ms []indexed
	var parked []string
	for _, r := range s.Routes {
		for _, h := range r.Handle {
			for _, u := range h.Upstreams {
				if strings.HasPrefix(u.ID, parkPrefix) {
					parked = append(parked, u.Dial)
					continue
				}
				i, m, ok := parseMemberID(u.ID)
				if !ok {
					return ops.EdgeConfig{}, 0, nil, fmt.Errorf("caddy: upstream %q in server %q wasn't written by OAP", u.Dial, e.Server)
				}
				ms = append(ms, indexed{i, m})
			}
		}
	}
	sort.Slice(ms, func(a, b int) bool { return ms[a].i < ms[b].i })
	cfg := ops.EdgeConfig{}
	for _, x := range ms {
		cfg.Members = append(cfg.Members, x.m)
	}
	return cfg, gen, parked, nil
}

// Apply replaces OAP's server with cfg. Caddy loads a configuration
// synchronously and finishes the old one's requests gracefully, so the new
// generation is in effect when Apply returns. Removed upstreams are parked
// while they may still be serving, and dropped once they aren't.
func (e *Edge) Apply(ctx context.Context, cfg ops.EdgeConfig) (uint64, error) {
	cur, gen, parked, err := e.read(ctx)
	if err != nil {
		return 0, err
	}
	if same(cur, cfg) && gen > 0 {
		return gen, nil
	}
	gen++
	counts, err := e.counts(ctx)
	if err != nil {
		return 0, err
	}
	active := map[string]bool{}
	for _, m := range cfg.Members {
		active[e.dial(m)] = true
	}
	park := map[string]bool{}
	for _, m := range cur.Members {
		if d := e.dial(m); !active[d] {
			park[d] = true // just removed: it may be serving
		}
	}
	for _, d := range parked {
		if !active[d] && counts[d] > 0 {
			park[d] = true // still serving
		}
	}
	var whole map[string]any
	if err := e.call(ctx, http.MethodGet, "/config/", nil, &whole); err != nil {
		return 0, err
	}
	if whole == nil {
		whole = map[string]any{}
	}
	apps := child(whole, "apps")
	httpApp := child(apps, "http")
	servers := child(httpApp, "servers")
	servers[e.Server] = e.build(gen, cfg, park)
	if err := e.call(ctx, http.MethodPost, "/load", whole, nil); err != nil {
		return 0, err
	}
	return gen, nil
}

func child(m map[string]any, key string) map[string]any {
	if c, ok := m[key].(map[string]any); ok {
		return c
	}
	c := map[string]any{}
	m[key] = c
	return c
}

func same(a, b ops.EdgeConfig) bool {
	if len(a.Members) != len(b.Members) {
		return false
	}
	for i := range a.Members {
		if a.Members[i] != b.Members[i] {
			return false
		}
	}
	return true
}

// build turns members into routes: one per host, path prefix and prefix
// handling, longest prefix first, balancing round robin over its members.
func (e *Edge) dial(m ops.RouteMember) string {
	return net.JoinHostPort(e.Host(m.Instance), strconv.Itoa(m.Port))
}

func (e *Edge) build(gen uint64, cfg ops.EdgeConfig, park map[string]bool) server {
	type key struct {
		host, prefix string
		strip        bool
	}
	groups := map[key][]upstream{}
	var keys []key
	for i, m := range cfg.Members {
		k := key{m.Host, m.PathPrefix, m.StripPrefix}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], upstream{ID: memberID(i, m), Dial: e.dial(m)})
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if len(a.prefix) != len(b.prefix) {
			return len(a.prefix) > len(b.prefix)
		}
		if a.host != b.host {
			return a.host < b.host
		}
		return !a.strip && b.strip
	})
	s := server{ID: genPrefix + strconv.FormatUint(gen, 10), Listen: e.Listen, Routes: []route{}}
	if !e.TLS {
		s.AutoHTTPS = &autoHTTPS{Disable: true}
	}
	for _, k := range keys {
		m := match{Host: []string{k.host}}
		if p := strings.TrimSuffix(k.prefix, "/"); p != "" {
			m.Path = []string{p, p + "/*"}
		}
		proxy := handler{Handler: "reverse_proxy", Upstreams: groups[k], LoadBalancing: &lb{}}
		proxy.LoadBalancing.SelectionPolicy.Policy = "round_robin"
		var hs []handler
		if k.strip && strings.TrimSuffix(k.prefix, "/") != "" {
			hs = append(hs, handler{Handler: "rewrite", StripPathPrefix: strings.TrimSuffix(k.prefix, "/")})
		}
		s.Routes = append(s.Routes, route{Match: []match{m}, Handle: append(hs, proxy), Terminal: true})
	}
	if len(park) > 0 {
		var ups []upstream
		for _, d := range sortedSet(park) {
			ups = append(ups, upstream{ID: parkPrefix + base64.RawURLEncoding.EncodeToString([]byte(d)), Dial: d})
		}
		s.Routes = append(s.Routes, route{Match: []match{{Host: []string{parkHost}}},
			Handle: []handler{{Handler: "reverse_proxy", Upstreams: ups}}, Terminal: true})
	}
	return s
}

// Confirmed returns once a generation is in effect. Apply is synchronous,
// so any generation up to the current one is; a later one was never applied.
func (e *Edge) Confirmed(ctx context.Context, generation uint64) error {
	_, cur, err := e.Config(ctx)
	if err != nil {
		return err
	}
	if generation > cur {
		return fmt.Errorf("caddy: generation %d was never applied (current %d)", generation, cur)
	}
	return nil
}

// InFlight returns the requests Caddy is proxying to an instance, from its
// upstream counters. A removed instance stays parked, and so counted, until
// its requests finish (see the package comment).
func (e *Edge) InFlight(ctx context.Context, instance ops.InstanceRef) (int, error) {
	counts, err := e.counts(ctx)
	if err != nil {
		return 0, err
	}
	host := e.Host(instance)
	n := 0
	for addr, c := range counts {
		if h, _, err := net.SplitHostPort(addr); err == nil && h == host {
			n += c
		}
	}
	return n, nil
}

// counts returns Caddy's in-flight request count per upstream address.
func (e *Edge) counts(ctx context.Context) (map[string]int, error) {
	var ups []struct {
		Address     string `json:"address"`
		NumRequests int    `json:"num_requests"`
	}
	if err := e.call(ctx, http.MethodGet, "/reverse_proxy/upstreams", nil, &ups); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, u := range ups {
		if u.NumRequests > 0 {
			out[u.Address] += u.NumRequests
		}
	}
	return out, nil
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
