// A small long-running workload with a process heartbeat and no HTTP listener.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const heartbeat = "/tmp/oap-worker-heartbeat"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "check-health" {
		raw, err := os.ReadFile(heartbeat)
		if err != nil {
			os.Exit(1)
		}
		timestamp, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		age := time.Now().Unix() - timestamp
		if err != nil || age < 0 || age > 10 {
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	boot := strconv.FormatInt(time.Now().UnixNano(), 10)
	for {
		if err := os.WriteFile(heartbeat, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0600); err != nil {
			log.Fatal("worker heartbeat unavailable")
		}
		log.Printf("worker active boot=%s", boot)
		select {
		case <-ctx.Done():
			log.Print("worker stopped")
			return
		case <-tick.C:
		}
	}
}
