package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
)

func TestRollbackAuthorityUsesRollbackInsteadOfDeploy(t *testing.T) {
	d := domain.Deployment{Operation: "deploy", Steps: []domain.Step{{RollbackFrom: "previous-release"}}}
	if releaseAuthorizationAction(d) != "rollback" {
		t.Fatal("rollback unexpectedly needs deploy permission")
	}
	d.Steps = nil
	if releaseAuthorizationAction(d) != "deploy" {
		t.Fatal("deploy permission changed")
	}
	d.Operation = "restart"
	if releaseAuthorizationAction(d) != "restart" {
		t.Fatal("restart permission changed")
	}
}
