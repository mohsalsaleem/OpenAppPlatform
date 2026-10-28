package caddy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/contract"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

// TestDNSResponder: Go's own resolver gets A records for known names,
// none for AAAA, and NXDOMAIN for unknown names.
func TestDNSResponder(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go serveDNS(cctx, conn, func(ctx context.Context, name string) ([]net.IP, []srvTarget, bool) {
		if name == "gweb.oap." {
			return []net.IP{net.ParseIP("10.0.0.2"), net.ParseIP("10.0.0.3")},
				[]srvTarget{{"oap-t-web-1", 8080}, {"oap-t-web-2", 8081}}, true
		}
		return nil, nil, false
	})
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", conn.LocalAddr().String())
	}}
	ips, err := r.LookupIP(ctx, "ip4", "gweb.oap")
	if err != nil || len(ips) != 2 || !ips[0].Equal(net.ParseIP("10.0.0.2")) {
		t.Fatalf("A: %v %v", ips, err)
	}
	if ips, err := r.LookupIP(ctx, "ip4", "GWEB.oap"); err != nil || len(ips) != 2 {
		t.Fatalf("names are case-insensitive: %v %v", ips, err)
	}
	if _, err := r.LookupIP(ctx, "ip4", "nope.oap"); err == nil {
		t.Fatal("unknown name resolved")
	}
	if ips, _ := r.LookupIP(ctx, "ip6", "gweb.oap"); len(ips) != 0 {
		t.Fatalf("AAAA answered: %v", ips)
	}
	if _, err := parseQuery([]byte{1, 2, 3}); err == nil {
		t.Fatal("short query accepted")
	}
	// SRV: each member's host name and port, for Caddy's srv upstreams.
	_, srvs, err := r.LookupSRV(ctx, "", "", "gweb.oap")
	if err != nil || len(srvs) != 2 {
		t.Fatalf("SRV: %v %v", srvs, err)
	}
	got := map[string]uint16{}
	for _, s := range srvs {
		got[s.Target] = s.Port
	}
	if got["oap-t-web-1."] != 8080 || got["oap-t-web-2."] != 8081 {
		t.Fatalf("SRV targets %v", got)
	}
	if _, _, err := r.LookupSRV(ctx, "", "", "nope.oap"); err == nil {
		t.Fatal("unknown SRV name resolved")
	}
}

