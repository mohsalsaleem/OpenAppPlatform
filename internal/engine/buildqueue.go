package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

// BuildQueue is the on-host build queue (R4.2). It sits in front of a
// builder and runs builds in the order they were asked for, Concurrency at
// a time (one by default), each only once the host has its memory: a
// build's MemoryMiB plus ReserveMiB for the running applications must fit
// in the memory the host reports available. Builders enforce MemoryMiB as a
// hard limit, so admitted builds can't take more than they were admitted
// with.
type BuildQueue struct {
	Builder ports.Builder
	Clock   ports.Clock
	// Available reports the host's available memory in MiB; ok is false
	// when the host doesn't report it, and builds are then admitted on
	// order and concurrency alone.
	Available func() (mib int, ok bool)
	// Concurrency is how many builds run at once; 1 when zero.
	Concurrency int
	// ReserveMiB is kept free for running applications; 128 when zero.
	ReserveMiB int
	// MaxWait bounds how long a build waits for its turn and its memory;
	// 30 minutes when zero. Poll is how often it checks; 1 s when zero.
	MaxWait, Poll time.Duration

	mu      sync.Mutex
	waiting []*ticket
	running int
	seq     uint64
	wake    chan struct{}
}

// ticket is a place in the queue. It must not be zero-size: Go may give
// distinct zero-size values the same address, and tickets are compared by
// pointer.
type ticket struct{ n uint64 }

// ErrNoMemory is a build that never got the memory it needs.
var ErrNoMemory = errors.New("build queue: not enough memory")

func (q *BuildQueue) defaults() {
	if q.Concurrency <= 0 {
		q.Concurrency = 1
	}
	if q.ReserveMiB <= 0 {
		q.ReserveMiB = 128
	}
	if q.MaxWait <= 0 {
		q.MaxWait = 30 * time.Minute
	}
	if q.Poll <= 0 {
		q.Poll = time.Second
	}
	if q.wake == nil {
		q.wake = make(chan struct{})
	}
}

// Build waits for its turn and its memory, then builds.
func (q *BuildQueue) Build(ctx context.Context, req ops.BuildRequest) (ops.BuildResult, error) {
	q.mu.Lock()
	q.defaults()
	q.seq++
	t := &ticket{n: q.seq}
	q.waiting = append(q.waiting, t)
	q.mu.Unlock()

	deadline := q.Clock.Now().Add(q.MaxWait)
	reason := ""
	for {
		q.mu.Lock()
		first := len(q.waiting) > 0 && q.waiting[0] == t
		admit := first && q.running < q.Concurrency
		if admit {
			if avail, ok := q.available(); ok && req.MemoryMiB+q.ReserveMiB > avail {
				admit = false
				reason = fmt.Sprintf("needs %d MiB plus %d MiB kept for applications; %d MiB available", req.MemoryMiB, q.ReserveMiB, avail)
			}
		}
		if admit {
			q.waiting = q.waiting[1:]
			q.running++
			q.mu.Unlock()
			break
		}
		wake := q.wake
		q.mu.Unlock()
		if !q.Clock.Now().Before(deadline) {
			q.leave(t)
			if reason != "" {
				return ops.BuildResult{}, fmt.Errorf("%w: %s", ErrNoMemory, reason)
			}
			return ops.BuildResult{}, fmt.Errorf("build queue: waited %s for a turn", q.MaxWait)
		}
		select {
		case <-ctx.Done():
			q.leave(t)
			return ops.BuildResult{}, ctx.Err()
		case <-wake:
		case <-time.After(q.Poll):
		}
	}
	defer q.done()
	if req.Started != nil {
		req.Started()
	}
	return q.Builder.Build(ctx, req)
}

func (q *BuildQueue) available() (int, bool) {
	if q.Available == nil {
		return 0, false
	}
	return q.Available()
}

// leave takes a ticket out of the queue without running it.
func (q *BuildQueue) leave(t *ticket) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, w := range q.waiting {
		if w == t {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			break
		}
	}
	q.signal()
}

func (q *BuildQueue) done() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.running--
	q.signal()
}

// signal wakes every waiting build to check its turn again.
func (q *BuildQueue) signal() {
	close(q.wake)
	q.wake = make(chan struct{})
}

// Queued reports how many builds are waiting and running.
func (q *BuildQueue) Queued() (waiting, running int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiting), q.running
}
