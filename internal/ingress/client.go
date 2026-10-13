package ingress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const responseLimit = 1 << 20

var ErrUnavailable = errors.New("ingress admin endpoint unavailable")
var ErrConflict = errors.New("ingress configuration changed; inspect and replan")
var ErrRejected = errors.New("ingress configuration rejected; previous configuration retained")
var ErrUnknown = errors.New("ingress publication outcome uncertain; inspect before retry")

type Snapshot struct {
	Hash string `json:"hash"`
	ETag string `json:"-"`
}
type Receipt struct {
	State         string `json:"state"`
	ExpectedHash  string `json:"expectedHash"`
	CandidateHash string `json:"candidateHash"`
	ObservedHash  string `json:"observedHash,omitempty"`
}
type Client struct{ http *http.Client }

// New uses only a host-authorized private Unix socket. No application-selected
// HTTP admin URL, redirects, Docker socket or unrestricted Caddy API is exposed.
func New(socket string) (*Client, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || strings.ContainsAny(socket, "\x00\r\n") {
		return nil, errors.New("private absolute ingress socket path required")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) Read(ctx context.Context) (Snapshot, error) {
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost/config/", nil)
	response, err := c.http.Do(request)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return Snapshot{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil || len(raw) > responseLimit || Hash(raw) == "" || response.Header.Get("ETag") == "" {
		return Snapshot{}, ErrUnavailable
	}
	return Snapshot{Hash: Hash(raw), ETag: response.Header.Get("ETag")}, nil
}

// Apply performs one conditional whole-config write. Callers must durably record
// expected/candidate hashes BEFORE calling; this client never retries a write.
// An uncertain response can be reconciled by reading the candidate hash later.
func (c *Client) Apply(ctx context.Context, expected Snapshot, candidate Candidate) (Receipt, error) {
	receipt := Receipt{State: "blocked", ExpectedHash: expected.Hash, CandidateHash: candidate.Hash()}
	if expected.Hash == "" || expected.ETag == "" || candidate.Hash() == "" || len(candidate.raw) > responseLimit {
		return receipt, ErrRejected
	}
	current, err := c.Read(ctx)
	if err != nil {
		return receipt, err
	}
	receipt.ObservedHash = current.Hash
	// A lost successful response is observed without another mutation. Authority
	// and operation identity must still be rechecked by the durable controller.
	if current.Hash == candidate.Hash() {
		receipt.State = "unchanged"
		return receipt, nil
	}
	if current.Hash != expected.Hash || current.ETag != expected.ETag {
		return receipt, ErrConflict
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost/config/", bytes.NewReader(candidate.raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", expected.ETag)
	response, err := c.http.Do(request)
	if err != nil {
		receipt.State = "unknown"
		return receipt, ErrUnknown
	}
	defer response.Body.Close()
	// Do not include native error bodies, which may contain private configuration.
	if response.StatusCode == 412 {
		return receipt, ErrConflict
	}
	if response.StatusCode == 400 {
		return receipt, ErrRejected
	}
	if response.StatusCode != 200 {
		receipt.State = "unknown"
		return receipt, ErrUnknown
	}
	observed, err := c.Read(ctx)
	if err != nil || observed.Hash != candidate.Hash() {
		receipt.State = "unknown"
		return receipt, ErrUnknown
	}
	receipt.State = "applied"
	receipt.ObservedHash = observed.Hash
	return receipt, nil
}
