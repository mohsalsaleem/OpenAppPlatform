package caddy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// DNSEdge routes OAP's traffic through Caddy without reloading Caddy when
// route membership changes (ADR 0001). Caddy's configuration holds one
// route per host, path prefix and port, whose upstreams come from DNS
// (dynamic_upstreams "a"); the names resolve, through the responder DNSEdge
// serves, to the instances that are members right now. Adding or removing
// an instance changes only the DNS answer. Caddy reloads only when the set
// of hosts, paths or ports changes.
//
// Membership and the generation are kept in a state file, so they survive
// oapd restarts. A change is in effect once Caddy's cached answers have
// expired: Confirmed waits two refresh intervals and a margin.
type DNSEdge struct {
	// Admin talks to Caddy's admin API and supplies the server name,
	// listen addresses, TLS setting and instance host names.
	Admin *Edge
	// Resolver is the address Caddy queries, for example
	// "udp/oap-a1b2c3-oapd:53".
	Resolver string
	// Refresh is how long Caddy caches an answer.
	Refresh time.Duration
	// StatePath is the file holding membership and the generation.
	StatePath string
	// Lookup resolves an instance's host name to its addresses.
	Lookup func(ctx context.Context, host string) ([]net.IP, error)
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Extra are fixed routes ahead of the applications', such as OAP's own
	// API: a host and the address Caddy proxies it to.
	Extra []StaticRoute

	mu     sync.Mutex
	state  dnsState
	loaded bool
	cache  map[string]cached
}

type dnsState struct {
	Generation uint64            `json:"generation"`
	Members    []ops.RouteMember `json:"members"`
	ChangedAt  time.Time         `json:"changed_at"`
}

// StaticRoute is a host Caddy proxies to one fixed upstream.
type StaticRoute struct {
	Host, Dial string
}

type cached struct {
	ips []net.IP
	at  time.Time
}

const dnsServerID = "oap-dns"

func (e *DNSEdge) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *DNSEdge) refresh() time.Duration {
	if e.Refresh <= 0 {
		return time.Second
	}
	return e.Refresh
}

// Settle is how long after a change Caddy may still use an old answer.
func (e *DNSEdge) Settle() time.Duration { return 2*e.refresh() + 250*time.Millisecond }

// load reads the state file. Without one, it adopts what a static Edge
// last wrote to Caddy, so switching an install to DNS mode keeps its
// routes; the next Apply rewrites Caddy's configuration in DNS form.
func (e *DNSEdge) load(ctx context.Context) error {
	if e.loaded {
		return nil
	}
	b, err := os.ReadFile(e.StatePath)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &e.state); err != nil {
			return fmt.Errorf("caddy: edge state %s is damaged: %w", e.StatePath, err)
		}
	case errors.Is(err, os.ErrNotExist):
		cfg, gen, err := e.Admin.Config(ctx)
		if err != nil && !strings.Contains(err.Error(), "wasn't written by OAP") {
			return err
		}
		e.state = dnsState{Generation: gen, Members: cfg.Members}
	default:
		return err
	}
	e.loaded = true
	return nil
}

func (e *DNSEdge) save() error {
	b, err := json.MarshalIndent(e.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.StatePath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(e.StatePath), ".edge-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), e.StatePath)
}

// Config returns the members and generation last applied.
func (e *DNSEdge) Config(ctx context.Context) (ops.EdgeConfig, uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.load(ctx); err != nil {
		return ops.EdgeConfig{}, 0, err
	}
	return ops.EdgeConfig{Members: append([]ops.RouteMember(nil), e.state.Members...)}, e.state.Generation, nil
}

// group is one Caddy route: a host, path prefix, prefix handling and port.
type group struct {
	Host, Prefix string
	Strip        bool
	Port         int
}

// Name is the DNS name whose answer lists the group's members.
func (g group) Name() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%t|%d", g.Host, g.Prefix, g.Strip, g.Port)))
	return "g" + hex.EncodeToString(sum[:8]) + ".oap."
}

func groups(cfg ops.EdgeConfig) []group {
	seen := map[group]bool{}
	var out []group
	for _, m := range cfg.Members {
		g := group{m.Host, m.PathPrefix, m.StripPrefix, m.Port}
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if len(a.Prefix) != len(b.Prefix) {
			return len(a.Prefix) > len(b.Prefix)
		}
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Strip != b.Strip {
			return !a.Strip
		}
		return a.Port < b.Port
	})
	return out
}

