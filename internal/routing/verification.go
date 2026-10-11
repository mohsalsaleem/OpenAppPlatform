package routing

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"time"
)

type Verification struct {
	Status                 string     `json:"status"`
	CheckedAt              time.Time  `json:"checkedAt"`
	DNS                    string     `json:"dns"`
	Addresses              []string   `json:"addresses"`
	TLS                    string     `json:"tls"`
	CertificateExpiresAt   *time.Time `json:"certificateExpiresAt,omitempty"`
	CertificateIssuer      string     `json:"certificateIssuer,omitempty"`
	CertificateFingerprint string     `json:"certificateFingerprint,omitempty"`
	Health                 string     `json:"health"`
	HTTPStatus             int        `json:"httpStatus"`
	LatencyMS              int64      `json:"latencyMs"`
	Message                string     `json:"message,omitempty"`
	AuthorityHash          string     `json:"authorityHash"`
	NativeHash             string     `json:"nativeHash"`
	DirectoryHash          string     `json:"directoryHash"`
}
type resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func expectedAddresses(values []string) (map[string]bool, error) {
	if len(values) > 8 {
		return nil, errors.New("provide at most eight explicit endpoint addresses")
	}
	out := map[string]bool{}
	for _, value := range values {
		addr, e := netip.ParseAddr(value)
		if e != nil || addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() || out[addr.Unmap().String()] {
			return nil, errors.New("endpoint addresses must be unique explicit unicast IP addresses")
		}
		out[addr.Unmap().String()] = true
	}
	return out, nil
}
func Probe(ctx context.Context, b Binding) Verification {
	return probe(ctx, b, net.DefaultResolver, nil)
}
func probe(ctx context.Context, b Binding, lookup resolver, transport *http.Transport) Verification {
	start := time.Now()
	result := Verification{Status: "failed", CheckedAt: start.UTC(), DNS: "skipped", Addresses: []string{}, TLS: "skipped", Health: "skipped"}
	// Never follow browser-supplied hosts, redirects or an implicit private-network
	// target. Every DNS address must match explicit trusted administrator config.
	expected, e := expectedAddresses(b.ExpectedAddresses)
	if e != nil || !validDomain(b.Domain) || !healthPattern.MatchString(b.HealthPath) {
		result.Message = "Endpoint verification authority is invalid."
		return result
	}
	if len(expected) == 0 {
		result.Status = "unconfigured"
		result.DNS = "unconfigured"
		result.Message = "Configure the expected endpoint IP addresses before verification."
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dnsctx, stop := context.WithTimeout(ctx, 3*time.Second)
	addresses, e := lookup.LookupIPAddr(dnsctx, b.Domain)
	stop()
	if e != nil || len(addresses) == 0 || len(addresses) > 16 {
		result.DNS = "failed"
		result.Message = "DNS could not resolve the configured hostname."
		result.LatencyMS = time.Since(start).Milliseconds()
		return result
	}
	known := map[string]bool{}
	for _, ip := range addresses {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || ip.Zone != "" {
			result.DNS = "failed"
			result.Message = "DNS returned an unsupported address."
			return result
		}
		known[addr.Unmap().String()] = true
	}
	for ip := range known {
		result.Addresses = append(result.Addresses, ip)
	}
	sort.Strings(result.Addresses)
	for _, ip := range result.Addresses {
		if !expected[ip] {
			result.DNS = "mismatch"
			result.Message = "DNS points to an address outside the configured endpoint authority."
			result.LatencyMS = time.Since(start).Milliseconds()
			return result
		}
	}
	result.DNS = "matched"
	if transport == nil {
		transport = &http.Transport{TLSClientConfig: &tls.Config{ServerName: b.Domain, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second, DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != net.JoinHostPort(b.Domain, "443") {
					return nil, errors.New("unexpected endpoint destination")
				}
				var last error
				for _, ip := range result.Addresses {
					conn, e := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
					if e == nil {
						return conn, nil
					}
					last = e
				}
				return nil, last
			}}
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+b.Domain+b.HealthPath, nil)
	if e != nil {
		result.Message = "Invalid endpoint readiness request."
		return result
	}
	request.Header.Set("User-Agent", "OpenAppPlatform-endpoint-verification/1")
	response, e := client.Do(request)
	result.LatencyMS = time.Since(start).Milliseconds()
	if e != nil {
		result.TLS = "failed"
		result.Message = "TLS validation or the HTTPS connection failed."
		return result
	}
	defer response.Body.Close()
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || len(response.TLS.PeerCertificates) == 0 {
		result.TLS = "failed"
		result.Message = "The endpoint did not present a verified TLS certificate."
		return result
	}
	leaf := response.TLS.PeerCertificates[0]
	expires := leaf.NotAfter.UTC()
	result.TLS = "valid"
	result.CertificateExpiresAt = &expires
	result.CertificateIssuer = leaf.Issuer.CommonName
	result.CertificateFingerprint = Hash(leaf.Raw)
	result.HTTPStatus = response.StatusCode
	result.Health = "failed"
	// Discard a bounded body. Store no response content, headers, cookies or secrets.
	_, e = io.Copy(io.Discard, io.LimitReader(response.Body, 4097))
	if e != nil {
		result.Message = "The readiness response could not be read."
		return result
	}
	if response.StatusCode != http.StatusOK {
		result.Message = "The configured readiness path did not return HTTP 200. Redirects are not followed."
		return result
	}
	result.Health = "ready"
	result.Status = "verified"
	if time.Until(expires) < 7*24*time.Hour {
		result.Message = "The TLS certificate expires within seven days."
	}
	return result
}

// Verification evidence expires independently of the certificate and is invalid
// after configuration, membership, native file or directory authority changes.
func CurrentVerification(v Verification, authority, native, directory string, now time.Time) Verification {
	if v.AuthorityHash != authority || v.NativeHash != native || v.DirectoryHash != directory || v.CheckedAt.Before(now.Add(-10*time.Minute)) || v.CheckedAt.After(now.Add(time.Minute)) || (v.CertificateExpiresAt != nil && !v.CertificateExpiresAt.After(now)) {
		v.Status = "stale"
		v.Message = "Endpoint configuration changed or this check expired. Verify again."
	}
	return v
}
