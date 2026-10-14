package controller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"regexp"
	"strings"
	"time"
)

// OperationQueue is observation-only and must be called after application scope
// authorization. Missing jobs never authorize redispatch or metadata repair.
func (c *Controller) OperationQueue(ctx context.Context, id string) domain.OperationQueueObservation {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := domain.OperationQueueObservation{State: "unavailable", ObservedAt: time.Now().UTC()}
	var state string
	var attempt, maxAttempts int
	var scheduled time.Time
	var paused *bool
	var raw []byte
	err := c.Store.Pool.QueryRow(ctx, `SELECT j.state,j.attempt,j.max_attempts,j.scheduled_at,CASE WHEN q.name IS NULL THEN NULL ELSE q.paused_at IS NOT NULL END,to_jsonb(j.errors) FROM river_job j LEFT JOIN river_queue q ON q.name=j.queue WHERE j.kind='oap_deploy' AND j.args->>'deploymentId'=$1 ORDER BY j.id DESC LIMIT 1`, id).Scan(&state, &attempt, &maxAttempts, &scheduled, &paused, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		out.State = "missing"
		return out
	}
	if err != nil {
		return out
	}
	out.State = state
	out.Attempt = &attempt
	out.MaxAttempts = &maxAttempts
	out.ScheduledAt = &scheduled
	out.Paused = paused
	var failures []struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &failures) == nil && len(failures) > 0 {
		out.LastErrorCode = queueFailureCode(failures[len(failures)-1].Error)
		if failures[len(failures)-1].Error == "cannot open deployment lock connection" {
			out.LastErrorCode = "lock_connection_unavailable"
		}
	}
	return out
}

var queueSQLState = regexp.MustCompile(`SQLSTATE ([A-Z0-9]{5})`)

func queueFailureCode(message string) string {
	if message == "Docker POST /networks/create returned HTTP 400" {
		return "network_creation_rejected"
	}
	if message == "image is not cached; pre-pull it or configure pullPolicy if-missing" {
		return "image_not_cached"
	}
	if code := queueSQLState.FindStringSubmatch(message); len(code) == 2 {
		return "database_" + code[1]
	}
	for prefix, code := range map[string]string{"deployment lock failed:": "deployment_lock_failed", "deployment read failed:": "deployment_read_failed", "deployment adapter failed:": "deployment_adapter_failed", "can't scan into dest[": "operation_decode_failed"} {
		if strings.HasPrefix(message, prefix) {
			return code
		}
	}
	return "worker_retry"
}
