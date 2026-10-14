package access

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"slices"
	"time"
)

// IssueOnboarding grants one Docker target, bounded creation, and selected actions
// only on apps created by this credential. It never grants existing-app ownership.
func (s Service) IssueOnboarding(ctx context.Context, user, name string, permissions domain.AgentPermissions) (string, string, error) {
	if len(name) < 1 || len(name) > 80 || !permissions.CreateApplications || permissions.MaxApplications < 1 || permissions.MaxApplications > 10 || permissions.TargetID == "" || permissions.Environment == "" || permissions.Actions == nil {
		return "", "", ErrDenied
	}
	seen := map[string]bool{}
	for _, action := range permissions.Actions {
		if !slices.Contains([]string{"deploy", "restart", "configure", "recover", "remove-component", "rollback"}, action) || seen[action] {
			return "", "", ErrDenied
		}
		seen[action] = true
	}
	var valid bool
	if err := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_targets WHERE id=$1 AND spec->>'operator'='docker' AND spec->>'environment'=$2)", permissions.TargetID, permissions.Environment).Scan(&valid); err != nil || !valid {
		return "", "", ErrDenied
	}
	secret, id := Secret(), domain.NewID()
	raw, _ := json.Marshal(permissions)
	_, err := s.Pool.Exec(ctx, "INSERT INTO oap_credentials(id,user_id,token_hash,kind,name,scope,expires_at,permissions,target_grant) VALUES($1,$2,$3,'agent',$4,'operate',$5,$6,true)", id, user, Hash(secret), name, time.Now().Add(24*time.Hour), raw)
	return secret, id, err
}
