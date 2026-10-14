package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestOperationQueueObservationIsBoundedAndDoesNotDispatch(t *testing.T) {
	c, api := setup(t, &fake{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Jobs.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Jobs.QueuePause(ctx, "default", nil); err != nil {
		t.Fatal(err)
	}
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.Enqueue(ctx, app.ID, "queue-observe-only")
	if err != nil {
		t.Fatal(err)
	}
	code, raw := request(t, api, "GET", "/api/v1/deployments/"+op.ID, "", nil)
	var observed domain.Deployment
	if code != 200 || json.Unmarshal(raw, &observed) != nil || observed.Queue == nil {
		t.Fatal("missing public queue evidence", code)
	}
	q := observed.Queue
	if q.State != "available" || q.Paused == nil || !*q.Paused || q.Attempt == nil || *q.Attempt != 0 || q.MaxAttempts == nil || *q.MaxAttempts != 10 || q.ScheduledAt == nil || q.ObservedAt.IsZero() {
		t.Fatal("incorrect queued evidence", q)
	}
	encoded, _ := json.Marshal(q)
	for _, private := range []string{"args", "errors", "token", "password", op.ID} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private queue information exposed")
		}
	}
	stored, _ := c.Store.Deployment(ctx, op.ID)
	if stored.State != "queued" {
		t.Fatal("inspection dispatched a paused operation")
	}
	if missing := c.OperationQueue(ctx, domain.NewID()); missing.State != "missing" || missing.Paused != nil {
		t.Fatal("missing job inferred as dispatched", missing)
	}
}
