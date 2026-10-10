import { useCanOperate } from "../../access";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, Box, Plus } from "lucide-react";
import { api, type ApplicationGroup, type Target } from "../../api";
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
          <h1>Your applications</h1>
          <p className="muted">
            Group existing services and manage their environments.
          </p>
        </div>
        {canOperate && (
          <div className="log-buttons">
            <Link className="button primary" to="/targets">
              Group existing services
            </Link>
            <Link className="button" to="/applications/new">
              <Plus size={17} /> New application
            </Link>
          </div>
        )}
      </div>
      <div className="section-heading application-list-heading">
        <h2>
          Applications{" "}
          <span className="list-count">{q.data?.length ?? "—"}</span>
        </h2>
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
          <h2>Bring your existing applications together</h2>
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
                  <Link
                    key={a.id}
                    to={`/applications/${a.id}`}
                    className="environment"
                  >
                    {a.manifest.environment} <ArrowUpRight size={14} />
                  </Link>
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
    </>
  );
}
