import { useState } from "react";
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
} from "../../api";
import { DeploymentDialog } from "../../components/DeploymentDialog";
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
  const [review, setReview] = useState<Application | null>(null);
  const [logs, setLogs] = useState("");
  const [logInstance, setLogInstance] = useState("");
  const [logError, setLogError] = useState("");
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
            {t[0].toUpperCase() + t.slice(1)}
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
              <span className="small muted">Standard deployment</span>
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
                    {latest?.manifest.components.find((v) => v.name === c.name)
                      ?.image || c.image}
                  </code>
                </div>
                <span className="small muted">{c.instances} configured</span>
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
