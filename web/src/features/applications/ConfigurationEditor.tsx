import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Save, RotateCcw } from "lucide-react";
import {
  api,
  type Application,
  type Manifest,
  type Capabilities,
} from "../../api";
import { RuntimeVariables } from "./RuntimeVariables";
import { ErrorBox } from "../../components/Feedback";

export function ConfigurationEditor({
  application: app,
}: {
  application: Application;
}) {
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
    value: string | number | Record<string, string>,
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
              !dirty ||
              stale ||
              save.isPending ||
              Object.values(invalid).some(Boolean)
            }
          >
            <Save size={15} />
            {save.isPending ? "Saving…" : "Save configuration"}
          </button>
        </div>
      </div>
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
          <RuntimeVariables
            component={c}
            onChange={(field, value) => change(i, field, value)}
            onValidity={(valid) => setInvalid({ ...invalid, [c.name]: !valid })}
            environmentSupported={!!capabilities.data?.environment}
            connectionsSupported={!!capabilities.data?.applicationDns}
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
