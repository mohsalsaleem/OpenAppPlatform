package system_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ingress"
)

// This proves the proxy foundation on the disposable Engine. It intentionally
// does not claim controller plan/apply integration or durable retirement/draining.
func TestCaddyStandaloneRoutingPublicationAndResume(t *testing.T) {
	socket, image, caddy := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE"), os.Getenv("OAP_TEST_CADDY_IMAGE")
	if socket == "" || image == "" || caddy == "" {
		t.Skip("isolated Docker and pinned Caddy fixtures required")
	}
	if socket != "/run/oap-test/docker.sock" {
		t.Fatal("Caddy acceptance requires disposable test Engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	engine := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer engine.CloseIdleConnections()
	api := func(method, path string, body any) []byte {
		t.Helper()
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		r, _ := http.NewRequestWithContext(ctx, method, "http://docker/v1.45"+path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		res, err := engine.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("fixture Engine %s %s: %d", method, path, res.StatusCode)
		}
		return raw
	}
	id := "oap-caddy-" + domain.NewID()[:8]
	network := id + "-app"
	proxy := ""
	created := []string{}
	volumes := []string{id + "-config", id + "-data"}
	api("POST", "/networks/create", map[string]any{"Name": network, "Internal": false})
	t.Cleanup(func() {
		// Exact fixture names only; no prune or application-owned data deletion.
		for _, name := range append(created, proxy) {
			if name != "" {
				r, _ := http.NewRequest("DELETE", "http://docker/v1.45/containers/"+name+"?force=true", nil)
				res, err := engine.Do(r)
				if err == nil {
					res.Body.Close()
				}
			}
		}
		r, _ := http.NewRequest("DELETE", "http://docker/v1.45/networks/"+network, nil)
		res, err := engine.Do(r)
		if err == nil {
			res.Body.Close()
		}
		for _, v := range volumes {
			r, _ := http.NewRequest("DELETE", "http://docker/v1.45/volumes/"+v, nil)
			res, err := engine.Do(r)
			if err == nil {
				res.Body.Close()
			}
		}
		os.Remove("/run/oap-test/" + id + ".sock")
	})
	create := func(name string, body map[string]any) string {
		t.Helper()
		var result struct {
			ID string `json:"Id"`
		}
		json.Unmarshal(api("POST", "/containers/create?name="+name, body), &result)
		if result.ID == "" {
			t.Fatal("missing fixture ID")
		}
		api("POST", "/containers/"+result.ID+"/start", nil)
		return result.ID
	}
	m := domain.Manifest{TargetID: "fixture", Components: []domain.Component{{Name: "web", Kind: "web", Port: 8080, Instances: 1}, {Name: "api", Kind: "web", Port: 8080, Instances: 3}}}
	d := ingress.Definition{Domain: "app.example.test", Routes: []ingress.Route{{Path: "/", Component: "web"}, {Path: "/api", Component: "api", StripPrefix: true}}}
	members := []ingress.Member{}
	for _, component := range m.Components {
		for ordinal := 1; ordinal <= component.Instances; ordinal++ {
			name := fmt.Sprintf("%s-%s-%d", id, component.Name, ordinal)
			container := create(name, map[string]any{"Image": image, "Env": []string{"OAP_TEST_MESSAGE=" + fmt.Sprintf("%s-%d", component.Name, ordinal)}, "HostConfig": map[string]any{"NetworkMode": network}})
			created = append(created, container)
			var state struct {
				NetworkSettings struct {
					Networks map[string]struct {
						IPAddress string `json:"IPAddress"`
					} `json:"Networks"`
				} `json:"NetworkSettings"`
			}
			json.Unmarshal(api("GET", "/containers/"+container+"/json", nil), &state)
			address := state.NetworkSettings.Networks[network].IPAddress
			if address == "" {
				t.Fatal("missing fixture address")
			}
			members = append(members, ingress.Member{ApplicationID: id, TargetID: m.TargetID, Component: component.Name, Ordinal: ordinal, Address: net.JoinHostPort(address, "8080"), Ready: true, ObservedAt: time.Now()})
		}
	}
	adminDirectory := "/run/oap-test/" + id + "-admin"
	if err := os.Mkdir(adminDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(adminDirectory) })
	options := ingress.Options{AdminSocket: "/run/oap-ingress/admin.sock", HTTPPort: 8080, HTTPSPort: 8443}
	bootstrap, _ := json.Marshal(map[string]any{"admin": map[string]any{"listen": "unix/" + options.AdminSocket + "|0600"}})
	ports := map[string]any{}
	for _, p := range []string{"8080/tcp", "8443/tcp"} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		ports[p] = []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": fmt.Sprint(port)}}
	}
	proxy = create(id, map[string]any{"Image": caddy, "Entrypoint": []string{"/bin/sh", "-c"}, "Cmd": []string{`printf '%s' "$1" > /config/bootstrap.json; exec caddy run --config /config/bootstrap.json --resume`, "fixture", string(bootstrap)}, "Env": []string{"XDG_CONFIG_HOME=/config", "XDG_DATA_HOME=/data"}, "ExposedPorts": map[string]any{"8080/tcp": map[string]any{}, "8443/tcp": map[string]any{}}, "HostConfig": map[string]any{"NetworkMode": network, "Binds": []string{adminDirectory + ":/run/oap-ingress", volumes[0] + ":/config", volumes[1] + ":/data"}, "PortBindings": ports, "SecurityOpt": []string{"no-new-privileges:true"}}})
	client, err := ingress.New(adminDirectory + "/admin.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var snapshot ingress.Snapshot
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		snapshot, err = client.Read(ctx)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("Caddy admin readiness", err)
	}
	candidate, err := ingress.Compile(id, m, d, members, options, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Apply(ctx, snapshot, candidate)
	if err != nil || receipt.State != "applied" {
		t.Fatal(receipt, err)
	}
	replay, err := client.Apply(ctx, snapshot, candidate)
	if err != nil || replay.State != "unchanged" {
		t.Fatal(replay, err)
	}
	var inspection struct {
		NetworkSettings struct {
			Ports map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	json.Unmarshal(api("GET", "/containers/"+proxy+"/json", nil), &inspection)
	if len(inspection.NetworkSettings.Ports["8080/tcp"]) != 1 || len(inspection.NetworkSettings.Ports["8443/tcp"]) != 1 {
		t.Fatal("fixture proxy ports not published")
	}
	httpPort := inspection.NetworkSettings.Ports["8080/tcp"][0].HostPort
	tlsPort := inspection.NetworkSettings.Ports["8443/tcp"][0].HostPort
	public := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(client *http.Client, scheme, port, path, host string) (int, []byte, error) {
		r, _ := http.NewRequestWithContext(ctx, "GET", scheme+"://127.0.0.1:"+port+path, nil)
		r.Host = host
		res, err := client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(io.LimitReader(res.Body, 4096))
		return res.StatusCode, b, err
	}
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		status, body, err := request(public, "http", httpPort, "/api", d.Domain)
		if err != nil || status != 200 {
			t.Fatalf("public API %d %v", status, err)
		}
		var value map[string]string
		json.Unmarshal(body, &value)
		seen[value["message"]] = true
	}
	if len(seen) != 3 || !seen["api-1"] || !seen["api-2"] || !seen["api-3"] {
		t.Fatal("replicas not all served", seen)
	}
	for _, path := range []string{"/", "/apiculture"} {
		status, body, err := request(public, "http", httpPort, path, d.Domain)
		if err != nil || status != 200 || !strings.Contains(string(body), "web-1") {
			t.Fatal("path precedence", path, status, err)
		}
	}
	status, body, err := request(public, "http", httpPort, "/api/health/custom", d.Domain)
	if err != nil || status != 200 || string(body) != "ready" {
		t.Fatal("prefix stripping", status, err)
	}
	status, _, err = request(public, "http", httpPort, "/", "other.example.test")
	if err != nil || status != 404 {
		t.Fatal("unknown host", status, err)
	}
	// Proxy reload alone removes two replicas. This is not controller scale-down.
	snapshot, _ = client.Read(ctx)
	m.Components[1].Instances = 1
	members = members[:2]
	for i := range members {
		members[i].ObservedAt = time.Now()
	}
	down, err := ingress.Compile(id, m, d, members, options, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Apply(ctx, snapshot, down); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		status, body, err = request(public, "http", httpPort, "/api", d.Domain)
		if err != nil || status != 200 || !strings.Contains(string(body), "api-1") {
			t.Fatal("route removal", status, err)
		}
	}
	// Restore clients and certificate/config storage across a real proxy restart.
	api("POST", "/containers/"+proxy+"/restart?t=3", nil)
	client.Close()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		status, body, err = request(public, "http", httpPort, "/api", d.Domain)
		if err == nil && status == 200 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || status != 200 || !strings.Contains(string(body), "api-1") {
		t.Fatal("resume lost route", status, err)
	}
	snapshot, err = client.Read(ctx)
	if err != nil || snapshot.Hash != down.Hash() {
		t.Fatal("resume hash mismatch", err)
	}
	// Offline internal CA verifies real HTTPS without ACME/network or insecure TLS.
	d.HTTPS = true
	options.InternalCA = true
	for i := range members {
		members[i].ObservedAt = time.Now()
	}
	secure, err := ingress.Compile(id, m, d, members, options, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Apply(ctx, snapshot, secure); err != nil {
		t.Fatal(err)
	}
	var cert []byte
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		r, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/v1.45/containers/"+proxy+"/archive?path=/data/caddy/pki/authorities/local/root.crt", nil)
		res, err := engine.Do(r)
		if err == nil {
			if res.StatusCode == 200 {
				reader := tar.NewReader(res.Body)
				if _, err := reader.Next(); err == nil {
					cert, _ = io.ReadAll(io.LimitReader(reader, 16384))
				}
			}
			res.Body.Close()
		}
		if len(cert) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("missing fixture CA certificate")
	}
	tlsClient := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: d.Domain, MinVersion: tls.VersionTLS12}}}
	defer tlsClient.CloseIdleConnections()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		status, body, err = request(tlsClient, "https", tlsPort, "/api", d.Domain)
		if err == nil && status == 200 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || status != 200 || !strings.Contains(string(body), "api-1") {
		t.Fatal("verified HTTPS failed", status, err)
	}
	api("POST", "/containers/"+proxy+"/restart?t=3", nil)
	client.Close()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		status, body, err = request(tlsClient, "https", tlsPort, "/api", d.Domain)
		if err == nil && status == 200 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || status != 200 {
		t.Fatal("HTTPS resume failed", status, err)
	}

	// A single proxy can host independently configured applications. Updating one
	// does not require forcing the other into the same HTTPS mode.
	otherID := id + "-other"
	otherContainer := create(otherID, map[string]any{"Image": image, "Env": []string{"OAP_TEST_MESSAGE=other-app"}, "HostConfig": map[string]any{"NetworkMode": network}})
	created = append(created, otherContainer)
	var otherState struct {
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	json.Unmarshal(api("GET", "/containers/"+otherContainer+"/json", nil), &otherState)
	otherIP := otherState.NetworkSettings.Networks[network].IPAddress
	if otherIP == "" {
		t.Fatal("other app address unavailable")
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		res, e := public.Get("http://" + net.JoinHostPort(otherIP, "8080") + "/health/custom")
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	otherManifest := domain.Manifest{TargetID: m.TargetID, Components: []domain.Component{{Name: "other-web", Kind: "web", Port: 8080, Instances: 1}}, Ingress: &domain.IngressDefinition{Domain: "other.example.test", HTTPS: true, Routes: []domain.IngressRoute{{Path: "/", Component: "other-web"}}}}
	otherMembers := []ingress.Member{{ApplicationID: otherID, TargetID: m.TargetID, Component: "other-web", Ordinal: 1, Address: net.JoinHostPort(otherIP, "8080"), Ready: true, ObservedAt: time.Now()}}
	d.HTTPS = false
	m.Ingress = &d
	for i := range members {
		members[i].ObservedAt = time.Now()
	}
	shared, e := ingress.CompileServer([]ingress.ApplicationRoutes{{ApplicationID: id, Manifest: m, Members: members}, {ApplicationID: otherID, Manifest: otherManifest, Members: otherMembers}}, options, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e = client.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Apply(ctx, snapshot, shared); e != nil {
		t.Fatal(e)
	}
	status, body, err = request(public, "http", httpPort, "/api", d.Domain)
	if err != nil || status != 200 || !strings.Contains(string(body), "api-1") {
		t.Fatal("HTTP app lost after adding HTTPS sibling", status, err)
	}
	otherTLS := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: otherManifest.Ingress.Domain, MinVersion: tls.VersionTLS12}}}
	defer otherTLS.CloseIdleConnections()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		status, body, err = request(otherTLS, "https", tlsPort, "/", otherManifest.Ingress.Domain)
		if err == nil && status == 200 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || status != 200 || !strings.Contains(string(body), "other-app") {
		t.Fatal("shared HTTPS app unavailable", status, err)
	}
	api("POST", "/containers/"+proxy+"/restart?t=3", nil)
	client.Close()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		status, body, err = request(public, "http", httpPort, "/api", d.Domain)
		if err == nil && status == 200 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || status != 200 {
		t.Fatal("shared HTTP resume", status, err)
	}
	status, body, err = request(otherTLS, "https", tlsPort, "/", otherManifest.Ingress.Domain)
	if err != nil || status != 200 || !strings.Contains(string(body), "other-app") {
		t.Fatal("shared HTTPS resume", status, err)
	}

	if dir := os.Getenv("OAP_TEST_REPORT_DIR"); dir != "" {
		receipt := map[string]any{"status": "passed", "scope": "Caddy foundation; no controller apply/scale-down acceptance", "httpReplicaCount": 3, "pathPrecedence": true, "prefixStripping": true, "unknownHostRejected": true, "conditionalPublication": true, "replayWithoutMutation": true, "replicaMembershipRemoval": true, "restartResume": true, "verifiedInternalCAHTTPS": true, "publicACMEIssued": false, "sharedApplicationCount": 2, "independentHTTPSModes": true}
		raw, _ := json.MarshalIndent(receipt, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "ingress-foundation.json"), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
