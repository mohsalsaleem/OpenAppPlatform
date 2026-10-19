package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// ErrLeaseLost means the worker no longer holds the job: its lease ended or
// another worker leased it.
var ErrLeaseLost = errors.New("sqlite: lease lost")

// Enqueue adds a job unless one with the same key exists.
func (s *Store) Enqueue(ctx context.Context, job ops.Job) (bool, error) {
	var added bool
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO queue (key, kind, payload) VALUES (?, ?, ?)`, job.Key, job.Kind, job.Payload)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		added = n == 1
		return err
	})
	return added, err
}

// Lease leases the oldest available job to worker until the given time.
func (s *Store) Lease(ctx context.Context, worker string, now, until time.Time) (*ops.Job, error) {
	var job *ops.Job
	err := s.tx(ctx, func(tx *sql.Tx) error {
		j := ops.Job{}
		var leaseEnds int64
		err := tx.QueryRowContext(ctx,
			`SELECT key, kind, payload, attempts FROM queue
			 WHERE leased_by = '' OR lease_ends <= ? ORDER BY seq LIMIT 1`, now.UnixNano()).
			Scan(&j.Key, &j.Kind, &j.Payload, &j.Attempts)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		leaseEnds = until.UnixNano()
		if _, err := tx.ExecContext(ctx,
			`UPDATE queue SET leased_by = ?, lease_ends = ?, attempts = attempts + 1 WHERE key = ?`,
			worker, leaseEnds, j.Key); err != nil {
			return err
		}
		j.Attempts++
		j.LeasedBy, j.LeaseEnds = worker, time.Unix(0, leaseEnds).UTC()
		job = &j
		return nil
	})
	return job, err
}

// held runs a statement that changes a job only while worker holds an
// unexpired lease on it, and reports ErrLeaseLost otherwise.
func (s *Store) held(ctx context.Context, query string, args ...any) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLeaseLost
		}
		return nil
	})
}

// Heartbeat extends a lease the worker still holds and that hasn't ended.
func (s *Store) Heartbeat(ctx context.Context, key, worker string, now, until time.Time) error {
	return s.held(ctx, `UPDATE queue SET lease_ends = ? WHERE key = ? AND leased_by = ? AND lease_ends > ?`,
		until.UnixNano(), key, worker, now.UnixNano())
}

// Complete removes a job the worker holds under an unexpired lease.
func (s *Store) Complete(ctx context.Context, key, worker string, now time.Time) error {
	return s.held(ctx, `DELETE FROM queue WHERE key = ? AND leased_by = ? AND lease_ends > ?`, key, worker, now.UnixNano())
}

// Release returns a job the worker holds to the queue.
func (s *Store) Release(ctx context.Context, key, worker string) error {
	return s.held(ctx, `UPDATE queue SET leased_by = '', lease_ends = 0 WHERE key = ? AND leased_by = ?`, key, worker)
}
