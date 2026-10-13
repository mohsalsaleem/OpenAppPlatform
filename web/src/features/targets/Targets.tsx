import { Disclosure } from "@/components/Disclosure";
import { Button } from "@/components/ui/button";
import { ExistingEnvironment } from "./ExistingEnvironment";
import { TargetSetup } from "./TargetSetup";
import { useCanOperate } from "../../access";
import { Link } from "react-router-dom";
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Server } from "lucide-react";
import { unwrap, api, type Target } from "../../api";
import { Status } from "../../components/Status";
import { ErrorBox, Loading } from "../../components/Feedback";
export function Targets() {
  const q = useQuery({
    queryKey: ["targets"],
    queryFn: () => api.GET("/api/v1/targets").then(unwrap),
  });
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">KEEP YOUR INFRASTRUCTURE</p>
          <h1>Deployment Targets</h1>
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
          <h2>No Targets Configured</h2>
          <p>Start the controller with a target configuration file.</p>
        </div>
      )}
    </>
  );
}
function TargetCard({ target: t }: { target: Target }) {
  const canOperate = useCanOperate();
  const [discover, setDiscover] = useState(false);
 const client=useQueryClient();
 const connection=useQuery({queryKey:["target-connection",t.id],queryFn:()=>api.GET("/api/v1/targets/{id}/connection-state",{params:{path:{id:t.id!}}}).then(unwrap),refetchInterval:30000});
 const evidence=connection.data;
 const expired=Boolean(evidence?.expiresAt && Date.now()>=Date.parse(evidence.expiresAt));
 const connectionState=expired ? "stale" : evidence?.state ?? "unknown";
 const capabilities=connectionState==="connected" ? evidence?.capabilities : undefined;
  const check = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/targets/{id}/connection-checks", {
          params: { path: { id: t.id! } },
        })
        .then(unwrap),
    onSuccess:value=>client.setQueryData(["target-connection",t.id],value),
  });

  const q = useQuery({
    queryKey: ["resources", t.id],
    queryFn: () =>
      api
        .GET("/api/v1/targets/{id}/resources", {
          params: { path: { id: t.id! } },
        })
        .then(unwrap),
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
          <Button
            variant="outline"

            onClick={() => check.mutate()}
            disabled={check.isPending || !canOperate}
          >
            {check.isPending ? "Checking…" : "Verify Connection"}
          </Button>
          {canOperate && (
            <Button asChild variant="default">
              <Link to={`/targets/${t.id}/assemble`}>Group Services</Link>
            </Button>
          )}
          <Button
            variant="outline"

            onClick={() => {
              setDiscover(true);
              if (discover) q.refetch();
            }}
          >
            Inspect Resources
          </Button>
        </div>
      </div>
      <ErrorBox error={check.error || connection.error || q.error} />
      <div className="flex items-center gap-2 mt-3" role="status">
        <Status value={connectionState}/>
        <span className="small muted">{connectionState==="connected" ? `Connection verified · ${evidence?.resourceCount} resources in the configured scope.` : expired ? "The previous connection observation has expired. Recheck before relying on it." : evidence?.message ?? "No recorded connection check."}</span>
      </div>
      {evidence?.checkedAt && <p className="small muted mt-2">Checked {new Date(evidence.checkedAt).toLocaleString()} · observation expires after 2 minutes.</p>}
      {evidence?.heldOperations.map(operation=>(
        <div className="step" key={operation.id}>
          <span className="small">Held Operation · {operation.id.slice(0,8)}</span>
          <Button variant="outline" size="sm" asChild><Link to={`/applications/${operation.applicationId}?tab=deployments#operation-${operation.id}`}>Review Operation</Link></Button>
        </div>
      ))}
      {capabilities && (
        <Disclosure
          className="workflow-ownership"
          title={<>Adapter Capabilities</>}
        >
          <p className="small muted">
            Reported adapter support after a read-only connection check.
            Resource eligibility and your access still apply.
          </p>
          <dl>
            {(
              [
                ["Standard image releases", capabilities.standard],
                [
                  "Image rollback safety checks",
                  capabilities.imageRollback,
                ],
                ["Instance restart", capabilities.restart],
                ["Retire owned instances", capabilities.retirement],
                [
                  "Managed environment variables",
                  capabilities.environment,
                ],
                [
                  "Application service DNS",
                  capabilities.applicationDns,
                ],
                [
                  "Explicit service endpoints",
                  capabilities.serviceEndpoints,
                ],
                [
                  "Image management handoff",
                  capabilities.managementHandoff,
                ],
                [
                  "Native source deployment observation",
                  evidence?.nativeSourceObservation,
                ],
                ["Rolling deployment", capabilities.rolling],
                ["Blue-green deployment", capabilities.blueGreen],
              ] as const
            ).map(([name, supported]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd>{supported ? "Supported" : "Unavailable in OAP"}</dd>
              </div>
            ))}
          </dl>
          <p className="small muted">
            Native source build/deploy commands and source management handoff
            are unavailable in OAP. Existing operator triggers remain in place.
          </p>
        </Disclosure>
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
