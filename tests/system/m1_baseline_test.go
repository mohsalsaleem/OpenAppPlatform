package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strings"
	"testing"
)

type securityAdapter struct{ *fake }

func (f *securityAdapter) Capabilities() operator.Capabilities {
	caps := f.fake.Capabilities()
	caps.ContainerSecurity = true
	return caps
}
func TestSecurityExceptionsRequireHumanOwnerAndCapability(t *testing.T) {
	ctx := context.Background()
	f := &securityAdapter{&fake{resources: map[string]operator.Resource{}}}
	c, _ := setup(t, f)
	c.RequireIdentity = true
	m := manifest()
	m.Components[0].Security = &domain.ContainerSecurity{AllowPrivilegeEscalation: true, ExceptionReason: "Reviewed setuid tool for this fixture"}
	for _, p := range []domain.Principal{{Role: "owner", Kind: "agent"}, {Role: "operator", Kind: "session"}} {
		if _, err := c.CreateApplication(domain.WithPrincipal(ctx, p), m); err == nil {
			t.Fatal("non-human-owner exception accepted")
		}
	}
	owner := domain.WithPrincipal(ctx, domain.Principal{Role: "owner", Kind: "session"})
	if _, err := c.CreateApplication(owner, m); err != nil {
		t.Fatal("owner exception denied", err)
	}
	c2, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	m.Name = "unsupported-security"
	if _, err := c2.CreateApplication(ctx, m); err == nil {
		t.Fatal("unsupported adapter accepted security override")
	}
	m.Components[0].Security.ExceptionReason = "short"
	if err := m.Validate(); err == nil {
		t.Fatal("unreviewed exception accepted")
	}
}
func TestLocalNotificationOutboxIsAtomicIdempotentAndRedacted(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t, &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}})
	m := manifest()
	m.Components[0].Env = map[string]string{"TOKEN": "private-context-value"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "diagnostic-notification-fixture")
	if err != nil {
		t.Fatal(err)
	}
	d.State = "attention"
	d.Steps[0].Phase = "attention"
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Store.SaveDeploymentTx(ctx, tx, &d); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	var count int
	if err = c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_notification_outbox WHERE deployment_id=$1", d.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback leaked notification", err)
	}
	if err = c.Store.SaveDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err = c.Store.SaveDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err = c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_notification_outbox WHERE deployment_id=$1", d.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("outcome duplicated", err)
	}
	code, raw := request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/diagnostics", "", nil)
	if code != 200 || strings.Contains(string(raw), "private-context-value") || strings.Contains(string(raw), "TOKEN") {
		t.Fatal("diagnostic secret exposure", code)
	}
	var result map[string]any
	json.Unmarshal(raw, &result)
	if result["notificationDelivery"] != "disabled" || len(result["events"].([]any)) != 1 {
		t.Fatal("notification delivery policy changed")
	}
}
