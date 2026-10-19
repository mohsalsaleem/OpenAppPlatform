package sqlite

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

var t0 = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

func at(seconds int64) time.Time { return t0.Add(time.Duration(seconds) * time.Second) }

func TestQueueBasics(t *testing.T) {
	q := open(t, ":memory:")
	if added, err := q.Enqueue(ctx, ops.Job{Key: "a", Kind: "apply", Payload: []byte("x")}); !added || err != nil {
		t.Fatalf("enqueue: %v %v", added, err)
	}
	if added, _ := q.Enqueue(ctx, ops.Job{Key: "a", Kind: "apply"}); added {
		t.Fatal("same key must not be added twice")
	}
	q.Enqueue(ctx, ops.Job{Key: "b", Kind: "apply"})

	j, err := q.Lease(ctx, "w1", at(0), at(10))
	if err != nil || j == nil || j.Key != "a" || j.Attempts != 1 || string(j.Payload) != "x" || j.LeasedBy != "w1" {
		t.Fatalf("lease oldest: %+v %v", j, err)
	}
	if j, _ := q.Lease(ctx, "w2", at(1), at(11)); j == nil || j.Key != "b" {
		t.Fatalf("second worker gets the next job: %+v", j)
	}
	if j, _ := q.Lease(ctx, "w3", at(2), at(12)); j != nil {
		t.Fatalf("nothing left while leases hold: %+v", j)
	}
	if err := q.Heartbeat(ctx, "a", "w2", at(3), at(20)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("only the holder may renew: %v", err)
	}
	if err := q.Heartbeat(ctx, "a", "w1", at(3), at(20)); err != nil {
		t.Fatal(err)
	}
	// b's lease ends at 11; w3 takes it over and w2 is fenced off.
	j, _ = q.Lease(ctx, "w3", at(11), at(30))
	if j == nil || j.Key != "b" || j.Attempts != 2 {
		t.Fatalf("expired lease taken over: %+v", j)
	}
	if err := q.Complete(ctx, "b", "w2", at(12)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("former holder must be fenced: %v", err)
	}
	if err := q.Complete(ctx, "a", "w1", at(25)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired holder must not complete: %v", err)
	}
	if err := q.Release(ctx, "b", "w3"); err != nil {
		t.Fatal(err)
	}
	if j, _ := q.Lease(ctx, "w1", at(26), at(40)); j == nil || j.Key != "a" {
		t.Fatalf("a (expired) is first again: %+v", j)
	}
	if err := q.Complete(ctx, "a", "w1", at(27)); err != nil {
		t.Fatal(err)
	}
	if j, _ := q.Lease(ctx, "w1", at(28), at(40)); j == nil || j.Key != "b" || j.Attempts != 3 {
		t.Fatalf("released job is available: %+v", j)
	}
}

// call is one queue call as the test observed it, in the store's order.
type call struct {
	op, worker, key string
	ok              bool
}

// recorder serializes queue calls through one lock so the log order is the
// order the store applied them in.
type recorder struct {
	mu  sync.Mutex
	q   *Store
	log []call
}

func (r *recorder) lease(worker string, now, until time.Time) *ops.Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, err := r.q.Lease(ctx, worker, now, until)
	if err != nil {
		panic(err)
	}
	if j != nil {
		r.log = append(r.log, call{"lease", worker, j.Key, true})
	}
	return j
}

func (r *recorder) do(op, worker, key string, fn func() error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := fn()
	if err != nil && !errors.Is(err, ErrLeaseLost) {
		panic(err)
	}
	r.log = append(r.log, call{op, worker, key, err == nil})
	return err == nil
}

// TestQueueRandomized runs many seeded rounds of concurrent workers against
// one queue. Workers complete, release, crash (drop the job) or stall past
// their lease while a shared logical clock moves on. Afterwards the log must
// show that every successful heartbeat or completion came from the worker
// that most recently leased the job (no job runs twice at once), and that
// every job completed exactly once (none is lost).
func TestQueueRandomized(t *testing.T) {
	// 10,000 runs take about 9 seconds normally and 40 minutes under the race
	// detector, so race builds run 500 unless OAP_QUEUE_RUNS says otherwise.
	// make rewrite-check runs both; the plan records a full race run.
	runs := 10000
	if raceBuild || testing.Short() {
		runs = 500
	}
	if n, err := strconv.Atoi(os.Getenv("OAP_QUEUE_RUNS")); err == nil && n > 0 {
		runs = n
	}
	t.Logf("%d seeded runs", runs)
	q := open(t, ":memory:")
	for run := 0; run < runs; run++ {
		if _, err := q.db.Exec(`DELETE FROM queue`); err != nil {
			t.Fatal(err)
		}
		seedRun(t, q, uint64(run))
	}
}

func seedRun(t *testing.T, q *Store, seed uint64) {
	r := rand.New(rand.NewPCG(seed, 3))
	jobs := 1 + r.IntN(6)
	workers := 2 + r.IntN(4)
	lease := int64(2 + r.IntN(5))
	for i := 0; i < jobs; i++ {
		q.Enqueue(ctx, ops.Job{Key: fmt.Sprintf("j%d", i), Kind: "k"})
	}
	rec := &recorder{q: q}
	var clock atomic.Int64
	var done atomic.Int64
	tick := func(r *rand.Rand) time.Time { return at(clock.Add(int64(r.IntN(3)))) }
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			wr := rand.New(rand.NewPCG(seed, uint64(100+w)))
			name := fmt.Sprintf("w%d", w)
			for steps := 0; done.Load() < int64(jobs) && steps < 5000; steps++ {
				now := tick(wr)
				j := rec.lease(name, now, now.Add(time.Duration(lease)*time.Second))
				if j == nil {
					// Nothing available: let time pass so abandoned leases expire.
					clock.Add(lease)
					runtime.Gosched()
					continue
				}
				switch roll := wr.IntN(10); {
				case roll < 2: // crash: drop the job; its lease will expire
				case roll < 4:
					rec.do("release", name, j.Key, func() error { return q.Release(ctx, j.Key, name) })
				default: // work, renewing the lease a few times, maybe stalling past it
					alive := true
					for beats := wr.IntN(4); beats > 0 && alive; beats-- {
						now = tick(wr)
						alive = rec.do("heartbeat", name, j.Key, func() error {
							return q.Heartbeat(ctx, j.Key, name, now, now.Add(time.Duration(lease)*time.Second))
						})
					}
					if alive {
						now = tick(wr)
						if rec.do("complete", name, j.Key, func() error { return q.Complete(ctx, j.Key, name, now) }) {
							done.Add(1)
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if done.Load() != int64(jobs) {
		t.Fatalf("seed %d: %d of %d jobs completed; jobs were lost", seed, done.Load(), jobs)
	}
	holder := map[string]string{}
	completed := map[string]int{}
	for i, c := range rec.log {
		switch {
		case c.op == "lease":
			holder[c.key] = c.worker
		case c.ok && holder[c.key] != c.worker:
			t.Fatalf("seed %d: call %d: %s by %s succeeded while %s held %s", seed, i, c.op, c.worker, holder[c.key], c.key)
		case c.ok && c.op == "complete":
			completed[c.key]++
		}
	}
	for k, n := range completed {
		if n != 1 {
			t.Fatalf("seed %d: %s completed %d times", seed, k, n)
		}
	}
	if j, _ := q.Lease(ctx, "checker", at(1<<30), at(1<<31)); j != nil {
		t.Fatalf("seed %d: %s left in the queue", seed, j.Key)
	}
}
