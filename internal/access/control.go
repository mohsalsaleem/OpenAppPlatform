package access

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
	"time"
)

type AgentControl struct {
	Enabled          bool      `json:"enabled"`
	Version          int64     `json:"version"`
	Reason           string    `json:"reason"`
	UpdatedAt        time.Time `json:"updatedAt"`
	InferenceEnabled bool      `json:"inferenceEnabled"`
}

func (s Service) Control(ctx context.Context) (AgentControl, error) {
	var v AgentControl
	err := s.Pool.QueryRow(ctx, "SELECT enabled,version,reason,updated_at FROM oap_agent_control WHERE workspace_id='default'").Scan(&v.Enabled, &v.Version, &v.Reason, &v.UpdatedAt)
	return v, err
}
func (s Service) UpdateControl(ctx context.Context, enabled bool, version int64, reason string) (AgentControl, error) {
	reason = strings.TrimSpace(reason)
	if version < 1 || len(reason) > 240 || strings.ContainsAny(reason, "\r\n\x00") || (!enabled && len(reason) < 12) {
		return AgentControl{}, errors.New("pausing requires a 12–240 byte reason and current version")
	}
	var v AgentControl
	err := s.Pool.QueryRow(ctx, "UPDATE oap_agent_control SET enabled=$1,reason=$2,version=version+1,updated_at=now() WHERE workspace_id='default' AND version=$3 RETURNING enabled,version,reason,updated_at", enabled, reason, version).Scan(&v.Enabled, &v.Version, &v.Reason, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, domain.ErrConflict
	}
	if err != nil {
		return v, err
	}
	return v, nil
}
