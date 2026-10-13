import { Disclosure } from "@/components/Disclosure";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import type { Component } from "../../api";
export function ContainerSecurity({component,supported,owner,onChange}:{component:Component;supported:boolean;owner:boolean;onChange:(value:Component["security"])=>void}){
 const inherited=!!component.resourceId || component.management==="observe" || !supported;
 const value=component.security;
 return <Disclosure title={<>Container Security</>}>
  <Badge variant="outline">{inherited ? "Managed in Operator" : value?.allowPrivilegeEscalation ? "Owner Exception" : "No New Privileges"}</Badge>
  <p className="small muted">{inherited ? "Security configuration is inherited. Review or change it in the operator." : "New OAP-owned Docker containers use no-new-privileges, isolated application networking and named volumes. Image users are preserved; this does not force every image to run as non-root. OAP does not prune active, rollback or retained images."}</p>
  {!inherited && owner && <div className="space-y-3 mt-3">
   <div className="flex items-center gap-2"><Checkbox id={`security-${component.name}`} checked={!!value?.allowPrivilegeEscalation} onCheckedChange={checked=>onChange(checked===true ? {allowPrivilegeEscalation:true,exceptionReason:""}:undefined)}/><Label htmlFor={`security-${component.name}`}>Allow Privilege Escalation</Label></div>
   {value?.allowPrivilegeEscalation && <div><Label htmlFor={`security-reason-${component.name}`}>Exception Reason</Label><Input id={`security-reason-${component.name}`} value={value.exceptionReason??""} minLength={12} maxLength={240} required onChange={event=>onChange({...value,exceptionReason:event.target.value})}/></div>}
   <p className="small muted">Human owner review is required. Keep secrets out of the reason; it is saved with the definition. The exception permits setuid/file-capability escalation, not privileged containers or arbitrary host mounts.</p>
  </div>}
 </Disclosure>;
}
