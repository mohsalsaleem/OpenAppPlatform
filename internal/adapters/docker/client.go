package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// APIVersion is the Docker Engine API version the adapter speaks. Docker 25
// and later support it; Docker 29 supports nothing older.
const APIVersion = "v1.44"

// APIError is a non-success response from the Docker Engine API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker: %d: %s", e.Status, e.Message) }

func isStatus(err error, status int) bool {
	ae, ok := err.(*APIError)
	return ok && ae.Status == status
}

type client struct {
	http *http.Client
	base string
}

// newClient talks to the Engine API on a unix socket.
func newClient(socket string) *client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &client{http: &http.Client{Transport: tr}, base: "http://docker/" + APIVersion}
}

// request sends a request and returns the response for the caller to read
// and close. Non-2xx and 304 responses become APIErrors.
func (c *client) request(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var m struct{ Message string }
		if json.Unmarshal(b, &m) != nil || m.Message == "" {
			m.Message = strings.TrimSpace(string(b))
		}
		return nil, &APIError{Status: resp.StatusCode, Message: m.Message}
	}
	return resp, nil
}

// do sends a request and decodes a JSON response into out, if out is set.
func (c *client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.request(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// labelFilter encodes a filters query parameter matching every label.
func labelFilter(labels ...string) url.Values {
	f, _ := json.Marshal(map[string][]string{"label": labels})
	return url.Values{"filters": {string(f)}}
}
