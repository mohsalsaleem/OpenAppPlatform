// Package docker implements direct Docker Engine targets without an SDK dependency.
package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

const prefix = "io.openappplatform."
const apiVersion = "v1.45"

type Settings struct {
	PullPolicy string `json:"pullPolicy"`
	HostBindIP string `json:"hostBindIP"`
}
type Client struct {
	target   domain.Target
	settings Settings
	http     *http.Client
	base     string
}
type APIError struct {
	Status       int
	Method, Path string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Docker %s %s returned HTTP %d", e.Method, e.Path, e.Status)
}
func New(t domain.Target) (*Client, error) {
	u, e := url.Parse(t.URL)
	if e != nil || u.Scheme != "unix" || u.Host != "" || !filepath.IsAbs(u.Path) || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("Docker targets currently require an absolute unix:// socket URL")
	}
	settings := Settings{PullPolicy: "never", HostBindIP: "127.0.0.1"}
	if len(t.Settings) > 0 {
		d := json.NewDecoder(bytes.NewReader(t.Settings))
		d.DisallowUnknownFields()
		if e = d.Decode(&settings); e != nil {
			return nil, errors.New("invalid Docker target settings")
		}
	}
	if settings.PullPolicy != "never" && settings.PullPolicy != "if-missing" {
		return nil, errors.New("Docker pullPolicy must be never or if-missing")
	}
	if ip := net.ParseIP(settings.HostBindIP); ip == nil {
		return nil, errors.New("Docker hostBindIP must be an IP address")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", u.Path)
	}}
	return &Client{target: t, settings: settings, base: "http://docker", http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Capabilities() operator.Capabilities {
	return operator.Capabilities{Standard: true, Discovery: true, ImmutableImages: true}
}
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.base+"/"+apiVersion+path, body)
	if e != nil {
		return errors.New("invalid Docker request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := c.http.Do(req)
	if e != nil {
		return errors.New("Docker Engine is unavailable or timed out")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &APIError{Status: res.StatusCode, Method: method, Path: path}
	}
	if output == nil {
		return nil
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil {
		return errors.New("cannot read Docker response")
	}
	if len(data) > 2<<20 {
		return errors.New("Docker response exceeds limit")
	}
	if e = json.Unmarshal(data, output); e != nil {
		return errors.New("invalid Docker response")
	}
	return nil
}
func isStatus(err error, status int) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == status
}

type container struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Image  string `json:"Image"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

func (c *Client) inspectContainer(ctx context.Context, name string) (container, error) {
	var v container
	e := c.request(ctx, "GET", "/containers/"+url.PathEscape(name)+"/json", nil, &v)
	return v, e
}
func (c *Client) owned(v container) bool {
	return v.Config.Labels[prefix+"target"] == c.target.ID && v.Config.Labels[prefix+"environment"] == c.target.Environment && v.Config.Labels[prefix+"owner"] != ""
}
func (c *Client) projection(v container) operator.Resource {
	status := v.State.Status
	if v.State.Health != nil {
		status += ":" + v.State.Health.Status
	}
	port, _ := strconv.Atoi(v.Config.Labels[prefix+"port"])
	resource := operator.Resource{Port: port, ID: v.Config.Labels[prefix+"reference"], Name: strings.TrimPrefix(v.Name, "/"), Description: v.Config.Labels[prefix+"owner"], Status: status, Image: v.Config.Labels[prefix+"artifact"], ArtifactKind: "image", Environment: c.target.Environment}
	for _, ports := range v.NetworkSettings.Ports {
		if len(ports) > 0 {
			resource.URL = "http://" + net.JoinHostPort(ports[0].HostIP, ports[0].HostPort)
			break
		}
	}
	return resource
}
func (c *Client) Discover(ctx context.Context) ([]operator.Resource, error) {
	filter, _ := json.Marshal(map[string][]string{"label": {prefix + "target=" + c.target.ID, prefix + "environment=" + c.target.Environment}})
	var summaries []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
	}
	if e := c.request(ctx, "GET", "/containers/json?all=true&filters="+url.QueryEscape(string(filter)), nil, &summaries); e != nil {
		return nil, e
	}
	out := []operator.Resource{}
	for _, s := range summaries {
		ref := s.Labels[prefix+"reference"]
		if ref == "" {
			continue
		}
		active := false
		for _, n := range s.Names {
			if strings.TrimPrefix(n, "/") == ref {
				active = true
			}
		}
		if !active {
			continue
		}
		v, e := c.inspectContainer(ctx, s.ID)
		if e != nil {
			return nil, e
		}
		if c.owned(v) {
			out = append(out, c.projection(v))
		}
	}
	return out, nil
}
func (c *Client) Inspect(ctx context.Context, ref string) (operator.Resource, error) {
	v, e := c.inspectContainer(ctx, ref)
	if e != nil {
		return operator.Resource{}, e
	}
	if !c.owned(v) || v.Config.Labels[prefix+"reference"] != ref || strings.TrimPrefix(v.Name, "/") != ref {
		return operator.Resource{}, errors.New("container is outside this target or is not an active managed instance")
	}
	return c.projection(v), nil
}
func (c *Client) image(ctx context.Context, ref string) (string, error) {
	var result struct {
		ID string `json:"Id"`
	}
	e := c.request(ctx, "GET", "/images/"+url.PathEscape(ref)+"/json", nil, &result)
	if isStatus(e, 404) && c.settings.PullPolicy == "if-missing" {
		if e = c.pull(ctx, ref); e != nil {
			return "", e
		}
		e = c.request(ctx, "GET", "/images/"+url.PathEscape(ref)+"/json", nil, &result)
	}
	if isStatus(e, 404) {
		return "", errors.New("image is not cached; pre-pull it or configure pullPolicy if-missing")
	}
	return result.ID, e
}
func (c *Client) pull(ctx context.Context, ref string) error {
	// Pull responses stream progress and errors; HTTP 200 alone is not success.
	req, e := http.NewRequestWithContext(ctx, "POST", c.base+"/"+apiVersion+"/images/create?fromImage="+url.QueryEscape(ref), nil)
	if e != nil {
		return e
	}
	res, e := c.http.Do(req)
	if e != nil {
		return errors.New("Docker image pull failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("Docker image pull was rejected")
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 2<<20))
	for {
		var line struct {
			Error string `json:"error"`
		}
		e = decoder.Decode(&line)
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return errors.New("invalid Docker image pull response")
		}
		if line.Error != "" {
			return errors.New("Docker image pull failed; inspect registry access")
		}
	}
}
func (c *Client) removeStopped(ctx context.Context, v container) error {
	if !c.owned(v) || v.State.Running {
		return errors.New("refusing to remove an unowned or running container")
	}
	return c.request(ctx, "DELETE", "/containers/"+url.PathEscape(v.ID)+"?v=false&force=false", nil, nil)
}
func (c *Client) Logs(ctx context.Context, ref string, lines int) (string, error) {
	if _, e := c.Inspect(ctx, ref); e != nil {
		return "", e
	}
	if lines < 1 || lines > 200 {
		lines = 100
	}
	req, e := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/%s/containers/%s/logs?stdout=true&stderr=true&tail=%d", c.base, apiVersion, url.PathEscape(ref), lines), nil)
	if e != nil {
		return "", e
	}
	res, e := c.http.Do(req)
	if e != nil {
		return "", errors.New("Docker logs unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("Docker logs request rejected")
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, (256<<10)+1))
	if e != nil {
		return "", e
	}
	if len(b) > 256<<10 {
		return "", errors.New("Docker log response exceeds limit")
	}
	return demultiplex(b)
}
func demultiplex(b []byte) (string, error) {
	var out bytes.Buffer
	for len(b) > 0 {
		if len(b) < 8 || (b[0] != 1 && b[0] != 2) || b[1] != 0 || b[2] != 0 || b[3] != 0 {
			return "", errors.New("invalid Docker multiplexed logs")
		}
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if n > len(b)-8 {
			return "", errors.New("truncated Docker log frame")
		}
		out.Write(b[8 : 8+n])
		b = b[8+n:]
	}
	return out.String(), nil
}
