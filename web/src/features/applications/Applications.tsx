import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowUpRight,
  Box,
  Component as ComponentIcon,
  Plus,
} from "lucide-react";
import { api, type Application, type Target } from "../../api";
import { ErrorBox, Loading } from "../../components/Feedback";
export function Applications() {
  const q = useQuery({
    queryKey: ["applications"],
    queryFn: () => api<Application[]>("/applications"),
  });
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<Target[]>("/targets"),
  });
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">BUILD ON YOUR OWN TERMS</p>
          <h1>Your applications</h1>
          <p className="muted">
            Manage components, configuration, and releases.
          </p>
        </div>
        <Link className="button primary" to="/applications/new">
          <Plus size={17} /> New application
        </Link>
      </div>
      <div className="section-heading application-list-heading">
        <h2>Applications <span className="list-count">{q.data?.length ?? "—"}</span></h2>
        <span className="small muted">{targets.data?.length ?? "—"} deployment targets</span>
      </div>
      <ErrorBox error={q.error} />
      {q.isPending ? (
        <Loading />
      ) : q.data?.length === 0 ? (
        <div className="empty">
          <Box size={38} />
          <h2>Start with one application</h2>
          <p>Group your web services under one release and one overview.</p>
          <Link to="/applications/new" className="button primary">
            <Plus size={16} /> Create application
          </Link>
        </div>
      ) : (
        <div className="app-grid">
          {q.data?.map((a) => (
            <Link to={`/applications/${a.id}`} className="app-card" key={a.id}>
              <span className="app-icon"><Box size={18} /></span>
              <div>
                <h2>{a.manifest.name}</h2>
                <p>{a.manifest.components.map((c) => c.name).join(" · ")}</p>
              </div>
              <span className="environment">{a.manifest.environment}</span>
              <span className="application-target">{targets.data?.find((t) => t.id === a.manifest.targetId)?.name ?? a.manifest.targetId}</span>
              <span className="application-count">
                <ComponentIcon size={14} /> {a.manifest.components.length} components
              </span>
              <ArrowUpRight className="application-arrow" size={16} />
            </Link>
          ))}
        </div>
      )}

    </>
  );
}