// TestDNSEdgeKeepsServingWithoutOapd: Caddy's routes take their members
// from SRV records, with a grace period, so Caddy keeps routing to the
// last members it learned while oapd (and its DNS) is down; the targets
// are the instances' own names, which Docker's DNS resolves without oapd.
func TestDNSEdgeKeepsServingWithoutOapd(t *testing.T) {
	f := &fakeCaddy{}
	e := dnsEdge(t, f, filepath.Join(t.TempDir(), "edge.json"))
	if _, err := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{web(1, "site.test"), web(2, "site.test")}}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(f.config)
	for _, want := range []string{`"source":"srv"`, `"grace_period"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("Caddy's routes have no %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), `"source":"a"`) {
		t.Fatalf("Caddy's routes still resolve A records: %s", b)
	}
	_, srvs, known := e.Resolve(ctx, group{"site.test", "/", false, 8080}.Name())
	if !known || len(srvs) != 2 || srvs[0].port != 8080 {
		t.Fatalf("SRV answer: %v %v", srvs, known)
	}
}

// fakeCaddy is enough of Caddy's admin API for the DNS edge: whole-config
// GET and load, and upstream counts.
type fakeCaddy struct {
	mu     sync.Mutex
	config map[string]any
	loads  int
	counts map[string]int
}

func (f *fakeCaddy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config/":
		json.NewEncoder(w).Encode(f.config)
	case r.Method == http.MethodPost && r.URL.Path == "/load":
		b, _ := io.ReadAll(r.Body)
		f.config = nil
		json.Unmarshal(b, &f.config)
		f.loads++
	case r.URL.Path == "/reverse_proxy/upstreams":
		var out []map[string]any
		for a, n := range f.counts {
			out = append(out, map[string]any{"address": a, "num_requests": n})
		}
		json.NewEncoder(w).Encode(out)
	default:
		http.NotFound(w, r)
	}
}

var ipOf = map[string]string{"oap-t-web-1": "10.0.0.2", "oap-t-web-2": "10.0.0.3", "oap-t-web-3": "10.0.0.4"}

func dnsEdge(t *testing.T, f *fakeCaddy, state string) *DNSEdge {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	admin := TCP(strings.TrimPrefix(ts.URL, "http://"), func(r ops.InstanceRef) string { return "oap-t-" + r.Component + "-" + string(rune('0'+r.Ordinal)) })
	return &DNSEdge{Admin: admin, Resolver: "udp/oap-t-oapd:53", Refresh: 10 * time.Millisecond, StatePath: state,
		Lookup: func(ctx context.Context, host string) ([]net.IP, error) {
			if ip, ok := ipOf[host]; ok {
				return []net.IP{net.ParseIP(ip)}, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}}
}

func web(n int, host string) ops.RouteMember {
	return ops.RouteMember{Instance: ops.InstanceRef{Application: "t", Component: "web", Ordinal: n, Release: "r1"}, Host: host, PathPrefix: "/", Port: 8080}
}

func TestDNSEdgeContract(t *testing.T) {
	contract.Edge(t, func(t *testing.T) ports.Edge {
		return dnsEdge(t, &fakeCaddy{}, filepath.Join(t.TempDir(), "edge.json"))
	})
}

// TestMembershipChangesDontReload: scaling changes only DNS answers; a new
// host reloads Caddy once.
func TestMembershipChangesDontReload(t *testing.T) {
	f := &fakeCaddy{}
	e := dnsEdge(t, f, filepath.Join(t.TempDir(), "edge.json"))
	g1, err := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{web(1, "site.test")}})
	if err != nil || f.loads != 1 {
		t.Fatalf("first apply: %v, %d loads", err, f.loads)
	}
	name := group{"site.test", "/", false, 8080}.Name()
	if ips, _, ok := e.Resolve(ctx, name); !ok || len(ips) != 1 || ips[0].String() != "10.0.0.2" {
		t.Fatalf("resolve %s: %v %v", name, ips, ok)
	}
	for _, members := range [][]ops.RouteMember{
		{web(1, "site.test"), web(2, "site.test"), web(3, "site.test")},
		{web(1, "site.test"), web(3, "site.test")},
		{web(3, "site.test")},
	} {
		g, err := e.Apply(ctx, ops.EdgeConfig{Members: members})
		if err != nil || g <= g1 {
			t.Fatalf("apply %v: gen %d after %d, %v", members, g, g1, err)
		}
		g1 = g
		ips, _, _ := e.Resolve(ctx, name)
		if len(ips) != len(members) {
			t.Fatalf("resolve after %d members: %v", len(members), ips)
		}
	}
	if f.loads != 1 {
		t.Fatalf("membership changes reloaded Caddy %d times", f.loads-1)
	}
	if _, err := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{web(3, "www.site.test")}}); err != nil || f.loads != 2 {
		t.Fatalf("a new host must reload once: %v, %d loads", err, f.loads)
	}
	if _, _, ok := e.Resolve(ctx, name); ok {
		t.Fatal("the old host's name still resolves")
	}
	// A fixed route (OAP's API) is added once, ahead of the applications.
	e.Extra = []StaticRoute{{Host: "oap.site.test", Dial: "oap-t-oapd:8080"}}
	if err := e.EnsureShape(ctx); err != nil || f.loads != 3 {
		t.Fatalf("ensure shape: %v, %d loads", err, f.loads)
	}
	if err := e.EnsureShape(ctx); err != nil || f.loads != 3 {
		t.Fatalf("ensure shape again: %v, %d loads", err, f.loads)
	}
	first := f.config["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)["oap"].(map[string]any)["routes"].([]any)[0]
	if b, _ := json.Marshal(first); !strings.Contains(string(b), `"oap.site.test"`) || !strings.Contains(string(b), `"oap-t-oapd:8080"`) {
		t.Fatalf("first route: %s", b)
	}
	routes := f.config["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)["oap"].(map[string]any)["routes"].([]any)
	route := routes[len(routes)-1]
	b, _ := json.Marshal(route)
	if !strings.Contains(string(b), `"dynamic_upstreams"`) || !strings.Contains(string(b), `"udp/oap-t-oapd:53"`) || strings.Contains(string(b), "10.0.0") {
		t.Fatalf("route: %s", b)
	}
}

func TestDNSEdgeStateSurvivesRestartAndAdoptsStatic(t *testing.T) {
	dir := t.TempDir()
	f := &fakeCaddy{}
	e := dnsEdge(t, f, filepath.Join(dir, "edge.json"))
	g, _ := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{web(1, "site.test"), web(2, "site.test")}})
	again := dnsEdge(t, f, filepath.Join(dir, "edge.json"))
	cfg, g2, err := again.Config(ctx)
	if err != nil || g2 != g || len(cfg.Members) != 2 {
		t.Fatalf("after restart: %v %d %v", cfg, g2, err)
	}
	// An install switching from the static edge keeps its routes.
	static := &fakeCaddy{}
	se := dnsEdge(t, static, filepath.Join(t.TempDir(), "unused.json")).Admin
	sg, err := se.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{web(1, "site.test")}})
	if err != nil {
		t.Fatal(err)
	}
	adopt := dnsEdge(t, static, filepath.Join(t.TempDir(), "edge.json"))
	adopt.Admin = se
	cfg, ag, err := adopt.Config(ctx)
	if err != nil || ag != sg || len(cfg.Members) != 1 || cfg.Members[0] != web(1, "site.test") {
		t.Fatalf("adopted %v %d %v", cfg, ag, err)
	}
}

func TestDNSEdgeConfirmedAndInFlight(t *testing.T) {
	f := &fakeCaddy{counts: map[string]int{"oap-t-web-1.:8080": 2, "oap-t-web-2.:8080": 0}}
	e := dnsEdge(t, f, filepath.Join(t.TempDir(), "edge.json"))
	g, _ := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{web(1, "site.test")}})
	start := time.Now()
	if err := e.Confirmed(ctx, g); err != nil || time.Since(start) < e.Settle()-5*time.Millisecond {
		t.Fatalf("confirmed after %s, settle %s: %v", time.Since(start), e.Settle(), err)
	}
	if n, err := e.InFlight(ctx, web(1, "").Instance); err != nil || n != 2 {
		t.Fatalf("in flight %d %v", n, err)
	}
	if n, _ := e.InFlight(ctx, web(2, "").Instance); n != 0 {
		t.Fatalf("idle instance in flight %d", n)
	}
}
