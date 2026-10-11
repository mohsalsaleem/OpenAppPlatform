package routing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fixedResolver struct {
	IPs []net.IPAddr
	Err error
}

func (r fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.IPs, r.Err
}
func tlsFixture(t *testing.T, hostname string, expired bool, handler http.Handler) (*http.Transport, *httptest.Server) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "OAP test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, IsCA: true, BasicConstraintsValid: true}
	caRaw, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, _ := x509.ParseCertificate(caRaw)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{hostname}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if expired {
		leaf.NotBefore = now.Add(-3 * time.Hour)
		leaf.NotAfter = now.Add(-time.Hour)
	}
	raw, e := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{raw, caRaw}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "api.example.test", MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	return transport, server
}
func TestEndpointProbeValidatesTLSAndNeverFollowsRedirects(t *testing.T) {
	b := fixture()
	b.Domain = "api.example.test"
	b.ExpectedAddresses = []string{"127.0.0.1"}
	lookup := fixedResolver{IPs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	for _, test := range []struct {
		name, hostname     string
		expired, untrusted bool
		code               int
		want               string
	}{{"ready", b.Domain, false, false, 200, "verified"}, {"wrong-name", "foreign.example.test", false, false, 200, "failed"}, {"expired", b.Domain, true, false, 200, "failed"}, {"untrusted", b.Domain, false, true, 200, "failed"}, {"not-ready", b.Domain, false, false, 503, "failed"}, {"redirect", b.Domain, false, false, 302, "failed"}} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			transport, _ := tlsFixture(t, test.hostname, test.expired, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != b.HealthPath || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("unbounded readiness request")
				}
				w.Header().Set("Location", "https://foreign.example.test/private")
				w.Header().Set("Set-Cookie", "private=do-not-store")
				w.WriteHeader(test.code)
				io.WriteString(w, "private body is discarded")
			}))
			if test.untrusted {
				transport.TLSClientConfig.RootCAs = x509.NewCertPool()
			}
			result := probe(context.Background(), b, lookup, transport)
			if result.Status != test.want || result.DNS != "matched" {
				t.Fatal(result)
			}
			if test.name == "ready" && (result.TLS != "valid" || result.Health != "ready" || result.CertificateExpiresAt == nil || result.CertificateFingerprint == "") {
				t.Fatal(result)
			}
			if test.name == "redirect" && (calls != 1 || result.HTTPStatus != 302 || result.Health != "failed") {
				t.Fatal("redirect followed", calls, result)
			}
		})
	}
}
func TestEndpointProbeRequiresExplicitMatchingDNSAuthority(t *testing.T) {
	b := fixture()
	calls := 0
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, errors.New("must not dial")
	}}
	result := probe(context.Background(), b, fixedResolver{}, transport)
	if result.Status != "unconfigured" {
		t.Fatal(result)
	}
	b.ExpectedAddresses = []string{"192.0.2.1"}
	for _, lookup := range []fixedResolver{{Err: errors.New("no DNS")}, {IPs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}, {IPs: []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("169.254.169.254")}}}} {
		result = probe(context.Background(), b, lookup, transport)
		if result.Status != "failed" {
			t.Fatal(result)
		}
	}
	if calls != 0 {
		t.Fatal("unexpected DNS authority reached network")
	}
	for _, ips := range [][]string{{"0.0.0.0"}, {"ff02::1"}, {"bad"}, {"192.0.2.1", "192.0.2.1"}} {
		if _, e := expectedAddresses(ips); e == nil {
			t.Fatal("invalid authority accepted", ips)
		}
	}
}
func TestEndpointVerificationExpiresAndTracksRoutingAuthority(t *testing.T) {
	now := time.Now()
	expires := now.Add(time.Hour)
	v := Verification{Status: "verified", CheckedAt: now, AuthorityHash: "a", NativeHash: "b", DirectoryHash: "c", CertificateExpiresAt: &expires}
	if CurrentVerification(v, "a", "b", "c", now).Status != "verified" {
		t.Fatal("fresh evidence rejected")
	}
	for _, values := range [][3]string{{"changed", "b", "c"}, {"a", "changed", "c"}, {"a", "b", "changed"}} {
		if CurrentVerification(v, values[0], values[1], values[2], now).Status != "stale" {
			t.Fatal("stale authority accepted")
		}
	}
	if CurrentVerification(v, "a", "b", "c", now.Add(11*time.Minute)).Status != "stale" {
		t.Fatal("expired observation accepted")
	}
}
