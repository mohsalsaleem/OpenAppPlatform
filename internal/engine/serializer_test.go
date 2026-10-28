package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSerializerRandomSchedules submits operations for several applications
// from concurrent goroutines with random timing, and checks for every seed
// that each application's operations ran one at a time in submission order.
func TestSerializerRandomSchedules(t *testing.T) {
	for seed := uint64(1); seed <= 300; seed++ {
		r := rand.New(rand.NewPCG(seed, 5))
		apps := 1 + r.IntN(4)
		perApp := 1 + r.IntN(6)

		var mu sync.Mutex
		var log []string // "start app n" / "end app n"
		active := map[string]int{}
		var overlapErr atomic.Value
		s := NewSerializer(func(ctx context.Context, op Operation) error {
			mu.Lock()
			if active[op.Application]++; active[op.Application] > 1 {
				overlapErr.Store(fmt.Sprintf("two operations ran at once on %s", op.Application))
			}
			log = append(log, "start "+op.ID)
			mu.Unlock()
			time.Sleep(time.Duration(r2(op.ID)) * time.Microsecond)
			mu.Lock()
			active[op.Application]--
			log = append(log, "end "+op.ID)
			mu.Unlock()
			return nil
		})

		// Submissions per app happen in a known order: each app has one
		// submitting goroutine, and apps submit concurrently.
		var wg sync.WaitGroup
		results := make([][]<-chan error, apps)
		for a := 0; a < apps; a++ {
			wg.Add(1)
			go func(a int) {
				defer wg.Done()
				wr := rand.New(rand.NewPCG(seed, uint64(a)))
				for n := 0; n < perApp; n++ {
					time.Sleep(time.Duration(wr.IntN(50)) * time.Microsecond)
					op := Operation{ID: fmt.Sprintf("app%d-%d", a, n), Application: fmt.Sprintf("app%d", a)}
					results[a] = append(results[a], s.Submit(context.Background(), op))
				}
			}(a)
		}
		wg.Wait()
		for a := range results {
			for _, ch := range results[a] {
				if err := <-ch; err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
			}
		}
		s.Wait()
		if msg := overlapErr.Load(); msg != nil {
			t.Fatalf("seed %d: %s", seed, msg)
		}
		next := map[string]int{}
		for _, entry := range log {
			var verb, id string
			fmt.Sscan(entry, &verb, &id)
			if verb != "start" {
				continue
			}
			var a, n int
			fmt.Sscanf(id, "app%d-%d", &a, &n)
			app := fmt.Sprintf("app%d", a)
			if n != next[app] {
				t.Fatalf("seed %d: %s started before %s-%d", seed, id, app, next[app])
			}
			next[app]++
		}
	}
}

// r2 derives a small, stable duration from an operation id.
func r2(id string) int {
	h := 0
	for _, c := range id {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h % 200
}

// TestSerializerRunsApplicationsInParallel blocks an operation on one
// application until an operation on another has started; a serializer that
// ran everything in one line would deadlock.
func TestSerializerRunsApplicationsInParallel(t *testing.T) {
	bStarted := make(chan struct{})
	s := NewSerializer(func(ctx context.Context, op Operation) error {
		if op.Application == "a" {
			select {
			case <-bStarted:
				return nil
			case <-time.After(5 * time.Second):
				return fmt.Errorf("application b never started while a was running")
			}
		}
		close(bStarted)
		return nil
	})
	a := s.Submit(context.Background(), Operation{ID: "a1", Application: "a"})
	b := s.Submit(context.Background(), Operation{ID: "b1", Application: "b"})
	if err := <-a; err != nil {
		t.Fatal(err)
	}
	if err := <-b; err != nil {
		t.Fatal(err)
	}
	s.Wait()
}

func TestSerializerSkipsCancelledOperations(t *testing.T) {
	release := make(chan struct{})
	var ran []string
	var mu sync.Mutex
	s := NewSerializer(func(ctx context.Context, op Operation) error {
		mu.Lock()
		ran = append(ran, op.ID)
		mu.Unlock()
		if op.ID == "1" {
			<-release
		}
		return nil
	})
	first := s.Submit(context.Background(), Operation{ID: "1", Application: "a"})
	ctx, cancel := context.WithCancel(context.Background())
	second := s.Submit(ctx, Operation{ID: "2", Application: "a"})
	third := s.Submit(context.Background(), Operation{ID: "3", Application: "a"})
	cancel()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != context.Canceled {
		t.Fatalf("cancelled operation: %v", err)
	}
	if err := <-third; err != nil {
		t.Fatal(err)
	}
	s.Wait()
	if fmt.Sprint(ran) != "[1 3]" {
		t.Fatalf("ran %v", ran)
	}
}
