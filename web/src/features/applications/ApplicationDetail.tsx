import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowUpRight,
  Component as ComponentIcon,
  Terminal,
} from "lucide-react";
import { api, type Application, type Deployment } from "../../api";
import { DeploymentDialog } from "../../components/DeploymentDialog";
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
function componentPhase(d: Deployment | undefined, name: string): string {
  const steps = d?.steps.filter((s) => s.component === name) || [];
  if (!steps.length) return "not deployed";
  if (steps.some((s) => s.phase === "attention")) return "attention";
  if (steps.some((s) => s.phase === "failed")) return "failed";
  if (steps.every((s) => s.phase === "succeeded")) return "succeeded";
  return steps.find((s) => s.phase !== "succeeded")?.phase || "pending";
}
export function ApplicationDetail() {
  const { id } = useParams();
  const client = useQueryClient();
  const [tab, setTab] = useState("overview");
  const [review, setReview] = useState<Application | null>(null);
  const [logs, setLogs] = useState("");
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
  async function showLogs(component: string) {
    try {
      setLogError("");
      const x = await api<{ logs: string }>(
        `/applications/${id}/logs/${component}`,
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
                <Status value={componentPhase(latest, c.name)} />
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
              {a.manifest.components.map((c) => (
                <button
                  className="secondary"
                  key={c.name}
                  onClick={() => showLogs(c.name)}
                >
                  {c.name}
                </button>
              ))}
            </div>
          </div>
          <p className="small muted">
            Latest 100 lines from the first instance. Logs can include
            application data.
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
