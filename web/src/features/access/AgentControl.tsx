import { useState } from "react";
import { useMutation,useQuery,useQueryClient } from "@tanstack/react-query";
import { api,unwrap } from "../../api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Disclosure } from "@/components/Disclosure";
import { ErrorBox } from "../../components/Feedback";
export function AgentControl(){
 const [reason,setReason]=useState("");const client=useQueryClient();
 const query=useQuery({queryKey:["agent-control"],queryFn:()=>api.GET("/api/v1/access/agent-control").then(unwrap)});
 const change=useMutation({mutationFn:(enabled:boolean)=>api.PUT("/api/v1/access/agent-control",{body:{enabled,expectedVersion:query.data!.version,reason}}).then(unwrap),onSuccess:()=>{setReason("");client.invalidateQueries({queryKey:["agent-control"]})}});
 return <div className="mb-4 border rounded-md p-4">
  <div className="flex flex-wrap items-center gap-2"><h3 className="mr-auto">Agent Access</h3><Badge variant={query.data?.enabled ? "secondary" : "outline"}>{query.data?.enabled ? "Enabled" : query.data ? "Paused" : "Loading"}</Badge><Badge variant="outline">OAP Inference Off</Badge></div>
  <ErrorBox error={query.error || change.error}/>
  {query.data && <>
   <Disclosure title={<>Pause or Resume Agents</>}>
    <p className="small muted">Pausing denies agent requests and holds new dispatches. Native work already in progress continues. Browser owner access remains available.</p>
    <Label htmlFor="agent-control-reason">Reason</Label><Input id="agent-control-reason" value={reason} maxLength={240} onChange={e=>setReason(e.target.value)} placeholder="Reason for changing agent access"/>
    <Button className="mt-3" variant="outline" disabled={change.isPending || (query.data.enabled && reason.trim().length<12)} onClick={()=>change.mutate(!query.data!.enabled)}>{query.data.enabled ? "Pause Agent Access" : "Resume Agent Access"}</Button>
    {query.data.reason && <p className="small muted mt-2">Last Change: {query.data.reason}</p>}
   </Disclosure>
  </>}
 </div>;
}
