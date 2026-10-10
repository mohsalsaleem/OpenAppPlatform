// Package source defines trusted GitHub bindings and the explicit image-builder contract.
package source

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

var sha = regexp.MustCompile(`^[a-f0-9]{40}$`)
var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var repo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var env = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var digest = regexp.MustCompile(`^[a-z0-9/._:-]+@sha256:[a-f0-9]{64}$`)
var ErrUncertain = errors.New("builder outcome is uncertain; inspect the builder before retrying")

type Component struct {
	Name            string `json:"name"`
	Context         string `json:"context"`
	ImageRepository string `json:"imageRepository"`
}
type Hook struct {
	Mode          string      `json:"mode,omitempty"`
	ID            string      `json:"id"`
	ApplicationID string      `json:"applicationId"`
	Repository    string      `json:"repository"`
	RepositoryID  int64       `json:"repositoryId"`
	Branch        string      `json:"branch"`
	SecretEnv     string      `json:"secretEnv"`
	CredentialEnv string      `json:"credentialEnv"`
	Components    []Component `json:"components"`
}
type Config struct {
	BuilderEnv []string `json:"builderEnv,omitempty"`
	Command    []string `json:"command"`
	Hooks      []Hook   `json:"hooks"`
}

func Parse(data []byte) (Config, error) {
	var c Config
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(&c); e != nil {
		return c, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("provide one source configuration document")
	}
	if len(c.Command) > 0 && (len(c.Command) > 16 || !strings.HasPrefix(c.Command[0], "/")) {
		return c, errors.New("configure an absolute trusted image-builder command")
	}
	for _, key := range c.BuilderEnv {
		if !env.MatchString(key) || key == "DATABASE_URL" || key == "COOLIFY_TOKEN" || key == "OAP_SETUP_TOKEN" || key == "OAP_API_TOKEN" {
			return c, errors.New("builder environment must use separate explicit secret references")
		}
	}
	seen := map[string]bool{}
	for _, h := range c.Hooks {
		if !slug.MatchString(h.ID) || seen[h.ID] || !repo.MatchString(h.Repository) || h.RepositoryID < 1 || h.ApplicationID == "" || h.Branch == "" || strings.ContainsAny(h.Branch, "\x00\n\r") || !env.MatchString(h.SecretEnv) || !env.MatchString(h.CredentialEnv) || len(h.Components) < 1 || len(h.Components) > 16 {
			return c, errors.New("invalid GitHub binding")
		}
		if h.Mode != "" && h.Mode != "image-builder" && h.Mode != "coolify-github-app" {
			return c, errors.New("unknown source mode")
		}
		if h.Mode != "coolify-github-app" && len(c.Command) == 0 {
			return c, errors.New("image mode requires a trusted builder command")
		}
		seen[h.ID] = true
		names := map[string]bool{}
		for _, v := range h.Components {
			if h.Mode == "coolify-github-app" {
				if !slug.MatchString(v.Name) || names[v.Name] {
					return c, errors.New("invalid native component mapping")
				}
				names[v.Name] = true
				continue
			}
			if !slug.MatchString(v.Name) || names[v.Name] || v.Context == "" || path.IsAbs(v.Context) || path.Clean(v.Context) != v.Context || v.Context == ".." || strings.HasPrefix(v.Context, "../") || !digest.MatchString(v.ImageRepository+"@sha256:"+strings.Repeat("a", 64)) || strings.LastIndex(v.ImageRepository, ":") > strings.LastIndex(v.ImageRepository, "/") {
				return c, errors.New("invalid component build mapping")
			}
			names[v.Name] = true
		}
	}
	return c, nil
}
func Verify(secret, signature string, body []byte) bool {
	if len(secret) < 24 || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	expected, e := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if e != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(expected, mac.Sum(nil))
}

type Push struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func (p Push) Valid(h Hook) bool {
	return !p.Deleted && p.After != strings.Repeat("0", 40) && sha.MatchString(p.After) && p.Ref == "refs/heads/"+h.Branch && p.Repository.ID == h.RepositoryID && strings.EqualFold(p.Repository.FullName, h.Repository)
}

type BuildRequest struct {
	BuildID      string    `json:"buildId"`
	Repository   string    `json:"repository"`
	RepositoryID int64     `json:"repositoryId"`
	Commit       string    `json:"commit"`
	Component    Component `json:"component"`
}
type BuildResult struct {
	Commit string `json:"commit"`
	Image  string `json:"image"`
}
type Builder interface {
	Build(context.Context, BuildRequest) (BuildResult, error)
}
type CommandBuilder struct {
	Command []string
	Env     []string
}
type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("builder response exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (b CommandBuilder) Build(ctx context.Context, r BuildRequest) (BuildResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.Command[0], b.Command[1:]...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for _, key := range b.Env {
		cmd.Env = append(cmd.Env, key+"="+os.Getenv(key))
	}
	input, _ := json.Marshal(r)
	cmd.Stdin = bytes.NewReader(input)
	out := &limitedOutput{limit: 8192}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if e := cmd.Run(); e != nil {
		return BuildResult{}, ErrUncertain
	}
	var result BuildResult
	d := json.NewDecoder(bytes.NewReader(out.Bytes()))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil {
		return result, ErrUncertain
	}
	var more any
	if d.Decode(&more) != io.EOF || !ValidResult(r, result) {
		return result, ErrUncertain
	}
	return result, nil
}

func ValidResult(r BuildRequest, result BuildResult) bool {
	return result.Commit == r.Commit && digest.MatchString(result.Image) && strings.HasPrefix(result.Image, r.Component.ImageRepository+"@sha256:")
}
