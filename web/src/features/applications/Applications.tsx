import { useCanOperate } from "../../access";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, Box, Plus } from "lucide-react";
import {
  api,
  type ApplicationGroup,
  type Target,
  type Application,
  type Instance,
  type Deployment,
} from "../../api";
import { replicaSummary, releaseOperation } from "./runtimeSummary";
import { Status } from "../../components/Status";
import { ErrorBox, Loading } from "../../components/Feedback";
export function Applications() {
  const canOperate = useCanOperate();
  const q = useQuery({
    queryKey: ["application-groups"],
    queryFn: () => api<ApplicationGroup[]>("/application-groups"),
  });
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<Target[]>("/targets"),
  });
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">KEEP YOUR EXISTING PLATFORM</p>
          <h1>Applications</h1>
          <p className="muted">
            Group existing services and manage their environments.
          </p>
        </div>
        {canOperate && (
          <div className="log-buttons">
            <Link className="button primary" to="/targets">
              Group Existing Services
            </Link>
            <Link className="button" to="/applications/new">
              <Plus size={17} /> New Application
            </Link>
          </div>
        )}
      </div>
      <div className="section-heading application-list-heading">
        <span className="small muted">
          {q.data?.length ?? "—"} applications
        </span>
        <span className="small muted">
          {targets.data?.length ?? "—"} deployment targets
        </span>
      </div>
      <ErrorBox error={q.error} />
      {q.isPending ? (
        <Loading />
      ) : q.data?.length === 0 ? (
        <div className="empty">
          <Box size={38} />
          <h2>Bring Your Existing Applications Together</h2>
          <p>
            Select services from your existing operator. Their native builds and
            deployments keep working.
          </p>
          <Link to="/targets" className="button primary">
            Choose a deployment target
          </Link>
        </div>
      ) : (
        <div className="app-grid">
          {q.data?.map((g) => (
            <section className="app-card application-group" key={g.id}>
              <span className="app-icon">
                <Box size={18} />
              </span>
              <div>
                <h2>
                  <Link to={`/applications/${g.environments[0].id}`}>
                    {g.name}
                  </Link>
                </h2>
                <p>
                  {g.environments.length} environment
                  {g.environments.length === 1 ? "" : "s"}
                </p>
              </div>
              <div className="application-count">
                {g.environments.map((a) => (
                  <EnvironmentSummary
                    key={a.id}
                    application={a}
                    target={targets.data?.find(
                      (t) => t.id === a.manifest.targetId,
                    )}
                  />
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
    </>
  );
}

function EnvironmentSummary({
  application: a,
  target,
}: {
  application: Application;
  target?: Target;
}) {
  const instances = useQuery({
    queryKey: ["instances", a.id],
    queryFn: () => api<Instance[]>(`/applications/${a.id}/instances`),
    refetchInterval: 10000,
  });
  const releases = useQuery({
    queryKey: ["deployments", a.id],
    queryFn: () => api<Deployment[]>(`/applications/${a.id}/deployments`),
    refetchInterval: 10000,
  });
  const latest = releases.data?.[0];
  return (
    <div className="environment-summary">
      <Link to={`/applications/${a.id}`} className="environment">
        {a.manifest.environment}
        <ArrowUpRight size={14} />
      </Link>
      <span className="small">
        {replicaSummary(
          a.manifest.components,
          instances.data,
          !!instances.error,
        )}
      </span>
      <span className="small muted">{target?.name || a.manifest.targetId}</span>
      <div className="latest-summary">
        {latest ? (
          <>
            <span className="small">
              {releaseOperation(latest)} ·{" "}
              {new Date(latest.createdAt).toLocaleString()}
            </span>
            <Status value={latest.state} />
          </>
        ) : (
          <span className="small muted">
            {releases.error
              ? "Deployment history unavailable"
              : releases.isPending
                ? "Checking deployments…"
                : "No recorded deployments"}
          </span>
        )}
      </div>
    </div>
  );
}
