// Command testext is a notifier extension for the host's tests. Its mode,
// set at build time, makes it behave well, crash, hang, hog memory or
// claim another name.
package main

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension/sdk"
)

var mode = "good"

var got struct {
	Settings map[string]string `json:"settings"`
	Secrets  []string          `json:"secrets"`
	Env      []string          `json:"env"`
}

var hoard [][]byte

func main() {
	name := "testext"
	if mode == "liar" {
		name = "someone-else"
	}
	sdk.Serve(sdk.Extension{
		Manifest: &extensionv1.Manifest{Name: name, Version: "1", Points: []extensionv1.Point{extensionv1.Point_POINT_NOTIFIER},
			Capabilities: []*extensionv1.Capability{
				{Kind: &extensionv1.Capability_Setting{Setting: "channel"}, Why: "where to deliver"},
				{Kind: &extensionv1.Capability_Secret{Secret: "token"}, Why: "to authenticate"},
				{Kind: &extensionv1.Capability_Secret{Secret: "spare"}, Why: "asked for, not granted"},
			}},
		Configure: func(ctx context.Context, settings, secrets map[string]string) error {
			got.Settings = settings
			for k, v := range secrets {
				got.Secrets = append(got.Secrets, k+"="+v)
			}
			sort.Strings(got.Secrets)
			for _, kv := range os.Environ() {
				k, _, _ := strings.Cut(kv, "=")
				got.Env = append(got.Env, k)
			}
			sort.Strings(got.Env)
			return nil
		},
		Notifier: func(ctx context.Context, req *extensionv1.NotifyRequest) (*extensionv1.NotifyResponse, error) {
			switch mode {
			case "crash":
				os.Exit(3)
			case "hang":
				select {}
			case "hog":
				for i := 0; i < 64; i++ { // 512 MiB, touched so it is resident
					b := make([]byte, 8<<20)
					for j := range b {
						b[j] = byte(j)
					}
					hoard = append(hoard, b)
					time.Sleep(10 * time.Millisecond)
				}
				select {}
			}
			b, _ := json.Marshal(got)
			return &extensionv1.NotifyResponse{Delivered: true, Detail: string(b)}, nil
		},
	})
}
