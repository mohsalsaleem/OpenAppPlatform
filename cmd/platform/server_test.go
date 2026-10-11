package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestProcessShutdownClosesAnOpenEventStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	})
	server := platformServer(ctx, "127.0.0.1:0", handler)
	listener, e := net.Listen("tcp", server.Addr)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	client := &http.Client{Timeout: 3 * time.Second}
	response, e := client.Get("http://" + listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if e = server.Shutdown(shutdown); e != nil {
		t.Fatal("open event stream held shutdown until its deadline", e)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("stream did not observe process cancellation")
	}
	if e = <-done; !errors.Is(e, http.ErrServerClosed) {
		t.Fatal(e)
	}
	body, e := io.ReadAll(response.Body)
	if e != nil || string(body) != "data: ready\n\n" {
		t.Fatal("stream did not close cleanly", string(body), e)
	}
}
