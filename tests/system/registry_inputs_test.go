package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"strings"
	"testing"
)

type registryAdapter struct {
	*fake
	ensures  int
	password string
}

func (f *registryAdapter) Capabilities() operator.Capabilities {
	caps := f.fake.Capabilities()
	caps.RegistryCredentials = true
	return caps
}
func (f *registryAdapter) Ensure(ctx context.Context, s operator.Spec) (operator.Resource, error) {
	f.ensures++
	if s.Component.RegistryAuth == nil {
		return operator.Resource{}, domain.ErrConflict
	}
	f.password = s.Component.RegistryAuth.Password
	return f.fake.Ensure(ctx, s)
}
func TestRegistryAdmissionAndChangedBindingHold(t *testing.T) {
	ctx := context.Background()
	f := &registryAdapter{fake: &fake{resources: map[string]operator.Resource{}}}
	c, _ := setup(t, f)
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].Image = "registry.example/team/web:v1"
	m.Components[0].RegistryCredentialRef = "read-v1"
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Enqueue(ctx, app.ID, "missing-registry"); err == nil || f.ensures != 0 || f.deploys != 0 {
		t.Fatal("missing credential admitted")
	}
	grant := func(password string) *runtimeinputs.Inputs {
		raw, _ := json.Marshal([]runtimeinputs.RegistryBinding{{Reference: "read-v1", TargetID: m.TargetID, ApplicationID: app.ID, Component: "web", Registry: "registry.example", UsernameEnv: "OAP_SECRET_REGISTRY_USER", PasswordEnv: "OAP_SECRET_REGISTRY_PASSWORD"}})
		inputs, err := runtimeinputs.AddRegistries(nil, raw, func(key string) string {
			if key == "OAP_SECRET_REGISTRY_USER" {
				return "reader"
			}
			return password
		})
		if err != nil {
			t.Fatal(err)
		}
		return inputs
	}
	c.RuntimeInputs = grant("private-registry-fixture")
	d, err := c.Enqueue(ctx, app.ID, "valid-registry")
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(ctx, d.ID)
	if f.ensures != 1 || f.password != "private-registry-fixture" {
		t.Fatal("credential not delivered")
	}
	stored, _ := c.Store.Deployment(ctx, d.ID)
	raw, _ := json.Marshal(stored)
	if strings.Contains(string(raw), f.password) {
		t.Fatal("credential persisted in release")
	}
	c.RuntimeInputs = grant("changed-registry-fixture")
	if err := c.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	held, _ := c.Store.Deployment(ctx, d.ID)
	if held.State != "attention" || f.deploys != 0 {
		t.Fatal("changed credential dispatched")
	}
}
