package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func recordNotification(ctx context.Context, tx pgx.Tx, d domain.Deployment) error {
	switch d.State {
	case "succeeded", "failed", "attention", "cancelled", "abandoned":
	default:
		return nil
	}
	operation := d.Operation
	if operation == "" {
		operation = "deploy"
	}
	for _, step := range d.Steps {
		if step.RollbackFrom != "" {
			operation = "rollback"
		}
	}
	_, err := tx.Exec(ctx, "INSERT INTO oap_notification_outbox(id,deployment_id,application_id,state,operation,commit_sha) SELECT $1,id,application_id,$3,$4,COALESCE(source->>'commit','') FROM oap_deployments WHERE id=$2 ON CONFLICT(deployment_id,state) DO NOTHING", domain.NewID(), d.ID, d.State, operation)
	return err
}
