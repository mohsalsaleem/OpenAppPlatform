package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

// BindJob projects the current release's ephemeral process into existing scoped
// instance/log interfaces. Older resource IDs remain in immutable release steps.
// The controller holds the component write claim; job kinds cannot become workers.
func (s *Store) BindJob(ctx context.Context, target, ref, app, component, release string) error {
	if ref != "oap-job-"+release+"-"+component || len(release) != 32 {
		return domain.ErrConflict
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var old string
	err = tx.QueryRow(ctx, "SELECT resource_id FROM oap_bindings WHERE application_id=$1 AND component=$2 AND ordinal=1 FOR UPDATE", app, component).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, "INSERT INTO oap_bindings(target_id,resource_id,application_id,component,ordinal) VALUES($1,$2,$3,$4,1)", target, ref, app, component)
	} else if err == nil {
		if !strings.HasPrefix(old, "oap-job-") || !strings.HasSuffix(old, "-"+component) {
			return domain.ErrConflict
		}
		result, updateErr := tx.Exec(ctx, "UPDATE oap_bindings SET resource_id=$1,retired_at=NULL WHERE target_id=$2 AND application_id=$3 AND component=$4 AND ordinal=1", ref, target, app, component)
		err = updateErr
		if err == nil && result.RowsAffected() != 1 {
			return domain.ErrConflict
		}
	}
	if err != nil {
		return mapError(err)
	}
	return tx.Commit(ctx)
}
