package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

const TargetCheckTimeout = 10 * time.Second
const TargetObservationTTL = 2 * time.Minute

type HeldTargetOperation struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
}
type TargetConnectionState struct {
	State                   string                 `json:"state"`
	ObservedState           string                 `json:"observedState"`
	Message                 string                 `json:"message"`
	CheckedAt               *time.Time             `json:"checkedAt,omitempty"`
	ExpiresAt               *time.Time             `json:"expiresAt,omitempty"`
	ResourceCount           *int                   `json:"resourceCount,omitempty"`
	Capabilities            *operator.Capabilities `json:"capabilities,omitempty"`
	NativeSourceObservation bool                   `json:"nativeSourceObservation"`
	HeldOperations          []HeldTargetOperation  `json:"heldOperations"`
}

func targetObservationHash(t domain.Target) string {
	raw, _ := json.Marshal(struct {
		Target              domain.Target
		CredentialReference string
	}{t, t.TokenEnv})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (c *Controller) TargetConnection(ctx context.Context, id string) (TargetConnectionState, error) {
	target, err := c.Store.Target(ctx, id)
	if err != nil {
		return TargetConnectionState{}, err
	}
	state := TargetConnectionState{State: "unknown", ObservedState: "unknown", Message: "Connection has not been checked for this configuration."}
	var hash string
	var raw []byte
	err = c.Store.Pool.QueryRow(ctx, "SELECT target_hash,summary FROM oap_target_observations WHERE target_id=$1", id).Scan(&hash, &raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return state, err
	}
	if err == nil && hash == targetObservationHash(target) {
		if err = json.Unmarshal(raw, &state); err != nil {
			return state, err
		}
		if state.ExpiresAt == nil || !time.Now().Before(*state.ExpiresAt) {
			state.State = "stale"
			state.Message = "The previous connection observation has expired. Recheck before relying on it."
		}
	}
	state.HeldOperations = []HeldTargetOperation{}
	rows, err := c.Store.Pool.Query(ctx, "SELECT id,application_id FROM oap_deployments WHERE spec->>'targetId'=$1 AND state='attention' AND COALESCE(steps->0->'control'->>'mode','') NOT IN ('cancel','abandon') ORDER BY updated_at DESC LIMIT 20", id)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var operation HeldTargetOperation
		if err = rows.Scan(&operation.ID, &operation.ApplicationID); err != nil {
			return state, err
		}
		state.HeldOperations = append(state.HeldOperations, operation)
	}
	return state, rows.Err()
}

// CheckTarget records only adapter discovery; it cannot retry lifecycle work.
func (c *Controller) CheckTarget(ctx context.Context, id string) (TargetConnectionState, error) {
	target, err := c.Store.Target(ctx, id)
	if err != nil {
		return TargetConnectionState{}, err
	}
	started := time.Now().UTC()
	expires := started.Add(TargetObservationTTL)
	state := TargetConnectionState{State: "failed", ObservedState: "failed", Message: "Check the target configuration, credentials and operator scope.", CheckedAt: &started, ExpiresAt: &expires, HeldOperations: []HeldTargetOperation{}}
	probe, cancel := context.WithTimeout(ctx, TargetCheckTimeout)
	defer cancel()
	adapter, err := c.Factory(target)
	if err == nil {
		resources, discoveryErr := adapter.Discover(probe)
		err = discoveryErr
		if err == nil {
			count := len(resources)
			caps := adapter.Capabilities()
			_, native := adapter.(operator.NativeSourceObserver)
			state.State = "connected"
			state.ObservedState = "connected"
			state.Message = "Connection verified."
			state.ResourceCount = &count
			state.Capabilities = &caps
			state.NativeSourceObservation = native
		}
	}
	if errors.Is(err, operator.ErrUnreachable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(probe.Err(), context.DeadlineExceeded) {
		state.State = "unreachable"
		state.ObservedState = "unreachable"
		state.Message = "The operator could not be reached within the connection deadline."
	}
	// Use the caller context for metadata after the bounded probe expires.
	tx, e := c.Store.Pool.Begin(ctx)
	if e != nil {
		return state, e
	}
	defer tx.Rollback(ctx)
	var spec []byte
	var credential string
	if e = tx.QueryRow(ctx, "SELECT spec,token_env FROM oap_targets WHERE id=$1 FOR SHARE", id).Scan(&spec, &credential); e != nil {
		return state, e
	}
	var current domain.Target
	if e = json.Unmarshal(spec, &current); e != nil {
		return state, e
	}
	current.TokenEnv = credential
	if targetObservationHash(current) != targetObservationHash(target) {
		return state, domain.ErrConflict
	}
	raw, _ := json.Marshal(state)
	if _, e = tx.Exec(ctx, "INSERT INTO oap_target_observations(target_id,target_hash,checked_at,summary) VALUES($1,$2,$3,$4) ON CONFLICT(target_id) DO UPDATE SET target_hash=excluded.target_hash,checked_at=excluded.checked_at,summary=excluded.summary WHERE oap_target_observations.checked_at<=excluded.checked_at", id, targetObservationHash(target), started, raw); e != nil {
		return state, e
	}
	if e = tx.Commit(ctx); e != nil {
		return state, e
	}
	return c.TargetConnection(ctx, id)
}
