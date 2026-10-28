package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
)

// client reads commits, checks, statuses, pull requests and reviews from
// the GitHub REST API, as a GitHub App installation (read-only) or, with no
// key, anonymously.
type client struct {
	api   string // https://api.github.com
	appID string
	key   *rsa.PrivateKey
	// installation is fixed by the owner, or found per repository.
	installation string
	http         *http.Client
	now          func() time.Time

	mu     sync.Mutex
	tokens map[string]token // by installation
}

type token struct {
	value   string
	expires time.Time
}

func parseKey(pemText string) (*rsa.PrivateKey, error) {
	b, _ := pem.Decode([]byte(pemText))
	if b == nil {
		return nil, errors.New("the GitHub App key isn't PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(b.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App key: %v", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App key isn't an RSA key")
	}
	return rk, nil
}

// jwt is the App's own credential, valid for nine minutes.
func (c *client) jwt() (string, error) {
	enc := base64.RawURLEncoding.EncodeToString
	now := c.now()
	header := enc([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": c.appID})
	signing := header + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc(sig), nil
}

// auth returns the Authorization header for a repository's requests.
func (c *client) auth(ctx context.Context, repo string) (string, error) {
	if c.key == nil {
		return "", nil
	}
	jwt, err := c.jwt()
	if err != nil {
		return "", err
	}
	inst := c.installation
	if inst == "" {
		var v struct {
			ID int64 `json:"id"`
		}
		if err := c.do(ctx, "Bearer "+jwt, http.MethodGet, "/repos/"+repo+"/installation", &v); err != nil {
			return "", fmt.Errorf("the GitHub App isn't installed on %s: %w", repo, err)
		}
		inst = strconv.FormatInt(v.ID, 10)
	}
	c.mu.Lock()
	t, ok := c.tokens[inst]
	c.mu.Unlock()
	if ok && c.now().Add(time.Minute).Before(t.expires) {
		return "Bearer " + t.value, nil
	}
	var v struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(ctx, "Bearer "+jwt, http.MethodPost, "/app/installations/"+inst+"/access_tokens", &v); err != nil {
		return "", fmt.Errorf("installation token: %w", err)
	}
	c.mu.Lock()
	if c.tokens == nil {
		c.tokens = map[string]token{}
	}
	c.tokens[inst] = token{v.Token, v.ExpiresAt}
	c.mu.Unlock()
	return "Bearer " + v.Token, nil
}

func (c *client) do(ctx context.Context, auth, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.api, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "oap-ext-github")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode/100 != 2 {
		var e struct{ Message string }
		json.Unmarshal(body, &e)
		return fmt.Errorf("GitHub %s %s: %s %s", method, path, res.Status, e.Message)
	}
	return json.Unmarshal(body, out)
}

var repoRe = regexp.MustCompile(`^(?:https?://[^/]+/|git@[^:]+:|ssh://git@[^/]+/)?([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// ownerName turns a repository URL into owner/name.
func ownerName(repo string) (string, error) {
	m := repoRe.FindStringSubmatch(strings.TrimSpace(repo))
	if m == nil {
		return "", fmt.Errorf("%q isn't a GitHub repository", repo)
	}
	return m[1] + "/" + m[2], nil
}

// facts gathers what GitHub knows about a commit.
func (c *client) facts(ctx context.Context, req *extensionv1.FactsRequest) (*extensionv1.FactsResponse, error) {
	repo, err := ownerName(req.Repository)
	if err != nil {
		return nil, err
	}
	auth, err := c.auth(ctx, repo)
	if err != nil {
		return nil, err
	}
	get := func(path string, out any) error { return c.do(ctx, auth, http.MethodGet, "/repos/"+repo+path, out) }

	var commit struct {
		SHA    string `json:"sha"`
		Commit struct {
			Verification struct {
				Verified bool `json:"verified"`
			} `json:"verification"`
		} `json:"commit"`
		Files []struct {
			Filename string `json:"filename"`
		} `json:"files"`
	}
	if err := get("/commits/"+req.Commit, &commit); err != nil {
		return nil, err
	}
	out := &extensionv1.FactsResponse{Commit: commit.SHA, Signed: commit.Commit.Verification.Verified}
	for _, f := range commit.Files {
		out.FilesChanged = append(out.FilesChanged, f.Filename)
	}

	var runs struct {
		CheckRuns []struct {
			Status, Conclusion string
		} `json:"check_runs"`
	}
	if err := get("/commits/"+commit.SHA+"/check-runs?per_page=100", &runs); err != nil {
		return nil, err
	}
	var status struct {
		State    string `json:"state"`
		Statuses []struct {
			State string `json:"state"`
		} `json:"statuses"`
	}
	if err := get("/commits/"+commit.SHA+"/status", &status); err != nil {
		return nil, err
	}
	var runStates, statusStates []string
	for _, r := range runs.CheckRuns {
		if r.Status != "completed" {
			runStates = append(runStates, "pending")
		} else {
			runStates = append(runStates, r.Conclusion)
		}
	}
	for _, s := range status.Statuses {
		statusStates = append(statusStates, s.State)
	}
	out.Ci = ciState(runStates, statusStates)

	pr := int(req.PullRequest)
	if pr == 0 {
		var pulls []struct {
			Number int `json:"number"`
		}
		if err := get("/commits/"+commit.SHA+"/pulls", &pulls); err != nil {
			return nil, err
		}
		if len(pulls) > 0 {
			pr = pulls[0].Number
		}
	}
	if pr != 0 {
		var reviews []struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
			State string `json:"state"`
		}
		if err := get(fmt.Sprintf("/pulls/%d/reviews?per_page=100", pr), &reviews); err != nil {
			return nil, err
		}
		var states []review
		for _, r := range reviews {
			states = append(states, review{r.User.Login, r.State})
		}
		out.Reviewed = approved(states)
		out.PullRequest = int32(pr)
	}
	return out, nil
}

// ciState folds check-run conclusions (or "pending" for runs not done) and
// legacy commit statuses into one state. Any failure is red; else anything
// unfinished is pending; else any success is green; otherwise (nothing
// ran, or every check was skipped or neutral) none.
func ciState(runs, statuses []string) extensionv1.CiState {
	red, pending, green := false, false, false
	for _, r := range runs {
		switch r {
		case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
			red = true
		case "pending":
			pending = true
		case "success":
			green = true
		} // skipped and neutral checks verify nothing
	}
	for _, s := range statuses {
		switch s {
		case "failure", "error":
			red = true
		case "pending":
			pending = true
		case "success":
			green = true
		}
	}
	switch {
	case red:
		return extensionv1.CiState_CI_STATE_RED
	case pending:
		return extensionv1.CiState_CI_STATE_PENDING
	case green:
		return extensionv1.CiState_CI_STATE_GREEN
	}
	return extensionv1.CiState_CI_STATE_NONE
}

type review struct{ user, state string }

// approved: some reviewer's latest review approves, and nobody's latest
// review requests changes. Comments don't count either way; a dismissed
// review clears that reviewer's earlier ones.
func approved(reviews []review) bool {
	latest := map[string]string{}
	for _, r := range reviews {
		switch r.state {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			latest[r.user] = r.state
		}
	}
	ok := false
	for _, s := range latest {
		switch s {
		case "CHANGES_REQUESTED":
			return false
		case "APPROVED":
			ok = true
		}
	}
	return ok
}
