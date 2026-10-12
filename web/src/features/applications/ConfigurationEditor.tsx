import { StorageAndLimits, invalidStorageAndLimits } from "./StorageAndLimits";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { HealthCheckFields, invalidHealthCheck } from "./HealthCheckFields";
import { ConfigurationDiff } from "./ConfigurationDiff";
import { useCanOperate } from "../../access";
import { useState, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Save, RotateCcw } from "lucide-react";
import {
  unwrap,
  api,
  type Application,
  type Manifest,
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
  const [selectedComponent, setSelectedComponent] = useState(
    app.manifest.components[0]?.name,
  );
  const [portMapping, setPortMapping] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(
      app.manifest.components.map((c) => [c.name, !!c.hostPort]),
    ),
  );
  const [saved, setSaved] = useState(false);
  const [editorRevision, setEditorRevision] = useState(0);
  const [invalid, setInvalid] = useState<Record<string, boolean>>({});
  const capabilities = useQuery({
    queryKey: ["capabilities", app.manifest.targetId],
    queryFn: () =>
      api
        .GET("/api/v1/targets/{id}/capabilities", {
          params: { path: { id: app.manifest.targetId! } },
        })
        .then(unwrap),
  });
  const dirty = JSON.stringify(draft) !== JSON.stringify(base.manifest);
  const stale = app.version !== base.version;
  useEffect(() => {
    if (stale && !dirty && !Object.values(invalid).some(Boolean)) {
      setBase(app);
      setDraft(structuredClone(app.manifest));
      setSaved(false);
    }
  }, [app, stale, dirty, invalid]);
  useEffect(() => {
    if (!dirty && !Object.values(invalid).some(Boolean)) return;
    const prevent = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", prevent);
    return () => window.removeEventListener("beforeunload", prevent);
  }, [dirty, invalid]);
  const save = useMutation({
    mutationFn: () =>
      api
        .PUT("/api/v1/applications/{id}", {
          params: { path: { id: app.id! } },
          body: {
            manifest: draft,
            expectedVersion: base.version,
          },
        })
        .then(unwrap),
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
    setPortMapping(
      Object.fromEntries(
        app.manifest.components.map((c) => [c.name, !!c.hostPort]),
      ),
    );
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
      | string[]
      | Component["volumes"]
      | Component["limits"],
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
          <h2>Runtime Configuration</h2>
          <Badge variant="outline">
            {draft.components.find((c) => c.name === selectedComponent)
              ?.management === "observe"
              ? "Operator Managed"
              : "Applies on Next Deployment"}
          </Badge>
        </div>
        <div className="log-buttons">
          <Button
            variant="outline"

            onClick={reload}
            disabled={save.isPending}
          >
            <RotateCcw size={15} /> Reload Configuration
          </Button>
          <Button
            variant="default"

            onClick={() => save.mutate()}
            disabled={
              !canOperate ||
              !dirty ||
              stale ||
              save.isPending ||
              Object.values(invalid).some(Boolean) ||
              draft.components.some(invalidStorageAndLimits) ||
              draft.components.some(
                (c) =>
                  portMapping[c.name] &&
                  (!c.hostPort ||
                    c.hostPort < 1024 ||
                    c.hostPort > 65535 ||
                    !Number.isInteger(c.hostPort) ||
                    c.instances !== 1),
              ) ||
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
            {save.isPending ? "Saving…" : "Save Configuration"}
          </Button>
        </div>
      </div>
      {dirty && (
        <p role="status" className="saved-message">
          Unsaved changes · your draft stays available while switching
          application tabs.
        </p>
      )}
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
      <Tabs
        value={selectedComponent}
        onValueChange={setSelectedComponent}
        className="component-selector"
      >
        <TabsList aria-label="Configure Component">
          {draft.components.map((c) => (
            <TabsTrigger key={c.name} value={c.name}>
              {c.name}
            </TabsTrigger>
          ))}
        </TabsList>
        {draft.components.map((c, i) => (
          <TabsContent
            value={c.name}
            forceMount
            className="component-form"
            hidden={c.name !== selectedComponent}
            key={`${c.name}-${base.version}-${editorRevision}`}
          >
            <h2>{c.name}</h2>
            {c.kind === "database" && (
              <div className="component-badges">
                <Badge variant="outline">Operator Managed</Badge>
                <code className="image-ref">{c.databaseEndpoint}</code>
              </div>
            )}
            <p className="small muted">
              {c.kind === "database"
                ? "Existing database · Operator Managed"
                : c.resourceId
                  ? "Adopted resource: runtime fields are managed by the operator."
                  : c.kind === "worker"
                    ? "Long-running worker · standard deployment"
                    : "Web service · standard deployment"}
            </p>
            <div className="form-grid configuration-fields">
              <div>
                <Label htmlFor={`config-image-${i}`}>Container Image</Label>
                <Input
                  aria-label={`Container Image for ${c.name}`}
                  id={`config-image-${i}`}
                  value={c.image}
                  readOnly={!!c.resourceId}
                  onChange={(e) => change(i, "image", e.target.value)}
                />
              </div>
              <div className="form-grid compact">
                {c.kind === "web" && (
                  <div>
                    <Label htmlFor={`config-port-${i}`}>Internal Port</Label>
                    <Input
                      aria-label={`Internal Port for ${c.name}`}
                      id={`config-port-${i}`}
                      type="number"
                      min="1"
                      max="65535"
                      value={c.port}
                      readOnly={!!c.resourceId}
                      onChange={(e) => change(i, "port", +e.target.value)}
                    />
                  </div>
                )}
                <div>
                  <Label htmlFor={`config-instances-${i}`}>Instances</Label>
                  <Input
                    aria-label={`Instances for ${c.name}`}
                    id={`config-instances-${i}`}
                    type="number"
                    min={base.manifest.components[i].instances}
                    max={c.resourceId || c.volumes?.length ? 1 : 4}
                    value={c.instances}
                    readOnly={!!c.resourceId || !!c.volumes?.length}
                    onChange={(e) => change(i, "instances", +e.target.value)}
                  />
                  {!c.resourceId && (
                    <details className="field-help">
                      <summary>Scaling Help</summary>
                      <p className="small muted">
                        Increase instances and deploy to scale up. Use Scale
                        Down in the overview to retire instances where
                        supported.
                      </p>
                    </details>
                  )}
                </div>
              </div>
              {c.kind === "web" && (
                <div>
                  <Label>
                    <Checkbox
                      aria-label={`Map Host Port for ${c.name}`}
                      checked={portMapping[c.name] || !!c.hostPort}
                      disabled={!!c.resourceId || c.instances !== 1}
                      onCheckedChange={(checked) => {
                        setPortMapping({
                          ...portMapping,
                          [c.name]: checked === true,
                        });
                        if (checked !== true) change(i, "hostPort", 0);
                      }}
                    />
                    Map Host Port
                  </Label>
                  {(portMapping[c.name] || !!c.hostPort) && (
                    <>
                      <Label htmlFor={`config-host-${i}`}>Host Port</Label>
                      <Input
                        aria-label={`Host Port for ${c.name}`}
                        id={`config-host-${i}`}
                        type="number"
                        min="1024"
                        max="65535"
                        value={c.hostPort || 0}
                        readOnly={!!c.resourceId}
                        onChange={(e) => change(i, "hostPort", +e.target.value)}
                      />
                      {(!c.hostPort ||
                        c.hostPort < 1024 ||
                        c.instances !== 1) && (
                        <p className="error-inline" role="alert">
                          Choose a port from 1024–65535 for one instance.
                        </p>
                      )}
                    </>
                  )}
                  <details className="field-help">
                    <summary>Port Mapping Help</summary>
                    <p className="small muted">
                      0 adds no host-port mapping. Existing platform routing
                      remains. A fixed host port requires one instance.
                    </p>
                  </details>
                </div>
              )}
            </div>
            <StorageAndLimits
              component={c}
              saved={base.manifest.components.find((v) => v.name === c.name)}
              capabilities={capabilities.data}
              onChange={(next) => {
                setSaved(false);
                setDraft({
                  ...draft,
                  components: draft.components.map((v, n) =>
                    n === i ? next : v,
                  ),
                });
              }}
            />
            {c.management !== "observe" && (
              <details className="dependency-fields">
                <summary>
                  Startup Dependencies for {c.name} ({c.dependsOn?.length || 0})
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
                    <Label key={other.name}>
                      <Checkbox
                        checked={c.dependsOn?.includes(other.name) || false}
                        onCheckedChange={(e) =>
                          change(
                            i,
                            "dependsOn",
                            e === true
                              ? [...(c.dependsOn || []), other.name]
                              : (c.dependsOn || []).filter(
                                  (name) => name !== other.name,
                                ),
                          )
                        }
                      />
                      Wait for {other.name} before starting {c.name}
                    </Label>
                  ))}
                {draft.components.length === 1 && (
                  <p className="small muted">
                    Add another component through a supported assembly workflow
                    to define dependencies.
                  </p>
                )}
              </details>
            )}
            {c.management !== "observe" && (
              <fieldset className="readiness-fields">
                <legend>Deployment Readiness for {c.name}</legend>
                {c.healthCheck?.mode === "http" && (
                  <p className="small muted">
                    Healthy status is required by the selected HTTP check.
                  </p>
                )}
                <Label>
                  <Checkbox
                    checked={
                      c.healthCheck?.mode === "http" ||
                      !!c.readiness?.requireHealthy
                    }
                    disabled={c.healthCheck?.mode === "http"}
                    onCheckedChange={(e) =>
                      change(i, "readiness", {
                        ...c.readiness,
                        requireHealthy: e === true,
                      })
                    }
                  />
                  Require Healthy Status for {c.name}
                </Label>
                <Label htmlFor={`readiness-timeout-${i}`}>
                  Observation Timeout for {c.name} (seconds)
                </Label>
                <Input
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
                <details className="field-help">
                  <summary>Readiness Help</summary>
                  <p className="small muted">
                    Starts after provider dispatch, including deployment and
                    readiness waiting. This gate uses the operator-reported
                    health check; it does not create a probe. A service with no
                    reported health will time out when healthy status is
                    required. Timeout requires recovery review and does not stop
                    the provider.
                  </p>
                </details>
              </fieldset>
            )}
            {c.management !== "observe" &&
              !c.resourceId &&
              capabilities.data?.httpHealthChecks && (
                <HealthCheckFields
                  name={c.name}
                  allowHTTP={c.kind === "web"}
                  healthCheck={c.healthCheck}
                  allowExisting={
                    !base.manifest.components.find((b) => b.name === c.name)
                      ?.healthCheck
                  }
                  onChange={(value) => change(i, "healthCheck", value)}
                />
              )}
            <details className="advanced-configuration">
              <summary>Environment and Service Connections</summary>
              <RuntimeVariables
                component={c}
                onChange={(field, value) => change(i, field, value)}
                onValidity={(valid) =>
                  setInvalid({ ...invalid, [c.name]: !valid })
                }
                environmentSupported={!!capabilities.data?.environment}
                connectionsSupported={
                  !!(
                    capabilities.data?.applicationDns ||
                    capabilities.data?.serviceEndpoints
                  )
                }
                endpointsSupported={!!capabilities.data?.serviceEndpoints}
              />
            </details>
          </TabsContent>
        ))}
      </Tabs>
      <details>
        <summary>View Application Definition</summary>
        <pre>{JSON.stringify(draft, null, 2)}</pre>
      </details>
    </section>
  );
}
