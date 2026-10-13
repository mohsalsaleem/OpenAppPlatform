// ingress-authority is a read-only host-owner helper for a dedicated Caddy peer.
// It never publishes configuration or emits native configuration bodies.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ingress"
	"os"
	"time"
)

func main() {
	socket := flag.String("socket", "", "Private absolute Caddy administration socket")
	flag.Parse()
	client, err := ingress.New(*socket)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "ok": false, "error": "Private ingress socket required"})
		os.Exit(2)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := client.Read(ctx)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "ok": false, "error": "Ingress peer unavailable"})
		os.Exit(5)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "ok": true, "bootstrapHash": snapshot.Hash, "executed": false})
}
