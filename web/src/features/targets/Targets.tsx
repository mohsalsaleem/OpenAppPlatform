import { ExistingEnvironment } from "./ExistingEnvironment";
import { TargetSetup } from "./TargetSetup";
import { useCanOperate } from "../../access";
import { Link } from "react-router-dom";
import { useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { Server } from "lucide-react";
import { api, type Target, type Resource } from "../../api";
import { Status } from "../../components/Status";
import { ErrorBox, Loading } from "../../components/Feedback";
export function Targets() {
  const q = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<Target[]>("/targets"),
  });
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">KEEP YOUR INFRASTRUCTURE</p>
          <h1>Deployment targets</h1>
          <p className="muted">
            Configured server connections. Credentials remain on the controller.
          </p>
        </div>
      </div>
      <ExistingEnvironment targets={q.data ?? []} />
      <TargetSetup existingIds={q.data?.map((t) => t.id) ?? []} />
      <ErrorBox error={q.error} />
      {q.isPending ? (
        <Loading />
      ) : (
        q.data?.map((t) => <TargetCard key={t.id} target={t} />)
      )}
      {q.data?.length === 0 && (
        <div className="empty">
          <Server size={32} />
          <h2>No targets configured</h2>
          <p>Start the controller with a target configuration file.</p>
        </div>
      )}
      <div className="note">
        <Server size={20} />
        <div>
          <strong>Bare Docker and Coolify are supported targets.</strong>
          <p>
            Direct Docker uses owned containers and application networks. Other
            operator adapters will use the same application contract.
          </p>
        </div>
      </div>
    </>
  );
}
function TargetCard({ target: t }: { target: Target }) {
  const canOperate = useCanOperate();
  const [discover, setDiscover] = useState(false);
  const check = useMutation({
    mutationFn: () =>
      api<{
        checkedAt: string;
        resourceCount: number;
        nativeSourceObservation: boolean;
      }>(`/targets/${t.id}/connection`),
  });

  const q = useQuery({
    queryKey: ["resources", t.id],
    queryFn: () => api<Resource[]>(`/targets/${t.id}/resources`),
    enabled: discover,
  });
  return (
    <section className="panel">
      <div className="section-heading">
        <div>
          <h2>{t.name}</h2>
          <p className="muted">
            {t.operator} · {t.environment} · {t.url}
          </p>
        </div>
        <div className="log-buttons">
          <button
            className="secondary"
            onClick={() => check.mutate()}
            disabled={check.isPending}
          >
            {check.isPending ? "Checking…" : "Verify connection"}
          </button>
          {canOperate && (
            <Link className="button primary" to={`/targets/${t.id}/assemble`}>
              Group services
            </Link>
          )}
          <button
            className="secondary"
            onClick={() => {
              setDiscover(true);
              if (discover) q.refetch();
            }}
          >
            Inspect resources
          </button>
        </div>
      </div>
      <ErrorBox error={check.error || q.error} />
      {check.data && (
        <p role="status" className="saved-message">
          Connection verified · {check.data.resourceCount} resources in the
          configured scope. Existing builds and deployment triggers are
          preserved.
        </p>
      )}
      {discover &&
        (q.isPending ? (
          <Loading />
        ) : (
          <>
            <p className="small muted">
              {q.data?.length || 0} resources in this target environment
            </p>
            {q.data?.map((r) => (
              <div className="step" key={r.id}>
                <div>
                  <strong>{r.name}</strong>
                  <p className="small muted">{r.id}</p>
                </div>
                <Status value={r.status} />
              </div>
            ))}
          </>
        ))}
    </section>
  );
}
