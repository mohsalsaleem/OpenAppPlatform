import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "../../api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Disclosure } from "@/components/Disclosure";
import { ErrorBox, Loading } from "../../components/Feedback";
export function Secrets(){
 const query=useQuery({queryKey:["secret-store"],queryFn:()=>api.GET("/api/v1/access/secret-store").then(unwrap)});
 const status=query.data;
 return <section className="panel">
  <div className="section-heading"><h2>Secret Store</h2><Button variant="outline" disabled={query.isFetching} onClick={()=>query.refetch()}>Refresh Status</Button></div>
  <div className="flex flex-wrap gap-2 mb-3"><Badge variant="outline">Optional · AES-256-GCM</Badge><Badge variant="outline">Host Managed</Badge><Badge variant="outline">Values Hidden</Badge></div>
  <ErrorBox error={query.error}/>
  {query.isPending ? <Loading/> : <p role="status" className="small">{status?.state==="ready" ? `Ready · ${status.grants.length} scoped grants loaded at startup.` : status?.state==="unavailable" ? "Store Unavailable · restore the original private key/store pair, check permissions, then restart OAP." : "Not Enabled · existing environment references and native operator secrets keep working."}</p>}
  <p className="small muted">Use the trusted host CLI for values. Refresh reads the current process snapshot; changes require an OAP restart and do not automatically update workloads.</p>
  {status?.grants.map(grant=><div className="step" key={[grant.reference,grant.targetId,grant.applicationId,grant.component,grant.variable].join(":")}>
   <div className="grow"><strong>{grant.reference}</strong><p className="small muted">{grant.targetId} · {grant.applicationId} · {grant.component}</p></div><Badge variant="outline">{grant.variable}</Badge>
  </div>)}
  <Disclosure title={<>Key Backup &amp; Recovery</>}>
   <p className="small muted">Save the encrypted store and its original 32-byte key separately, with private permissions. OAP metadata backups do not include these files. Keep a protected off-host recovery copy and test it before relying on the store.</p>
   <p className="small muted">A missing, wrong or tampered key/store leaves encrypted references unavailable. Restore the exact pair and restart; never generate a replacement key for an existing store. Inspect stale writer locks on the host before retrying. Registry logins and native operator secrets retain their existing ownership.</p>
  </Disclosure>
 </section>;
}
