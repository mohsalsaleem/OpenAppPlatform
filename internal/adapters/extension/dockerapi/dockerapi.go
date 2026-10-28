// Package dockerapi is the small Docker Engine API client the first-party
// builder extensions use: build from a context, pull, and inspect.
package dockerapi

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Client talks to the Docker Engine on a unix socket.
type Client struct {
	h *http.Client
}

// New returns a client for the socket.
func New(socket string) *Client {
	return &Client{h: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}}}
}

// BuildOptions is a build.
type BuildOptions struct {
	Tag, Dockerfile string
	// MemoryMiB is a hard limit for every build step: memory and memory
	// plus swap are both set to it.
	MemoryMiB int
	Labels    map[string]string
}

// Build sends a context directory to the Engine's builder and returns the
// last lines of its output. A step that fails, or runs out of its memory,
// fails the build.
func (c *Client) Build(ctx context.Context, dir string, o BuildOptions) (string, error) {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarDir(dir, pw)) }()
	q := url.Values{"t": {o.Tag}, "dockerfile": {o.Dockerfile}, "rm": {"1"}, "forcerm": {"1"}, "version": {"1"}}
	if o.MemoryMiB > 0 {
		b := strconv.FormatInt(int64(o.MemoryMiB)<<20, 10)
		q.Set("memory", b)
		q.Set("memswap", b)
	}
	if len(o.Labels) > 0 {
		l, _ := json.Marshal(o.Labels)
		q.Set("labels", string(l))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/build?"+q.Encode(), pr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	res, err := c.h.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return "", fmt.Errorf("docker build: %s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	return readStream(res.Body)
}

// Pull pulls an image by reference.
func (c *Client) Pull(ctx context.Context, ref string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/images/create?"+url.Values{"fromImage": {ref}}.Encode(), nil)
	if err != nil {
		return "", err
	}
	res, err := c.h.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return "", fmt.Errorf("docker pull %s: %s: %s", ref, res.Status, strings.TrimSpace(string(b)))
	}
	return readStream(res.Body)
}

// readStream reads the Engine's JSON progress stream, failing on an error
// message, and returns its last lines.
func readStream(r io.Reader) (string, error) {
	var tail []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var m struct {
			Stream, Status, Error string
			ErrorDetail           struct{ Message string }
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Error != "" {
			return strings.Join(tail, ""), fmt.Errorf("%s", m.Error)
		}
		line := m.Stream
		if line == "" && m.Status != "" {
			line = m.Status + "\n"
		}
		if line != "" {
			tail = append(tail, line)
			if len(tail) > 30 {
				tail = tail[1:]
			}
		}
	}
	return strings.Join(tail, ""), sc.Err()
}

// tarDir writes dir as a tar stream, leaving out .git.
func tarDir(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
