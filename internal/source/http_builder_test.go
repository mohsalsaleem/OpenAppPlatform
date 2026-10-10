package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPBuilderPinnedResultAndNoCredentialRedirect(t *testing.T) {
	request := BuildRequest{Commit: strings.Repeat("a", 40), Component: Component{ImageRepository: "registry.test/app"}}
	token := strings.Repeat("s", 32)
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer destination.Close()
	redirect := false
	oversize := false
	wrong := false
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing builder credential")
		}
		if redirect {
			http.Redirect(w, r, destination.URL, 307)
			return
		}
		if oversize {
			w.Write([]byte(strings.Repeat("a", 8193)))
			return
		}
		commit := request.Commit
		if wrong {
			commit = strings.Repeat("b", 40)
		}
		json.NewEncoder(w).Encode(BuildResult{Commit: commit, Image: request.Component.ImageRepository + "@sha256:" + strings.Repeat("c", 64)})
	}))
	defer host.Close()
	b := HTTPBuilder{URL: host.URL, Token: token}
	if _, e := b.Build(context.Background(), request); e != nil {
		t.Fatal(e)
	}
	redirect = true
	if _, e := b.Build(context.Background(), request); e == nil || called {
		t.Fatal("redirect forwarded credentials")
	}
	redirect = false
	oversize = true
	if _, e := b.Build(context.Background(), request); e == nil {
		t.Fatal("oversized output accepted")
	}
	oversize = false
	wrong = true
	if _, e := b.Build(context.Background(), request); e == nil {
		t.Fatal("wrong commit accepted")
	}
}
