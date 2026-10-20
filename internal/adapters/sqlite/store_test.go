package sqlite

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

var ctx = context.Background()

func open(t testing.TB, path string) *Store {
	t.Helper()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func entry(kind string, data string) ops.Entry {
	return ops.Entry{At: time.Date(2026, 10, 19, 14, 5, 0, 0, time.UTC), Kind: kind, Operation: "op_1", Step: 2, State: "started", Data: []byte(data)}
}

func TestAppendSinceHead(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "oap.db"))
	if h, _ := s.Head(ctx); h != 0 {
		t.Fatalf("empty head %d", h)
	}
	r1, err := s.Append(ctx, entry("proposal", "a"))
	if err != nil || r1 != 1 {
		t.Fatalf("first append: %d %v", r1, err)
	}
	r3, err := s.Append(ctx, entry("apply", "b"), entry("step", "c"))
	if err != nil || r3 != 3 {
		t.Fatalf("batch append: %d %v", r3, err)
	}
	got, err := s.Since(ctx, 1, 10)
	if err != nil || len(got) != 2 || got[0].Revision != 2 || string(got[1].Data) != "c" || got[0].Kind != "apply" {
		t.Fatalf("since: %+v %v", got, err)
	}
	if !got[0].At.Equal(entry("", "").At) || got[0].Operation != "op_1" || got[0].Step != 2 || got[0].State != "started" {
		t.Fatalf("fields not preserved: %+v", got[0])
	}
	if got, _ := s.Since(ctx, 0, 1); len(got) != 1 || got[0].Revision != 1 {
		t.Fatalf("limit: %+v", got)
	}
	if h, _ := s.Head(ctx); h != 3 {
		t.Fatalf("head %d", h)
	}
	if _, err := s.Append(ctx); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty append: %v", err)
	}
}

// A failure just before commit leaves no trace: no partial batch, and the
// next append continues from the last committed revision.
func TestFailedAppendIsAtomic(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "oap.db"))
	if _, err := s.Append(ctx, entry("a", "1")); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("disk on fire")
	s.beforeCommit = func() error { return injected }
	if _, err := s.Append(ctx, entry("b", "2"), entry("b", "3"), entry("b", "4")); !errors.Is(err, injected) {
		t.Fatalf("want injected failure, got %v", err)
	}
	s.beforeCommit = nil
	if got, _ := s.Since(ctx, 0, 10); len(got) != 1 {
		t.Fatalf("partial batch visible: %+v", got)
	}
	r, err := s.Append(ctx, entry("c", "5"))
	if err != nil || r != 2 {
		t.Fatalf("revision after a failed append: %d %v", r, err)
	}
}

func TestReopenKeepsDataAndMigrationsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oap.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, entry("a", "1"), entry("a", "2")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = open(t, path)
	if h, _ := s.Head(ctx); h != 2 {
		t.Fatalf("head after reopen %d", h)
	}
	var versions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil || versions != len(migrations) {
		t.Fatalf("migrations recorded %d times: %v", versions, err)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oap.db")
	s := open(t, path)
	if _, err := s.db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, len(migrations)+1); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("want a newer-schema error, got %v", err)
	}
}

// TestCrashWriterHelper is not a test on its own: TestCrashConsistency runs it
// in a child process that appends batches until it is killed.
func TestCrashWriterHelper(t *testing.T) {
	path := os.Getenv("OAP_SQLITE_CRASH_DB")
	if path == "" {
		t.Skip("helper process for TestCrashConsistency")
	}
	s, err := Open(ctx, path)
	if err != nil {
		fmt.Println("error", err)
		os.Exit(1)
	}
	out := bufio.NewWriter(os.Stdout)
	for batch := 0; ; batch++ {
		size := 1 + batch%3
		entries := make([]ops.Entry, size)
		for j := range entries {
			entries[j] = entry("crash", fmt.Sprintf("batch-%d-%d-of-%d", batch, j, size))
		}
		rev, err := s.Append(ctx, entries...)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Fprintf(out, "ack %d %d\n", rev, batch)
		out.Flush()
	}
}

// TestCrashConsistency kills a writer with SIGKILL at random points, mostly
// in the middle of a write, then reopens the database and checks that it is
// intact, that every acknowledged batch survived, that batches are whole and
// that revisions are gapless and only move forward.
func TestCrashConsistency(t *testing.T) {
	const runs = 20
	path := filepath.Join(t.TempDir(), "oap.db")
	lastHead := uint64(0)
	for run := 0; run < runs; run++ {
		r := rand.New(rand.NewPCG(uint64(run), 11))
		killAfter := 1 + r.IntN(200)
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashWriterHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "OAP_SQLITE_CRASH_DB="+path)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(stdout)
		acked, lastAck := 0, uint64(0)
		for lines.Scan() {
			f := strings.Fields(lines.Text())
			if len(f) == 0 || f[0] != "ack" {
				if len(f) > 0 && f[0] == "error" {
					t.Fatalf("run %d: writer failed: %s", run, lines.Text())
				}
				continue
			}
			lastAck, _ = strconv.ParseUint(f[1], 10, 64)
			if acked++; acked == killAfter {
				// The writer keeps writing; the kill lands at an arbitrary point.
				cmd.Process.Signal(syscall.SIGKILL)
				break
			}
		}
		cmd.Wait()

		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("run %d: reopen: %v", run, err)
		}
		if err := s.Check(ctx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		head, err := s.Head(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if head < lastAck || head < lastHead {
			t.Fatalf("run %d: head %d went behind an acknowledged revision %d or the previous head %d", run, head, lastAck, lastHead)
		}
		checkJournal(t, run, s, head)
		lastHead = head
		s.Close()
	}
}

// checkJournal reads the whole journal and checks gapless revisions and whole
// batches.
func checkJournal(t *testing.T, run int, s *Store, head uint64) {
	t.Helper()
	sizes := map[string]int{}
	seen := map[string]int{}
	next := uint64(1)
	for {
		page, err := s.Since(ctx, next-1, 500)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if e.Revision != next {
				t.Fatalf("run %d: revision %d follows %d", run, e.Revision, next-1)
			}
			next++
			// Data looks like batch-<n>-<j>-of-<size>; batches from earlier runs reuse numbers, so key by revision range start too.
			parts := strings.Split(string(e.Data), "-")
			size, _ := strconv.Atoi(parts[4])
			j, _ := strconv.Atoi(parts[2])
			key := fmt.Sprintf("%d@%d", size, e.Revision-uint64(j))
			sizes[key] = size
			seen[key]++
		}
	}
	if next-1 != head {
		t.Fatalf("run %d: journal ends at %d, head says %d", run, next-1, head)
	}
	for key, n := range seen {
		if n != sizes[key] {
			t.Fatalf("run %d: batch %s has %d of %d entries", run, key, n, sizes[key])
		}
	}
}
