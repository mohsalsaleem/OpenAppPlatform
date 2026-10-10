import {
  replicaSummary,
  releaseOperation,
  operatorName,
} from "./runtimeSummary";
import { healthCheckSummary } from "./HealthCheckFields";
import { ReleaseControls } from "./ReleaseControls";
import { RollbackReview } from "./RollbackReview";
import { ReleaseComparison } from "./ReleaseComparison";
import { WorkflowOwnership } from "./WorkflowOwnership";
import { EnvironmentNavigation } from "./EnvironmentNavigation";
import { SourceEvents } from "./SourceEvents";
import { useCanOperate } from "../../access";
import { useState, useEffect } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowUpRight,
  Component as ComponentIcon,
  Terminal,
} from "lucide-react";
import {
  api,
  type Application,
  type Deployment,
  type Instance,
  type Capabilities,
  type Target,
} from "../../api";
import { DeploymentDialog } from "../../components/DeploymentDialog";
import { ScaleDownDialog } from "./ScaleDownDialog";
import { RecoveryPanel } from "./RecoveryPanel";
import { ConfigurationEditor } from "./ConfigurationEditor";
import { Status } from "../../components/Status";
import { ErrorBox, Loading } from "../../components/Feedback";
const when = (s: string) =>
  new Date(s).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
