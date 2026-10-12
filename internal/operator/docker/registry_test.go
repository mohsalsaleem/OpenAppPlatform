package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"strings"
	"testing"
)

func TestRegistryAuthOnlyOnPullAndSanitizedFailure(t *testing.T) {
	auth := &domain.RegistryAuth{Username: "reader", Password: "fixture-password", ServerAddress: "localhost:5000"}
	pulls := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.45/images/create" {
			pulls++
			raw, err := base64.URLEncoding.DecodeString(r.Header.Get("X-Registry-Auth"))
			if err != nil {
				t.Fatal(err)
			}
			var got domain.RegistryAuth
			if json.Unmarshal(raw, &got) != nil || got != *auth {
				t.Fatal("auth header not scoped")
			}
			w.Write([]byte(`{"error":"fixture-password"}`))
			return
		}
		if r.Header.Get("X-Registry-Auth") != "" {
			t.Fatal("auth on unrelated request")
		}
		http.NotFound(w, r)
	})
	c.settings.PullPolicy = "if-missing"
	_, err := c.image(context.Background(), "localhost:5000/team/private:v1", auth)
	if err == nil || strings.Contains(err.Error(), auth.Password) || pulls != 1 {
		t.Fatal("pull failure", err)
	}
	before := pulls
	_, err = c.Ensure(context.Background(), operator.Spec{Component: domain.Component{RegistryCredentialRef: "read-v1", Image: "localhost:5000/team/private:v1"}})
	if err == nil || pulls != before {
		t.Fatal("unresolved credential reached Engine")
	}
}
