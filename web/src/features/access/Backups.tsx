import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "../../api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Disclosure } from "@/components/Disclosure";
import { ErrorBox, Loading } from "../../components/Feedback";
import { Status } from "../../components/Status";

const reasons: Record<string,string> = {
  "controller-running":"Controller must be stopped first.",
  "destination-exists":"Destination already exists; archive was preserved.",
  "checksum-mismatch":"Archive checksum or format did not match.",
  "invalid-restore-target":"Use a new isolated restore database.",
  interrupted:"Host operation was interrupted; inspect before retrying.",
  "operation-failed":"Inspect the host helper and retain the existing data.",
};
const commands = "(cd .local/install && docker compose stop platform)\nmkdir -p .local/install/backups\npython3 scripts/install.py backup --directory .local/install --archive .local/install/backups/metadata.dump\npython3 scripts/install.py restore-check --directory .local/install --archive .local/install/backups/metadata.dump --database oap_restore_verified";
export function Backups(){
 const query=useQuery({queryKey:["backup-reports"],queryFn:()=>api.GET("/api/v1/access/backups").then(unwrap),refetchInterval:30000});
 return <section className="panel">
  <div className="section-heading"><h2>Metadata Backups</h2><Button variant="outline" disabled={query.isFetching} onClick={()=>query.refetch()}>Refresh Reports</Button></div>
  <div className="flex flex-wrap gap-2 mb-3"><Badge variant="outline">Metadata Only</Badge><Badge variant="outline">Application Data Excluded</Badge><Badge variant="outline">Configuration &amp; Keys Saved Separately</Badge></div>
  <p className="small muted">Host-reported results. Archives stay on the host; these records do not confirm that a file is still available.</p>
  <ErrorBox error={query.error}/>
  {query.isPending ? <Loading/> : !query.data?.configured ? <p className="muted">Report feed is not configured. Use the installation’s read-only backup report mount.</p> : query.data.reports.length===0 && <p className="muted">No backup reports yet.</p>}
  {query.data && (query.data.invalidReports>0 || query.data.truncated) && <p role="alert" className="small">Some reports could not be loaded, or the feed exceeded its bounded scan. Inspect and archive older report files on the host.</p>}
  {query.data?.reports.map(report=><div className="step" key={report.id}>
   <div className="grow">
    <strong>{report.action==="backup" ? "Metadata Archive" : "Isolated Restore Check"}</strong>
    <p className="small muted">{new Date(report.updatedAt).toLocaleString()} · Host Helper</p>
    {report.status==="failed" && <p className="small">{reasons[report.errorCode]}</p>}
    {report.status==="running" && <p className="small">No terminal result recorded. Inspect the host before retrying.</p>}
    <Disclosure title={<>Report Details</>}>
      <p className="small">Report {report.id} · metadata only</p>
      {report.sha256 && <><code className="break-all">SHA256 {report.sha256}</code><p className="small">{report.sizeBytes.toLocaleString()} bytes</p></>}
    </Disclosure>
   </div>
   {report.status==="running" ? <Badge variant="outline">Result Pending</Badge> : <Status value={report.status==="failed" ? "failed" : "succeeded"}/>}
  </div>)}
  <Disclosure title={<>Backup &amp; Recovery Steps</>}>
   <p className="small muted">For packaged standalone installs, run the helper from the repository and Compose from the installation directory. Use a fresh archive path each time. Operator-managed installs use their trusted host backup workflow and the same report format.</p>
   <pre className="whitespace-pre-wrap break-words">{commands}</pre>
   <p className="small muted">Back up deployment configuration, secret values and any secret-store recovery keys separately. Metadata restore checks use a new database and do not start a controller or restore application databases or volumes.</p>
   <p className="small muted">Retention: keep at least two verified archives and an independent off-host copy. Remove older copies manually only after checking a newer isolated restore and recovery dependencies. OAP does not delete archives.</p>
  </Disclosure>
 </section>;
}
