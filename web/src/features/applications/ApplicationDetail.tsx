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
  const { id } = useParams();
  const client = useQueryClient();
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
  const active =
    latest && ["queued", "running", "attention"].includes(latest.state);
  return (
    <>
      <Link className="back" to="/">
        <ArrowLeft size={16} /> Applications
      </Link>
      <div className="page-heading">
        <div>
          <div className="title-line">
            <h1>{a.manifest.name}</h1>
            <span className="environment">{a.manifest.environment}</span>
          </div>
          <p className="muted">
            {a.manifest.components.length} component
            {a.manifest.components.length === 1 ? "" : "s"} ·{" "}
            {a.manifest.targetId}
          </p>
        </div>
        <button
          className="primary"
          disabled={!!active}
          onClick={() => setReview(structuredClone(a))}
        >
          <ArrowUpRight size={17} />
          {latest?.state === "attention"
            ? "Deployment needs review"
            : active
              ? "Deployment in progress"
              : "Deploy application"}
        </button>
      </div>
      <div className="tabs" role="tablist">
        {["overview", "deployments", "configuration", "logs"].map((t) => (
          <button
            role="tab"
            aria-selected={tab === t}
            className={tab === t ? "selected" : ""}
            key={t}
            onClick={() => setTab(t)}
          >
            {({ overview: "Overview", deployments: "Activity", configuration: "Settings", logs: "Logs" } as Record<string, string>)[t]}
          </button>
        ))}
      </div>
      <ErrorBox error={releases.error || logError} />
      {tab === "overview" && (
        <>
          <section className="panel release-banner">
            <div>
              <p className="eyebrow">LATEST RELEASE</p>
              <h2>
                {latest
                  ? `Release ${latest.id.slice(0, 8)}`
                  : "Ready for your first deployment"}
              </h2>
              <p className="muted">
                {latest
                  ? when(latest.createdAt)
                  : "Your definition is saved. Deployment is a separate action."}
              </p>
            </div>
            {latest ? (
              <Status value={latest.state} />
            ) : (
              <span className="status neutral">Not deployed</span>
            )}
          </section>
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
                  <code className="image-ref">
                    {instances.data?.find(
                      (v) => v.component === c.name && v.resource?.image,
                    )?.resource?.image || c.image}
                  </code>
                </div>
                <span className="small muted">{c.instances} configured</span>
                {capabilities.data?.retirement &&
                  c.instances > 1 &&
                  !c.resourceId && (
                    <button
                      className="secondary"
                      disabled={!!active}
                      onClick={() =>
                        setScaleReview({
                          component: c.name,
                          instances: c.instances,
                          version: a.version,
                        })
                      }
                    >
                      Scale down {c.name}
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
              <h2>Live instances</h2>
              <button
                className="secondary"
                disabled={instances.isFetching}
                onClick={() => instances.refetch()}
              >
                Refresh health
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
                        Open service
                      </a>
                    )}
                </div>
                <Status value={instance.status} />
                {capabilities.data?.restart && (
                  <button
                    className="secondary"
                    disabled={
                      !!active || !instance.resourceId || instance.retired
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
        </>
      )}
      {tab === "deployments" && (
        <section className="panel">
          <div className="section-heading">
            <h2>Release history</h2>
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
                  <strong>Release {d.id.slice(0, 8)}</strong>
                  <p className="small muted">
                    {when(d.createdAt)} · definition v{d.definitionVersion}
                  </p>
                </div>
                <Status value={d.state} />
              </div>
              {d.steps.map((s) => (
                <div className="step" key={`${s.component}-${s.ordinal}`}>
                  <div>
                    <strong>
                      {s.component} / {s.ordinal}
                      {s.action ? ` · ${s.action}` : ""}
                    </strong>
                    <p className="small muted">
                      {s.resourceId
                        ? `Resource ${s.resourceId}`
                        : "Resource not prepared"}
                      {s.observed ? ` · ${s.observed}` : ""}
                    </p>
                    {s.error && <p className="error-inline">{s.error}</p>}
                  </div>
                  <Status value={s.phase} />
                </div>
              ))}
              {d.state === "attention" && <RecoveryPanel deployment={d} />}
            </article>
          ))}
        </section>
      )}
      {tab === "configuration" && <ConfigurationEditor application={a} />}
      {tab === "logs" && (
        <section className="panel">
          <div className="section-heading">
            <h2>Component logs</h2>
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
      )}
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
              {restart.isPending ? "Queuing…" : "Restart now"}
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
              disabled={deploy.isPending}
            >
              {deploy.isPending ? "Queuing…" : "Deploy now"}
            </button>
          </div>
        </DeploymentDialog>
      )}
    </>
  );
}
