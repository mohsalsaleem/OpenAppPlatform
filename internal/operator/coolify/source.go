package coolify

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) FindSourceDeployment(ctx context.Context, id, repository, branch, commit string, since time.Time) (operator.NativeSourceDeployment, error) {
	resource, e := c.Inspect(ctx, id)
	if e != nil {
		return operator.NativeSourceDeployment{}, e
	}
	if resource.ArtifactKind != "source" {
		return operator.NativeSourceDeployment{}, errors.New("native observation requires a source application")
	}
	var app struct {
		Repository string `json:"git_repository"`
		Branch     string `json:"git_branch"`
		SourceID   int    `json:"source_id"`
		SourceType string `json:"source_type"`
	}
	if e = c.request(ctx, "GET", "/applications/"+url.PathEscape(id), nil, &app); e != nil {
		return operator.NativeSourceDeployment{}, e
	}
	repo := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(app.Repository, "https://github.com/"), "git@github.com:"), ".git")
	if !strings.EqualFold(repo, repository) || app.Branch != branch || app.SourceID < 1 || !strings.HasSuffix(app.SourceType, "GithubApp") {
		return operator.NativeSourceDeployment{}, errors.New("application does not match the configured GitHub App repository and branch")
	}
	matches := []operator.NativeSourceDeployment{}
	latest := ""
	for skip := 0; skip < 100; skip += 20 {
		var result struct {
			Count       int `json:"count"`
			Deployments []struct {
				ID      string `json:"deployment_uuid"`
				Commit  string `json:"commit"`
				Status  string `json:"status"`
				Created string `json:"created_at"`
			} `json:"deployments"`
		}
		if e = c.request(ctx, "GET", "/deployments/applications/"+url.PathEscape(id)+"?take=20&skip="+strconv.Itoa(skip), nil, &result); e != nil {
			return operator.NativeSourceDeployment{}, e
		}
		if skip == 0 && len(result.Deployments) > 0 {
			latest = result.Deployments[0].ID
		}
		for _, d := range result.Deployments {
			at, err := time.Parse(time.RFC3339Nano, d.Created)
			if err != nil {
				at, err = time.Parse("2006-01-02 15:04:05", d.Created)
			}
			if d.Commit != commit || err != nil || at.Before(since.Add(-2*time.Minute)) {
				continue
			}
			matches = append(matches, operator.NativeSourceDeployment{ID: d.ID, Commit: d.Commit, State: deploymentState(d.Status), ResourceStatus: resource.Status})
		}
		if len(result.Deployments) < 20 || skip+len(result.Deployments) >= result.Count {
			break
		}
	}
	if len(matches) > 1 {
		return operator.NativeSourceDeployment{}, errors.New("multiple native deployments match this commit; review the provider")
	}
	if len(matches) == 0 {
		return operator.NativeSourceDeployment{State: "pending"}, nil
	}
	if matches[0].State == "succeeded" && matches[0].ID != latest {
		matches[0].ResourceStatus = "superseded"
	}
	return matches[0], nil
}