// server builds Caddy's server for a set of groups. It holds no members.
func (e *DNSEdge) server(gs []group) map[string]any {
	routes := []any{}
	for _, r := range e.Extra {
		routes = append(routes, map[string]any{"match": []any{map[string]any{"host": []any{r.Host}}}, "terminal": true,
			"handle": []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": r.Dial}},
				"transport": map[string]any{"protocol": "http", "versions": []any{"h2c", "2"}}}}})
	}
	for _, g := range gs {
		m := map[string]any{"host": []any{g.Host}}
		if p := strings.TrimSuffix(g.Prefix, "/"); p != "" {
			m["path"] = []any{p, p + "/*"}
		}
		var hs []any
		if g.Strip && strings.TrimSuffix(g.Prefix, "/") != "" {
			hs = append(hs, map[string]any{"handler": "rewrite", "strip_path_prefix": strings.TrimSuffix(g.Prefix, "/")})
		}
		hs = append(hs, map[string]any{
			"handler": "reverse_proxy",
			"dynamic_upstreams": map[string]any{
				"source": "a", "name": strings.TrimSuffix(g.Name(), "."), "port": strconv.Itoa(g.Port),
				"refresh": e.refresh().String(), "resolver": map[string]any{"addresses": []any{e.Resolver}},
			},
			"load_balancing": map[string]any{"selection_policy": map[string]any{"policy": "round_robin"}},
		})
		routes = append(routes, map[string]any{"match": []any{m}, "handle": hs, "terminal": true})
	}
	s := map[string]any{"@id": dnsServerID, "listen": toAny(e.Admin.Listen), "routes": routes}
	if !e.Admin.TLS {
		s["automatic_https"] = map[string]any{"disable": true}
	}
	return s
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Apply makes cfg the membership. Caddy is reloaded only when the routes'
// shape changes; otherwise only the DNS answers change.
func (e *DNSEdge) Apply(ctx context.Context, cfg ops.EdgeConfig) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.load(ctx); err != nil {
		return 0, err
	}
	if same(ops.EdgeConfig{Members: e.state.Members}, cfg) && e.state.Generation > 0 {
		return e.state.Generation, nil
	}
	if err := e.ensure(ctx, cfg); err != nil {
		return 0, err
	}
	e.state = dnsState{Generation: e.state.Generation + 1, Members: append([]ops.RouteMember(nil), cfg.Members...), ChangedAt: e.now()}
	e.cache = nil
	if err := e.save(); err != nil {
		return 0, err
	}
	return e.state.Generation, nil
}

// EnsureShape rewrites Caddy's server if it doesn't match the current
// membership and fixed routes, for example after Extra changed. It changes
// no membership.
func (e *DNSEdge) EnsureShape(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.load(ctx); err != nil {
		return err
	}
	return e.ensure(ctx, ops.EdgeConfig{Members: e.state.Members})
}

// ensure reloads Caddy if its server differs from the one cfg needs.
func (e *DNSEdge) ensure(ctx context.Context, cfg ops.EdgeConfig) error {
	var whole map[string]any
	if err := e.Admin.call(ctx, http.MethodGet, "/config/", nil, &whole); err != nil {
		return err
	}
	if whole == nil {
		whole = map[string]any{}
	}
	servers := child(child(child(whole, "apps"), "http"), "servers")
	want := e.server(groups(cfg))
	// Compare through JSON, as Caddy returns it.
	wantJSON, _ := json.Marshal(want)
	var wantTree any
	json.Unmarshal(wantJSON, &wantTree)
	if reflect.DeepEqual(servers[e.Admin.Server], wantTree) {
		return nil
	}
	servers[e.Admin.Server] = wantTree
	return e.Admin.call(ctx, http.MethodPost, "/load", whole, nil)
}

// Confirmed returns once Caddy can no longer be using answers from before
// the generation's change.
func (e *DNSEdge) Confirmed(ctx context.Context, generation uint64) error {
	e.mu.Lock()
	if err := e.load(ctx); err != nil {
		e.mu.Unlock()
		return err
	}
	cur, changed := e.state.Generation, e.state.ChangedAt
	e.mu.Unlock()
	if generation > cur {
		return fmt.Errorf("caddy: generation %d was never applied (current %d)", generation, cur)
	}
	wait := changed.Add(e.Settle()).Sub(e.now())
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// addresses resolves an instance's host name, cached for a refresh interval.
func (e *DNSEdge) addresses(ctx context.Context, host string) []net.IP {
	e.mu.Lock()
	c, ok := e.cache[host]
	e.mu.Unlock()
	if ok && e.now().Sub(c.at) < e.refresh() {
		return c.ips
	}
	lookup := e.Lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip4", host)
		}
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil
	}
	e.mu.Lock()
	if e.cache == nil {
		e.cache = map[string]cached{}
	}
	e.cache[host] = cached{ips: ips, at: e.now()}
	e.mu.Unlock()
	return ips
}

// Resolve answers a DNS name: the addresses of the members of its group.
func (e *DNSEdge) Resolve(ctx context.Context, name string) ([]net.IP, bool) {
	e.mu.Lock()
	if err := e.load(ctx); err != nil {
		e.mu.Unlock()
		return nil, false
	}
	var hosts []string
	known := false
	for _, m := range e.state.Members {
		if (group{m.Host, m.PathPrefix, m.StripPrefix, m.Port}).Name() == strings.ToLower(name) {
			known = true
			hosts = append(hosts, e.Admin.Host(m.Instance))
		}
	}
	e.mu.Unlock()
	var ips []net.IP
	for _, h := range hosts {
		ips = append(ips, e.addresses(ctx, h)...)
	}
	return ips, known
}

// ServeDNS answers Caddy's queries on conn until ctx ends.
func (e *DNSEdge) ServeDNS(ctx context.Context, conn net.PacketConn) error {
	return serveDNS(ctx, conn, e.Resolve)
}

// InFlight sums Caddy's in-flight request counts for the instance's
// addresses, on any port.
func (e *DNSEdge) InFlight(ctx context.Context, instance ops.InstanceRef) (int, error) {
	var ups []struct {
		Address     string `json:"address"`
		NumRequests int    `json:"num_requests"`
	}
	if err := e.Admin.call(ctx, http.MethodGet, "/reverse_proxy/upstreams", nil, &ups); err != nil {
		return 0, err
	}
	ips := map[string]bool{}
	for _, ip := range e.addresses(ctx, e.Admin.Host(instance)) {
		ips[ip.String()] = true
	}
	n := 0
	for _, u := range ups {
		host, _, err := net.SplitHostPort(u.Address)
		if err == nil && ips[host] && u.NumRequests > 0 {
			n += u.NumRequests
		}
	}
	return n, nil
}
