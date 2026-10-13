import { useState } from "react";
import { useMutation,useQuery,useQueryClient } from "@tanstack/react-query";
import { api,unwrap } from "../../api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Disclosure } from "@/components/Disclosure";
import { ErrorBox } from "../../components/Feedback";
export function DeploymentPlans({id,currentVersion}:{id:string;currentVersion:number}){
 const [enabled,setEnabled]=useState(false);const client=useQueryClient();
 const identity=useQuery({queryKey:["me"],queryFn:()=>api.GET("/api/v1/auth/me").then(unwrap),retry:false});
 const owner=identity.data?.role==="owner" && identity.data?.kind==="session";
 const query=useQuery({queryKey:["deployment-plans",id],queryFn:()=>api.GET("/api/v1/applications/{id}/plans",{params:{path:{id}}}).then(unwrap),enabled:enabled && owner});
 const approve=useMutation({mutationFn:({plan,hash}:{plan:string;hash:string})=>api.POST("/api/v1/applications/{id}/plans/{plan}/approve",{params:{path:{id,plan}},body:{planHash:hash}}).then(unwrap),onSuccess:()=>client.invalidateQueries({queryKey:["deployment-plans",id]})});
 if(!owner)return null;
 return <section className="panel">
  <div className="section-heading"><h2>Agent Deployment Plans</h2><Button variant="outline" disabled={query.isFetching} onClick={()=>{setEnabled(true);if(enabled)query.refetch()}}>Review Plans</Button></div>
  <ErrorBox error={query.error || approve.error}/>
  {query.data?.length===0 && <p className="small muted">No agent plans to review.</p>}
  {query.data?.map(plan=>{const expired=new Date(plan.expiresAt).getTime()<=Date.now();const stale=plan.definitionVersion!==currentVersion;return <Disclosure key={plan.id} title={<span className="flex flex-wrap gap-2 items-center">Plan {plan.id.slice(0,8)}<Badge variant="outline">{plan.deploymentId ? "Queued" : expired ? "Expired" : stale ? "Stale" : plan.approvedAt ? "Approved" : "Needs Review"}</Badge></span>}>
   <p className="small">Definition v{plan.definitionVersion} · {plan.targetId} · {plan.environment}</p>
   <p className="small muted">{plan.effect}</p>
   {Object.entries(plan.images).map(([name,image])=><p className="small break-all" key={name}>{name} · {image}</p>)}
   <p className="small muted">Expires {new Date(plan.expiresAt).toLocaleString()} · Creator {plan.credentialId}</p>
   <Button variant="outline" disabled={approve.isPending || expired || stale || !!plan.deploymentId || !!plan.approvedAt} onClick={()=>approve.mutate({plan:plan.id,hash:plan.planHash})}>Approve This Plan</Button>
  </Disclosure>})}
 </section>;
}
