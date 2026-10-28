// Command oap-ext-github is OAP's GitHub fact source (R4.3): a read-only
// GitHub App that reports what GitHub knows about a commit (the files it
// changed, its CI, whether its pull request was approved, whether it is
// signed), so the owner's envelope can judge deploys by their source.
// Without an App key it reads public repositories anonymously.
package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension/sdk"
)

var (
	mu sync.Mutex
	gh *client
)

func main() {
	sdk.Serve(sdk.Extension{
		Manifest: &extensionv1.Manifest{Name: "github", Version: "1",
			Description: "Facts about commits from GitHub, through a read-only GitHub App.",
			Points:      []extensionv1.Point{extensionv1.Point_POINT_FACT_SOURCE},
			Capabilities: []*extensionv1.Capability{
				{Kind: &extensionv1.Capability_Setting{Setting: "app_id"}, Why: "the GitHub App to act as"},
				{Kind: &extensionv1.Capability_Setting{Setting: "installation_id"}, Why: "the App's installation (default: found per repository)"},
				{Kind: &extensionv1.Capability_Setting{Setting: "api_url"}, Why: "GitHub's API (default https://api.github.com)"},
				{Kind: &extensionv1.Capability_Secret{Secret: "github-app-key"}, Why: "the App's private key, to sign in as it"},
				{Kind: &extensionv1.Capability_Network{Network: "api.github.com:443"}, Why: "to read commits, checks and reviews"},
			}},
		Configure: func(ctx context.Context, settings, secrets map[string]string) error {
			c := &client{api: settings["api_url"], appID: settings["app_id"], installation: settings["installation_id"],
				http: &http.Client{Timeout: 20 * time.Second}, now: time.Now}
			if c.api == "" {
				c.api = "https://api.github.com"
			}
			if k := secrets["github-app-key"]; k != "" {
				key, perr := parseKey(k)
				if perr != nil {
					return perr
				}
				c.key = key
			}
			mu.Lock()
			gh = c
			mu.Unlock()
			return nil
		},
		Facts: func(ctx context.Context, req *extensionv1.FactsRequest) (*extensionv1.FactsResponse, error) {
			mu.Lock()
			c := gh
			mu.Unlock()
			return c.facts(ctx, req)
		},
	})
}
