package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// fakeBuilder records when each build ran.
type fakeBuilder struct {
	mu       sync.Mutex
	spans    map[string][2]time.Time
	order    []string
	took     time.Duration
	inFlight int32
	maxSeen  int32
}

func (b *fakeBuilder) Build(ctx context.Context, req ops.BuildRequest) (ops.BuildResult, error) {
	n := atomic.AddInt32(&b.inFlight, 1)
	for {
		m := atomic.LoadInt32(&b.maxSeen)
		if n <= m || atomic.CompareAndSwapInt32(&b.maxSeen, m, n) {
			break
		}
	}
	start := time.Now()
	time.Sleep(b.took)
	atomic.AddInt32(&b.inFlight, -1)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spans == nil {
		b.spans = map[string][2]time.Time{}
	}
	b.spans[req.Component] = [2]time.Time{start, time.Now()}
	b.order = append(b.order, req.Component)
	return ops.BuildResult{Image: req.Image + ":x"}, nil
}

func buildAll(t *testing.T, q *BuildQueue, names ...string) []error {
	t.Helper()
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = q.Build(context.Background(), ops.BuildRequest{Component: n, Image: "img/" + n, MemoryMiB: 400})
		}()
		time.Sleep(20 * time.Millisecond) // asked for in this order
	}
	wg.Wait()
	return errs
}

func TestBuildQueueRunsOneAtATime(t *testing.T) {
	b := &fakeBuilder{took: 150 * time.Millisecond}
	q := &BuildQueue{Builder: b, Clock: sim.NewClock(time.Now()), Poll: 10 * time.Millisecond,
		Available: func() (int, bool) { return 900, true }}
	for _, err := range buildAll(t, q, "a", "b", "c") {
		if err != nil {
			t.Fatal(err)
		}
	}
	if b.maxSeen != 1 {
		t.Fatalf("%d builds ran at once", b.maxSeen)
	}
	if strings.Join(b.order, "") != "abc" {
		t.Fatalf("order %v", b.order)
	}
	if !b.spans["a"][1].Before(b.spans["b"][0]) && !b.spans["a"][1].Equal(b.spans["b"][0]) {
		t.Fatal("b started before a finished")
	}
}

func TestBuildQueueConcurrency(t *testing.T) {
	b := &fakeBuilder{took: 150 * time.Millisecond}
	q := &BuildQueue{Builder: b, Clock: sim.NewClock(time.Now()), Poll: 10 * time.Millisecond, Concurrency: 2}
	buildAll(t, q, "a", "b")
	if b.maxSeen != 2 {
		t.Fatalf("with concurrency 2, %d ran at once", b.maxSeen)
	}
}

// TestBuildQueueWaitsForMemory: a build that doesn't fit in the available
// memory (with the reserve) waits, runs once it fits, and gives up with
// the reason when it never does.
func TestBuildQueueWaitsForMemory(t *testing.T) {
	var avail atomic.Int32
	avail.Store(450) // 400 + 128 reserve doesn't fit
	clock := sim.NewClock(time.Now())
	b := &fakeBuilder{}
	q := &BuildQueue{Builder: b, Clock: clock, Poll: 10 * time.Millisecond, MaxWait: time.Minute,
		Available: func() (int, bool) { return int(avail.Load()), true }}
	done := make(chan error, 1)
	go func() {
		_, err := q.Build(context.Background(), ops.BuildRequest{Component: "a", MemoryMiB: 400})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if len(b.order) != 0 {
		t.Fatal("a build ran without the memory it needs")
	}
	avail.Store(600)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	avail.Store(300)
	go func() {
		_, err := q.Build(context.Background(), ops.BuildRequest{Component: "b", MemoryMiB: 400})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	clock.Advance(2 * time.Minute)
	err := <-done
	if !errors.Is(err, ErrNoMemory) || !strings.Contains(err.Error(), "300 MiB available") {
		t.Fatalf("never fits: %v", err)
	}
	if w, r := q.Queued(); w != 0 || r != 0 {
		t.Fatalf("queue after giving up: %d waiting, %d running", w, r)
	}
}

func TestBuildQueueCancel(t *testing.T) {
	b := &fakeBuilder{took: 300 * time.Millisecond}
	q := &BuildQueue{Builder: b, Clock: sim.NewClock(time.Now()), Poll: 10 * time.Millisecond}
	go q.Build(context.Background(), ops.BuildRequest{Component: "a"})
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := q.Build(ctx, ops.BuildRequest{Component: "b"})
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled build: %v", err)
	}
	if w, _ := q.Queued(); w != 0 {
		t.Fatal("a cancelled build stayed in the queue")
	}
}
