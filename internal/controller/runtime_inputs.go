package controller

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
)

func (c *Controller) runtimeComponent(m domain.Manifest, app, name string) (domain.Component, error) {
	comp, err := domain.RuntimeComponent(m, name)
	if err != nil {
		return comp, err
	}
	return c.RuntimeInputs.Resolve(m.TargetID, app, comp)
}
func (c *Controller) inputPins(d domain.Deployment) (map[string]string, error) {
	pins := map[string]string{}
	for _, step := range d.Steps {
		if step.Action == "retire" {
			continue
		}
		runtime, err := c.runtimeComponent(d.Manifest, d.ApplicationID, step.Component)
		if err != nil {
			return nil, err
		}
		if len(runtime.SecretRefs) > 0 || runtime.RegistryCredentialRef != "" {
			pins[step.Component] = runtimeinputs.Fingerprint(runtime, d.ID)
		}
	}
	return pins, nil
}
func (c *Controller) verifyInputPin(ctx context.Context, d domain.Deployment, comp domain.Component) error {
	runtime, err := c.runtimeComponent(d.Manifest, d.ApplicationID, comp.Name)
	if err != nil {
		return err
	}
	var expected string
	if err = c.Store.Pool.QueryRow(ctx, "SELECT fingerprint FROM oap_runtime_input_pins WHERE deployment_id=$1 AND component=$2", d.ID, comp.Name).Scan(&expected); err != nil {
		return errors.New("runtime secret recovery metadata is unavailable")
	}
	if runtimeinputs.Fingerprint(runtime, d.ID) != expected {
		return errors.New("runtime secret binding changed; restore the admitted binding before recovery")
	}
	return nil
}

// RedactRuntimeLogs fails closed if historical secret inputs cannot be resolved.
// References are immutable: a changed/missing value requires an explicit future
// rotation workflow; it must not expose old values retained in provider logs.
func (c *Controller) RedactRuntimeLogs(ctx context.Context, appID, logs string) (string, error) {
	rows, err := c.Store.Pool.Query(ctx, `SELECT DISTINCT d.id,d.spec,p.component,p.fingerprint FROM oap_runtime_input_pins p JOIN oap_deployments d ON d.id=p.deployment_id WHERE d.application_id=$1`, appID)
	if err != nil {
		return "", errors.New("runtime log redaction verification is unavailable")
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, expected string
		var m domain.Manifest
		if err = rows.Scan(&id, &m, &name, &expected); err != nil {
			return "", errors.New("runtime log redaction verification is unavailable")
		}
		runtime, err := c.runtimeComponent(m, appID, name)
		if err != nil || runtimeinputs.Fingerprint(runtime, id) != expected {
			return "", errors.New("logs unavailable until historical runtime secret bindings are restored")
		}
	}
	if rows.Err() != nil {
		return "", errors.New("runtime log redaction verification is unavailable")
	}
	return c.RuntimeInputs.Redact(logs), nil
}
