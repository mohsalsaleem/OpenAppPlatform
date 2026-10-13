import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api,unwrap } from "../../api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Disclosure } from "@/components/Disclosure";
import { ErrorBox } from "../../components/Feedback";
import { Status } from "../../components/Status";
export function Diagnostics({id}:{id:string}){
 const [enabled,setEnabled]=useState(false);
 const query=useQuery({queryKey:["diagnostic-context",id],queryFn:()=>api.GET("/api/v1/applications/{id}/diagnostics",{params:{path:{id}}}).then(unwrap),enabled});
 const context=query.data;
 return <section className="panel">
  <div className="section-heading"><h2>Diagnostic Context</h2><Button variant="outline" disabled={query.isFetching} onClick={()=>{setEnabled(true);if(enabled)query.refetch()}}>{query.isFetching ? "Loading…" : "Inspect Context"}</Button></div>
  <div className="flex flex-wrap gap-2 mb-2"><Badge variant="outline">No AI Required</Badge><Badge variant="outline">Notification Delivery Disabled</Badge><Badge variant="outline">Values &amp; Logs Excluded</Badge></div>
  <ErrorBox error={query.error}/>
  {context && <>
   <p className="small muted">Definition v{context.definitionVersion} · {context.targetId} · bounded recorded release context.</p>
   {context.comparison && <Disclosure title={<>Latest Release &amp; Changes</>}>
    <p className="small">Release {context.comparison.to.id} · {context.comparison.to.operation} · commit {context.comparison.to.commit || "Not Recorded"}</p>
    {context.comparison.to.artifacts.map(artifact=><p className="small break-all" key={artifact.component}>{artifact.component} · {artifact.immutable ? "Digest Reference" : "Requested Tag"} · {artifact.image}</p>)}
    {context.comparison.changes.map((change,index)=><p className="small" key={index}>{change.component} · {change.field} · {change.before} → {change.after}</p>)}
   </Disclosure>}
   <Disclosure title={<>Local Notification Events</>}>
    <p className="small muted">Recorded once per release outcome. No external destination or delivery worker is enabled.</p>
    {context.events.length===0 && <p className="small muted">No recorded events yet.</p>}
    {context.events.map(event=><div className="step" key={event.id}><div className="grow"><strong>{event.operation} · {event.deploymentId.slice(0,8)}</strong><p className="small muted">{new Date(event.createdAt).toLocaleString()}</p></div><Status value={event.state}/></div>)}
   </Disclosure>
  </>}
 </section>;
}