export function ApplicationDetail() {
  const canOperate = useCanOperate();
  const { id } = useParams();
  const client = useQueryClient();
  const [handoff, setHandoff] = useState("");
  const [tab, setTab] = useState("overview");
  const [scaleReview, setScaleReview] = useState<{
    component: string;
    instances: number;
    version: number;
  } | null>(null);
  const [review, setReview] = useState<Application | null>(null);
  const [logs, setLogs] = useState("");
  const [logInstance, setLogInstance] = useState("");
  const [logError, setLogError] = useState("");
  const [restartReview, setRestartReview] = useState<Instance | null>(null);
  const app = useQuery({
    queryKey: ["application", id],
    queryFn: () => api<Application>(`/applications/${id}`),
  });
  const releases = useQuery({
    queryKey: ["deployments", id],
    queryFn: () => api<Deployment[]>(`/applications/${id}/deployments`),
    refetchInterval: 2000,
  });
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<Target[]>("/targets"),
  });
  const instances = useQuery({
    queryKey: ["instances", id],
    queryFn: () => api<Instance[]>(`/applications/${id}/instances`),
    refetchInterval: 5000,
  });
  useEffect(() => {
    const latest = releases.data?.[0];
    if (latest?.state === "succeeded" && latest.operation === "scale-down") {
      client.invalidateQueries({ queryKey: ["application", id] });
      client.invalidateQueries({ queryKey: ["instances", id] });
    }
  }, [releases.data?.[0]?.id, releases.data?.[0]?.state, client, id]);
  const deploy = useMutation({
    mutationFn: (definitionVersion: number) =>
      api<Deployment>(`/applications/${id}/deployments`, {
        method: "POST",
        body: JSON.stringify({ expectedVersion: definitionVersion }),
        headers: { "Idempotency-Key": crypto.randomUUID() },
      }),
    onSuccess: () => {
      setReview(null);
      setTab("deployments");
      client.invalidateQueries({ queryKey: ["deployments", id] });
    },
  });
  const capabilities = useQuery({
    queryKey: ["capabilities", app.data?.manifest.targetId],
    queryFn: () =>
      api<Capabilities>(`/targets/${app.data!.manifest.targetId}/capabilities`),
    enabled: !!app.data,
  });
  const restart = useMutation({
    mutationFn: ({
      instance,
      version,
    }: {
      instance: Instance;
      version: number;
    }) =>
      api<Deployment>(`/applications/${id}/restarts`, {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify({
          component: instance.component,
          ordinal: instance.ordinal,
          expectedVersion: version,
        }),
      }),
    onSuccess: () => {
      setRestartReview(null);
      setTab("deployments");
      client.invalidateQueries({ queryKey: ["deployments", id] });
      client.invalidateQueries({ queryKey: ["instances", id] });
    },
  });
  const enableManagement = useMutation({
    mutationFn: () =>
      api<Application>(`/applications/${id}/management`, {
        method: "POST",
        body: JSON.stringify({
          component: handoff,
          expectedVersion: app.data!.version,
        }),
      }),
    onSuccess: () => {
      setHandoff("");
      client.invalidateQueries({ queryKey: ["application", id] });
    },
  });
  async function showLogs(component: string, ordinal = 1) {
    try {
      setLogError("");
      setLogs("");
      setLogInstance(`${component} / ${ordinal}`);
      const x = await api<{ logs: string }>(
        `/applications/${id}/logs/${component}?ordinal=${ordinal}`,
      );
      setLogs(x.logs);
      setTab("logs");
    } catch (e) {
      setLogError((e as Error).message);
    }
  }
  if (app.isPending) return <Loading />;
  if (!app.data) return <ErrorBox error={app.error} />;
  const a = app.data;
  const latest = releases.data?.[0];
  const observed = a.manifest.components.some(
    (c) => c.management === "observe",
  );
  const active =
    latest &&
    (["queued", "running", "attention"].includes(latest.state) ||
      (latest.state === "abandoned" && !latest.control?.resolvedAt));
  return (
    <>
      <Link className="back" to="/">
        <ArrowLeft size={16} /> Applications
      </Link>
      <div className="page-heading">
        <div>
          <div className="title-line">
            <h1>{a.manifest.name}</h1>
            <EnvironmentNavigation applicationId={id!} />
          </div>
          <p className="muted">
            {a.manifest.components.length} component
            {a.manifest.components.length === 1 ? "" : "s"} ·{" "}
            {targets.data?.find((t) => t.id === a.manifest.targetId)?.name ||
              a.manifest.targetId}
          </p>
          <p className="runtime-summary" role="status">
            {replicaSummary(
              a.manifest.components,
              instances.data,
              !!instances.error,
            )}
          </p>
        </div>
        {observed ? (
          <span className="status neutral">Managed Externally</span>
        ) : (
          <button
            className="primary"
            disabled={
              !canOperate ||
              !!active ||
              observed ||
              !capabilities.data?.standard
            }
            onClick={() => setReview(structuredClone(a))}
          >
            <ArrowUpRight size={17} />
            {observed
              ? "Managed Externally"
              : latest?.state === "abandoned" && !latest.control?.resolvedAt
                ? "Reconciliation Required"
                : latest?.state === "attention"
                  ? "Deployment Needs Review"
                  : active
                    ? "Deployment in Progress"
                    : "Review Deployment"}
          </button>
        )}
      </div>
      {active && (
        <div role="status" className="operation-notice">
          {latest?.state === "abandoned"
            ? "Tracking is closed. Review the platform’s current state in Activity before another deployment."
            : latest?.state === "attention"
              ? "This deployment needs review. Open Activity to inspect the affected instances."
              : "A deployment is in progress. Follow its status in Activity."}
        </div>
      )}
      <div className="tabs" role="tablist" aria-label="Application views">
        {["overview", "deployments", "configuration", "logs"].map((t) => (
          <button
            role="tab"
            id={`tab-${t}`}
            aria-controls={`panel-${t}`}
            tabIndex={tab === t ? 0 : -1}
            aria-selected={tab === t}
            onKeyDown={(event) => {
              const keys = ["overview", "deployments", "configuration", "logs"];
              const index = keys.indexOf(t);
              const next =
                event.key === "ArrowRight"
                  ? keys[(index + 1) % keys.length]
                  : event.key === "ArrowLeft"
                    ? keys[(index + keys.length - 1) % keys.length]
                    : event.key === "Home"
                      ? keys[0]
                      : event.key === "End"
                        ? keys[keys.length - 1]
                        : undefined;
              if (next) {
                event.preventDefault();
                setTab(next);
                document.getElementById(`tab-${next}`)?.focus();
              }
            }}
            className={tab === t ? "selected" : ""}
            key={t}
            onClick={() => setTab(t)}
          >
            {
              (
                {
                  overview: "Overview",
                  deployments: "Activity",
                  configuration: "Settings",
                  logs: "Logs",
                } as Record<string, string>
              )[t]
            }
          </button>
        ))}
      </div>
      <ErrorBox error={releases.error || logError} />
      <div
        id="panel-overview"
        role="tabpanel"
        aria-labelledby="tab-overview"
        hidden={tab !== "overview"}
      >
        <ErrorBox error={capabilities.error} />
        <section className="panel">
          <div className="section-heading">
            <h2>Components</h2>
          </div>
          {a.manifest.components.map((c) => (
            <div className="component-row" key={c.name}>
              <span className="app-icon">
                <ComponentIcon size={20} />
              </span>
              <div className="grow">
                <strong>{c.name}</strong>
                <p>
                  {c.kind} · {c.instances} instance
                  {c.instances > 1 ? "s" : ""} · port {c.port}
                </p>
                {c.healthCheck && (
                  <p className="small muted">
                    {healthCheckSummary(c.healthCheck)}
                  </p>
                )}
                <p className="small">
                  {replicaSummary([c], instances.data, !!instances.error)}
                </p>
                <p className="small muted">
                  {c.management === "observe"
                    ? `Managed in ${operatorName(targets.data?.find((t) => t.id === a.manifest.targetId)?.operator)}`
                    : "Managed through Open App Platform"}
                </p>
                <details>
                  <summary>Image Details</summary>
                  {instances.data
                    ?.filter((v) => v.component === c.name && !v.retired)
                    .map((v) => (
                      <div key={v.ordinal}>
                        <span className="small">
                          Running · instance {v.ordinal}
                        </span>
                        <code className="image-ref">
                          {v.resource?.image || "Image unavailable"}
                        </code>
                      </div>
                    ))}
                  <span className="small">Saved for the next deployment</span>
                  <code className="image-ref">
                    {c.image || "Managed by the platform"}
                  </code>
                </details>
                {instances.data?.some(
                  (v) =>
                    !v.retired &&
                    v.component === c.name &&
                    v.resource?.image &&
                    v.resource.image !== c.image,
                ) &&
                  c.management !== "observe" && (
                    <p className="configuration-drift" role="status">
                      Saved image differs from the running image. Review before
                      deploying.
                    </p>
                  )}
              </div>
              <span className="small muted">
                {c.management === "observe"
                  ? "Observe-only"
                  : `${c.instances} configured`}
              </span>
              {c.management === "observe" &&
                instances.data?.find((i) => i.component === c.name)?.resource
                  ?.artifactKind === "image" &&
                !instances.data
                  ?.find((i) => i.component === c.name)
                  ?.resource?.description?.startsWith("OpenAppPlatform:") &&
                a.manifest.targetId &&
                capabilities.data?.managementHandoff && (
                  <button
                    className="secondary"
                    onClick={() => setHandoff(c.name)}
                    disabled={!canOperate || !!active}
                  >
                    Enable Management for {c.name}
                  </button>
                )}
              {capabilities.data?.retirement &&
                c.instances > 1 &&
                !c.resourceId && (
                  <button
                    className="secondary"
                    disabled={!canOperate || !!active}
                    onClick={() =>
                      setScaleReview({
                        component: c.name,
                        instances: c.instances,
                        version: a.version,
                      })
                    }
                  >
                    Scale Down {c.name}
                  </button>
                )}
              <button
                className="icon-button"
                title={`View ${c.name} logs`}
                aria-label={`View ${c.name} logs`}
                onClick={() => showLogs(c.name)}
              >
                <Terminal size={18} />
              </button>
            </div>
          ))}
        </section>
        <section className="panel">
          <div className="section-heading">
            <h2>Live Instances</h2>
            <button
              className="secondary"
              disabled={instances.isFetching}
              onClick={() => instances.refetch()}
            >
              Refresh Health
            </button>
          </div>
          <p className="small muted">
            Current operator state, separate from the recorded release result.
          </p>
          <ErrorBox error={instances.error} />
          {instances.isPending && <Loading />}
          {instances.data?.map((instance) => (
            <div
              className="step"
              key={`${instance.component}-${instance.ordinal}`}
            >
              <div className="grow">
                <strong>
                  {instance.component} / {instance.ordinal}
                  {instance.retired ? " · retained after retirement" : ""}
                </strong>
                <p className="small muted">
                  Checked {when(instance.checkedAt)}
                </p>
                {instance.error && (
                  <p className="error-inline">{instance.error}</p>
                )}
                {instance.resource?.url &&
                  /^https?:\/\//.test(instance.resource.url) && (
                    <a
                      href={instance.resource.url}
                      target="_blank"
                      rel="noreferrer"
                    >
                      Open Service
                    </a>
                  )}
              </div>
              <Status value={instance.status} />
              {capabilities.data?.restart && (
                <button
                  className="secondary"
                  disabled={
                    !canOperate ||
                    !!active ||
                    !instance.resourceId ||
                    instance.retired ||
                    a.manifest.components.find(
                      (c) => c.name === instance.component,
                    )?.management === "observe"
                  }
                  onClick={() => setRestartReview(instance)}
                >
                  Restart {instance.component} / {instance.ordinal}
                </button>
              )}
              <button
                className="icon-button"
                aria-label={`View ${instance.component} instance ${instance.ordinal} logs`}
                disabled={!instance.resourceId}
                onClick={() => showLogs(instance.component, instance.ordinal)}
              >
                <Terminal size={18} />
              </button>
            </div>
          ))}
        </section>
        <section className="panel release-banner">
          <div>
            <p className="eyebrow">LATEST DEPLOYMENT</p>
            <h2>
              {latest
                ? `${releaseOperation(latest)} · ${latest.id.slice(0, 8)}`
                : observed
                  ? "Observing Existing Services"
                  : "Ready for Your First Deployment"}
            </h2>
            <p className="muted">
              {latest
                ? `${when(latest.createdAt)}${latest.source ? ` · ${latest.source.repository}@${latest.source.commit.slice(0, 8)}` : ""}`
                : observed
                  ? "Health and logs are available. Workloads remain managed by your operator."
                  : "Your definition is saved. Deployment is a separate action."}
            </p>
          </div>
          {latest ? (
            <Status value={latest.state} />
          ) : (
            <span className="status neutral">
              {observed ? "Observe-only" : "Not deployed"}
            </span>
          )}
        </section>
      </div>
      <div
        id="panel-deployments"
        role="tabpanel"
        aria-labelledby="tab-deployments"
        hidden={tab !== "deployments"}
      >
        <SourceEvents
          applicationId={a.id}
          version={a.version}
          releases={releases.data ?? []}
        />
        <ReleaseComparison
          applicationId={a.id}
          releases={releases.data ?? []}
        />
        <section className="panel">
          <div className="section-heading">
            <h2>Release History</h2>
            <span className="small muted">
              Provider completion and workload state are tracked separately.
            </span>
          </div>
          {releases.data?.length === 0 && (
            <p className="muted">No deployments yet.</p>
          )}
          {releases.data?.map((d) => (
            <article className="release" key={d.id}>
              <div className="section-heading">
                <div>
                  <strong>
                    {releaseOperation(d)} · {d.id.slice(0, 8)}
                  </strong>
                  <p className="small muted">
                    {when(d.createdAt)} · definition v{d.definitionVersion}
                    {d.steps.some((step) => step.rollbackFrom)
                      ? ` · rollback from ${d.steps.find((step) => step.rollbackFrom)!.rollbackFrom!.slice(0, 8)}`
                      : ""}
                    {d.source
                      ? ` · ${d.source.repository}@${d.source.commit.slice(0, 8)}`
                      : ""}
                  </p>
                </div>
                <Status value={d.state} />
              </div>
              {d.manifest.components
                .filter((c) => c.healthCheck)
                .map((c) => (
                  <p className="small muted" key={`health-${c.name}`}>
                    {c.name} · {healthCheckSummary(c.healthCheck)}
                  </p>
                ))}
              {d.manifest.components
                .filter((c) => c.readiness || c.healthCheck?.mode === "http")
                .map((c) => (
                  <p className="small muted" key={`readiness-${c.name}`}>
                    {c.name} readiness ·{" "}
                    {c.healthCheck?.mode === "http" ||
                    c.readiness?.requireHealthy
                      ? "Operator healthy status required"
                      : "Operator running/health status"}{" "}
                    · {c.readiness?.timeoutSeconds || 900}s observation timeout
                  </p>
                ))}
              {d.manifest.components
                .filter((c) => c.dependsOn?.length)
                .map((c) => (
                  <p className="small muted" key={`dependencies-${c.name}`}>
                    {c.name} waits for {c.dependsOn!.join(", ")}
                  </p>
                ))}
              {d.steps.map((s) => (
                <div className="step" key={`${s.component}-${s.ordinal}`}>
                  <div>
                    <strong>
                      {s.component} / {s.ordinal}
                      {s.action ? ` · ${s.action}` : ""}
                    </strong>
                    <details>
                      <summary>Instance Details</summary>
                      <p className="small muted">
                        {s.resourceId
                          ? `Resource ${s.resourceId}`
                          : "Resource not prepared"}
                        {s.observed ? ` · ${s.observed}` : ""}
                      </p>
                    </details>
                    {s.error && <p className="error-inline">{s.error}</p>}
                  </div>
                  <Status value={s.phase} />
                </div>
              ))}
              {d.state === "succeeded" &&
                (d.operation || "deploy") === "deploy" && (
                  <RollbackReview
                    application={a}
                    release={d}
                    onQueued={() => setTab("deployments")}
                  />
                )}
              {d.state === "attention" && <RecoveryPanel deployment={d} />}
              <ReleaseControls deployment={d} />
            </article>
          ))}
        </section>
      </div>
      <div
        id="panel-logs"
        role="tabpanel"
        aria-labelledby="tab-logs"
        hidden={tab !== "logs"}
      >
        <section className="panel">
          <div className="section-heading">
            <h2>Component Logs</h2>
            <div className="log-buttons">
              {instances.data
                ?.filter((i) => i.resourceId)
                .map((i) => (
                  <button
                    className="secondary"
                    key={`${i.component}-${i.ordinal}`}
                    onClick={() => showLogs(i.component, i.ordinal)}
                  >
                    {i.component} / {i.ordinal}
                  </button>
                ))}
            </div>
          </div>
          <p className="small muted">
            Latest 100 lines
            {logInstance
              ? ` from ${logInstance}`
              : " from the selected instance"}
            . Logs can include application data.
          </p>
          <pre className="log-output">
            {logs || "Select a deployed component to fetch its logs."}
          </pre>
        </section>
      </div>
      {handoff && (
        <DeploymentDialog onClose={() => setHandoff("")}>
          <h2 id="deploy-title">Enable Management for {handoff}?</h2>
          <p>
            Allow Open App Platform to deploy and restart this existing
            image-backed service. This saves permission only; it does not change
            or restart the workload.
          </p>
          <p className="muted">
            Source, variables, routes, volumes, and native operator triggers
            stay with the operator. Deploying can interrupt requests. An
            application can deploy once all its components support management.
          </p>
          <ErrorBox error={enableManagement.error} />
          <div className="form-actions">
            <button
              className="secondary"
              disabled={enableManagement.isPending}
              onClick={() => setHandoff("")}
            >
              Cancel
            </button>
            <button
              className="primary"
              disabled={enableManagement.isPending}
              onClick={() => enableManagement.mutate()}
            >
              Confirm Management Handoff
            </button>
          </div>
        </DeploymentDialog>
      )}
      <div
        id="panel-configuration"
        role="tabpanel"
        aria-labelledby="tab-configuration"
        hidden={tab !== "configuration"}
      >
        <ConfigurationEditor key={a.id} application={a} />
        <EnvironmentNavigation applicationId={id!} organization />
        <WorkflowOwnership application={a} capabilities={capabilities.data} />
      </div>
      {scaleReview && (
        <ScaleDownDialog
          applicationId={id!}
          component={scaleReview.component}
          instances={scaleReview.instances}
          version={scaleReview.version}
          onClose={() => setScaleReview(null)}
          onQueued={() => {
            setScaleReview(null);
            setTab("deployments");
          }}
        />
      )}
      {restartReview && (
        <DeploymentDialog onClose={() => setRestartReview(null)}>
          <h2 id="deploy-title">
            Restart {restartReview.component} / {restartReview.ordinal}?
          </h2>
          <p>
            This restarts the existing instance and can interrupt requests.
            Saved configuration changes are not applied. Other instances keep
            running.
          </p>
          <ErrorBox error={restart.error} />
          <div className="form-actions">
            <button
              className="secondary"
              disabled={restart.isPending}
              onClick={() => setRestartReview(null)}
            >
              Cancel
            </button>
            <button
              className="primary"
              disabled={restart.isPending}
              onClick={() =>
                restart.mutate({ instance: restartReview, version: a.version })
              }
            >
              {restart.isPending ? "Queuing…" : "Restart Now"}
            </button>
          </div>
        </DeploymentDialog>
      )}
      {review && (
        <DeploymentDialog onClose={() => setReview(null)}>
          <h2 id="deploy-title">Deploy {a.manifest.name}?</h2>
          <p>
            This starts standard deployments for{" "}
            {review.manifest.components.length} components on{" "}
            <strong>{review.manifest.targetId}</strong> using definition v
            {review.version}.
          </p>
          <p className="muted">
            Existing managed instances may restart. Standard deployment can
            cause downtime. No databases or volumes will be removed.
          </p>
          <section
            className="deployment-images"
            aria-label="Deployment image changes"
          >
            <h3>Running → Proposed Images</h3>
            {review.manifest.components.map((c) => (
              <div key={c.name}>
                <strong>
                  {c.name} · {c.instances} instance
                  {c.instances === 1 ? "" : "s"}
                </strong>
                {instances.isFetching && !instances.data ? (
                  <p>Checking running images…</p>
                ) : instances.error ? (
                  <p className="error-inline">
                    Running images unavailable. Refresh health before deploying.
                  </p>
                ) : (
                  <>
                    {instances.data
                      ?.filter((v) => v.component === c.name && !v.retired)
                      .map((v) => (
                        <div key={v.ordinal}>
                          <span className="small">
                            Current instance {v.ordinal}
                          </span>
                          <code className="image-ref">
                            {v.resource?.image || "Image unavailable"}
                          </code>
                        </div>
                      ))}
                    {!instances.isPending &&
                      !instances.data?.some(
                        (v) => v.component === c.name && !v.retired,
                      ) && <p className="small muted">No existing instance.</p>}
                  </>
                )}
                <span className="small">Proposed image</span>
                <code className="image-ref">{c.image}</code>
              </div>
            ))}
          </section>
          {review.manifest.components
            .filter((c) => c.healthCheck)
            .map((c) => (
              <p className="small muted" key={`native-${c.name}`}>
                {c.name} · {healthCheckSummary(c.healthCheck)}
              </p>
            ))}
          {review.manifest.components
            .filter((c) => c.readiness || c.healthCheck?.mode === "http")
            .map((c) => (
              <p className="small muted" key={c.name}>
                {c.name}:{" "}
                {c.healthCheck?.mode === "http" || c.readiness?.requireHealthy
                  ? "requires operator-reported healthy status"
                  : "uses operator running/health status"}
                ; observation timeout {c.readiness?.timeoutSeconds || 900}s.
              </p>
            ))}
          {review.manifest.components
            .filter((c) => c.dependsOn?.length)
            .map((c) => (
              <p className="small muted" key={`dependencies-${c.name}`}>
                {c.name} waits for {c.dependsOn!.join(", ")}; all dependency
                replicas must pass readiness.
              </p>
            ))}
          <ErrorBox error={deploy.error} />
          <div className="form-actions">
            <button
              className="secondary"
              onClick={() => setReview(null)}
              disabled={deploy.isPending}
            >
              Cancel
            </button>
            <button
              className="primary"
              onClick={() => deploy.mutate(review.version)}
              disabled={
                deploy.isPending || instances.isPending || !!instances.error
              }
            >
              {deploy.isPending ? "Queuing…" : "Deploy Now"}
            </button>
          </div>
        </DeploymentDialog>
      )}
    </>
  );
}
