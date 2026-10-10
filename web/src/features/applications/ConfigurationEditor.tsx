import { HealthCheckFields, invalidHealthCheck } from "./HealthCheckFields";
import { ConfigurationDiff } from "./ConfigurationDiff";
import { useCanOperate } from "../../access";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Save, RotateCcw } from "lucide-react";
import {
  api,
  type Application,
  type Manifest,
  type Capabilities,
  type Component,
} from "../../api";
import { RuntimeVariables } from "./RuntimeVariables";
import { ErrorBox } from "../../components/Feedback";

export function ConfigurationEditor({
  application: app,
}: {
  application: Application;
}) {
  const canOperate = useCanOperate();
  const client = useQueryClient();
  const [base, setBase] = useState(app);
  const [draft, setDraft] = useState<Manifest>(() =>
    structuredClone(app.manifest),
  );
  const [saved, setSaved] = useState(false);
  const [editorRevision, setEditorRevision] = useState(0);
  const [invalid, setInvalid] = useState<Record<string, boolean>>({});
  const capabilities = useQuery({
    queryKey: ["capabilities", app.manifest.targetId],
    queryFn: () =>
      api<Capabilities>(`/targets/${app.manifest.targetId}/capabilities`),
  });
  const dirty = JSON.stringify(draft) !== JSON.stringify(base.manifest);
  const stale = app.version !== base.version;
  const save = useMutation({
    mutationFn: () =>
      api<Application>(`/applications/${app.id}`, {
        method: "PUT",
        body: JSON.stringify({
          manifest: draft,
          expectedVersion: base.version,
        }),
      }),
    onSuccess: (updated) => {
      setBase(updated);
      setDraft(structuredClone(updated.manifest));
      setSaved(true);
      setInvalid({});
      client.setQueryData(["application", app.id], updated);
      client.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  function reload() {
    setBase(app);
    setDraft(structuredClone(app.manifest));
    setSaved(false);
    save.reset();
    setEditorRevision((v) => v + 1);
    setInvalid({});
  }
  function change(
    index: number,
    field: string,
    value:
      | string
      | number
      | Record<string, string>
      | Component["healthCheck"]
      | Component["readiness"]
      | string[],
  ) {
    setSaved(false);
    setDraft({
      ...draft,
      components: draft.components.map((c, i) =>
        i === index ? { ...c, [field]: value } : c,
      ),
    });
  }
  return (
    <section className="panel">
      <div className="section-heading">
        <div>
          <h2>Runtime configuration</h2>
          <p className="muted">
            Definition version {base.version}. Saving changes future releases;
            it does not restart your components.
          </p>
        </div>
        <div className="log-buttons">
          <button
            className="secondary"
            onClick={reload}
            disabled={save.isPending}
          >
            <RotateCcw size={15} /> Reload definition
          </button>
          <button
            className="primary"
            onClick={() => save.mutate()}
            disabled={
              !canOperate ||
              !dirty ||
              stale ||
              save.isPending ||
              Object.values(invalid).some(Boolean) ||
              draft.components.some((c) => invalidHealthCheck(c.healthCheck)) ||
              draft.components.some(
                (c) =>
                  c.readiness?.timeoutSeconds !== undefined &&
                  (!Number.isInteger(c.readiness.timeoutSeconds) ||
                    c.readiness.timeoutSeconds < 30 ||
                    c.readiness.timeoutSeconds > 1800),
              )
            }
          >
            <Save size={15} />
            {save.isPending ? "Saving…" : "Save configuration"}
          </button>
        </div>
      </div>
      <ConfigurationDiff before={base.manifest} after={draft} />
      {stale && (
        <div className="error" role="alert">
          A newer definition is available. Reload before saving to avoid
          overwriting another change.
        </div>
      )}
      {saved && (
        <p role="status" className="saved-message">
          Configuration saved. Deploy when you are ready.
        </p>
      )}
      <ErrorBox error={save.error || capabilities.error} />
      {draft.components.map((c, i) => (
        <div
          className="component-form"
          key={`${c.name}-${base.version}-${editorRevision}`}
        >
          <h2>{c.name}</h2>
          <p className="small muted">
            {c.resourceId
              ? "Adopted resource: runtime fields are managed by the operator."
              : "Managed component · standard deployment"}
          </p>
          <div className="form-grid configuration-fields">
            <div>
              <label htmlFor={`config-image-${i}`}>
                Container image for {c.name}
              </label>
              <input
                id={`config-image-${i}`}
                value={c.image}
                readOnly={!!c.resourceId}
                onChange={(e) => change(i, "image", e.target.value)}
              />
            </div>
            <div className="form-grid compact">
              <div>
                <label htmlFor={`config-port-${i}`}>
                  Internal port for {c.name}
                </label>
                <input
                  id={`config-port-${i}`}
                  type="number"
                  min="1"
                  max="65535"
                  value={c.port}
                  readOnly={!!c.resourceId}
                  onChange={(e) => change(i, "port", +e.target.value)}
                />
              </div>
              <div>
                <label htmlFor={`config-instances-${i}`}>
                  Instances for {c.name}
                </label>
                <input
                  id={`config-instances-${i}`}
                  type="number"
                  min={base.manifest.components[i].instances}
                  max={c.resourceId ? 1 : 4}
                  value={c.instances}
                  readOnly={!!c.resourceId}
                  onChange={(e) => change(i, "instances", +e.target.value)}
                />
                <small className="muted">
                  Increase instances and deploy to scale up. Use Scale down in
                  the overview to retire instances where supported.
                </small>
              </div>
            </div>
            <div>
              <label htmlFor={`config-host-${i}`}>Host port for {c.name}</label>
              <input
                id={`config-host-${i}`}
                type="number"
                min="0"
                max="65535"
                value={c.hostPort || 0}
                readOnly={!!c.resourceId}
                onChange={(e) => change(i, "hostPort", +e.target.value)}
              />
              <small className="muted">
                0 keeps the component private. A fixed host port requires one
                instance.
              </small>
            </div>
          </div>
          {c.management !== "observe" && (
            <details className="dependency-fields">
              <summary>
                Startup dependencies for {c.name} ({c.dependsOn?.length || 0})
              </summary>
              <p className="small muted">
                Selected dependencies must finish every replica’s release
                readiness. Services outside the release are inspected without
                redeployment. This orders starts; it does not continuously
                monitor dependencies.
              </p>
              {draft.components
                .filter((other) => other.name !== c.name)
                .map((other) => (
                  <label key={other.name}>
                    <input
                      type="checkbox"
                      checked={c.dependsOn?.includes(other.name) || false}
                      onChange={(e) =>
                        change(
                          i,
                          "dependsOn",
                          e.target.checked
                            ? [...(c.dependsOn || []), other.name]
                            : (c.dependsOn || []).filter(
                                (name) => name !== other.name,
                              ),
                        )
                      }
                    />
                    Wait for {other.name} before starting {c.name}
                  </label>
                ))}
              {draft.components.length === 1 && (
                <p className="small muted">
                  Add another component through a supported assembly workflow to
                  define dependencies.
                </p>
              )}
            </details>
          )}
          {c.management !== "observe" && (
            <fieldset className="readiness-fields">
              <legend>Release readiness for {c.name}</legend>
              <label>
                <input
                  type="checkbox"
                  checked={!!c.readiness?.requireHealthy}
                  onChange={(e) =>
                    change(i, "readiness", {
                      ...c.readiness,
                      requireHealthy: e.target.checked,
                    })
                  }
                />
                Require operator-reported healthy status for {c.name}
              </label>
              <label htmlFor={`readiness-timeout-${i}`}>
                Observation timeout for {c.name} (seconds)
              </label>
              <input
                id={`readiness-timeout-${i}`}
                type="number"
                min="30"
                max="1800"
                value={c.readiness?.timeoutSeconds ?? 900}
                onChange={(e) =>
                  change(i, "readiness", {
                    ...c.readiness,
                    timeoutSeconds: +e.target.value,
                  })
                }
              />
              <p className="small muted">
                Starts after provider dispatch, including deployment and
                readiness waiting. This gate uses the operator-reported health
                check; it does not create a probe. A service with no reported
                health will time out when healthy status is required. Timeout
                requires recovery review and does not stop the provider.
              </p>
            </fieldset>
          )}
          {c.management !== "observe" &&
            !c.resourceId &&
            capabilities.data?.httpHealthChecks && (
              <HealthCheckFields
                name={c.name}
                healthCheck={c.healthCheck}
                allowExisting={
                  !base.manifest.components.find((b) => b.name === c.name)
                    ?.healthCheck
                }
                onChange={(value) => change(i, "healthCheck", value)}
              />
            )}
          <RuntimeVariables
            component={c}
            onChange={(field, value) => change(i, field, value)}
            onValidity={(valid) => setInvalid({ ...invalid, [c.name]: !valid })}
            environmentSupported={!!capabilities.data?.environment}
            connectionsSupported={
              !!(
                capabilities.data?.applicationDns ||
                capabilities.data?.serviceEndpoints
              )
            }
            endpointsSupported={!!capabilities.data?.serviceEndpoints}
          />
        </div>
      ))}
      <details>
        <summary>View application definition</summary>
        <pre>{JSON.stringify(draft, null, 2)}</pre>
      </details>
    </section>
  );
}
