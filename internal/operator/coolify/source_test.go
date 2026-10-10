package coolify

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeGitHubObservationPinsScopeRepositoryCommitAndNeverMutates(t *testing.T) {
	mutations := 0
	commit := strings.Repeat("a", 40)
	now := time.Now().UTC()
	repo := "owner/repository"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
			w.WriteHeader(500)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/projects/"):
			json.NewEncoder(w).Encode(map[string]any{"applications": []map[string]any{{"uuid": "source-app", "build_pack": "dockerfile"}}})
		case strings.Contains(r.URL.Path, "/deployments/"):
			json.NewEncoder(w).Encode(map[string]any{"count": 1, "deployments": []map[string]any{{"deployment_uuid": "provider-release", "commit": commit, "status": "finished", "created_at": now.Format(time.RFC3339)}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"uuid": "source-app", "build_pack": "dockerfile", "status": "running:healthy", "git_repository": repo, "git_branch": "main", "source_id": 2, "source_type": "App\\Models\\GithubApp"})
		}
	}))
	defer server.Close()
	c, e := New(domain.Target{URL: server.URL, ProjectID: "project", Environment: "staging"}, "token")
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.FindSourceDeployment(context.Background(), "source-app", repo, "main", commit, now)
	if e != nil || d.ID != "provider-release" || d.State != "succeeded" {
		t.Fatalf("native observation %+v %v", d, e)
	}
	if _, e = c.FindSourceDeployment(context.Background(), "foreign", repo, "main", commit, now); e == nil {
		t.Fatal("foreign source accepted")
	}
	repo = "different/repository"
	if _, e = c.FindSourceDeployment(context.Background(), "source-app", "owner/repository", "main", commit, now); e == nil {
		t.Fatal("repository drift accepted")
	}
	if mutations != 0 {
		t.Fatal("native observation mutated Coolify")
	}
}
