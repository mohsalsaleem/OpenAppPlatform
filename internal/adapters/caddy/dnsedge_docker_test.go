package caddy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// TestDNSEdgeScalesWithoutLoss runs real Caddy and real instances: under
// continuous traffic it scales 1 -> 3 -> 1 in the planner's order (add,
// confirm; remove, confirm, drain, stop) and requires that no request
// fails and that membership changes never reload Caddy. Gated on
// OAP_TEST_DOCKER=1. The DNS responder runs in the test process and Caddy
// reaches it at host.docker.internal.
func TestDNSEdgeScalesWithoutLoss(t *testing.T) {
	rt, err := docker.New(gate(t), install())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.RemoveAll(context.Background()) })
	refs := make([]ops.InstanceRef, 4)
	for i := 1; i <= 3; i++ {
		refs[i] = ops.InstanceRef{Application: "edge", Component: "web", Ordinal: i, Release: "r-1"}
	}
	if _, err := rt.Start(ctx, ops.InstanceSpec{Ref: refs[1], Image: "traefik/whoami:v1.10.3", Port: 80}); err != nil {
		t.Fatal(err)
	}
	adminAddr, webAddr := caddy(t, rt.Install, docker.NetworkName(rt.Install))

	conn, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	host := func(r ops.InstanceRef) string { return docker.ContainerName(rt.Install, r) }
	e := &DNSEdge{Admin: TCP(adminAddr, host), Resolver: "udp/host.docker.internal:" + strconv.Itoa(port),
		Refresh: time.Second, StatePath: filepath.Join(t.TempDir(), "edge.json"),
		Lookup: func(ctx context.Context, h string) ([]net.IP, error) {
			ip := dockerCLI(t, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", h)
			return []net.IP{net.ParseIP(ip)}, nil
		}}
	dctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.ServeDNS(dctx, conn)

	member := func(r ops.InstanceRef) ops.RouteMember {
		return ops.RouteMember{Instance: r, Host: "edge.test", PathPrefix: "/", Port: 80}
	}
	members := func(ords ...int) ops.EdgeConfig {
		var c ops.EdgeConfig
		for _, o := range ords {
			c.Members = append(c.Members, member(refs[o]))
		}
		return c
	}
	apply := func(c ops.EdgeConfig) {
		t.Helper()
		g, err := e.Apply(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Confirmed(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	apply(members(1))
	serverBefore := fmt.Sprint(readServer(t, e))

	var sent, failed atomic.Int64
	var mu sync.Mutex
	var reasons []string
	stop := make(chan struct{})
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				req, _ := http.NewRequest(http.MethodGet, "http://"+webAddr+"/?wait=150ms", nil)
				req.Host = "edge.test"
				sent.Add(1)
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode != 200 {
						err = fmt.Errorf("status %d", resp.StatusCode)
					}
				}
				if err != nil {
					failed.Add(1)
					mu.Lock()
					reasons = append(reasons, err.Error())
					mu.Unlock()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
	}
	time.Sleep(2 * time.Second)
	for i := 2; i <= 3; i++ { // scale up: start, then route
		if _, err := rt.Start(ctx, ops.InstanceSpec{Ref: refs[i], Image: "traefik/whoami:v1.10.3", Port: 80}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Second)
	apply(members(1, 2))
	apply(members(1, 2, 3))
	time.Sleep(2 * time.Second)
	for _, gone := range []int{3, 2} { // scale down: route-remove, confirm, drain, stop
		var keep []int
		for o := 1; o < gone; o++ {
			keep = append(keep, o)
		}
		apply(members(keep...))
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			n, err := e.InFlight(ctx, refs[gone])
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("instance %d didn't drain", gone)
			}
		}
		if err := rt.Stop(ctx, refs[gone]); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()
	t.Logf("scaled 1 -> 3 -> 1: %d requests, %d failed %v", sent.Load(), failed.Load(), reasons)
	if failed.Load() != 0 || sent.Load() < 100 {
		t.Fatalf("%d of %d requests failed: %v", failed.Load(), sent.Load(), reasons)
	}
	if after := fmt.Sprint(readServer(t, e)); after != serverBefore {
		t.Fatalf("membership changes rewrote Caddy's server:\n%s\n%s", serverBefore, after)
	}
}

func readServer(t *testing.T, e *DNSEdge) any {
	t.Helper()
	var whole map[string]any
	if err := e.Admin.call(ctx, http.MethodGet, "/config/apps/http/servers/oap", nil, &whole); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(whole), "dynamic_upstreams") {
		t.Fatalf("server isn't in DNS form: %v", whole)
	}
	return whole
}
