import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { releaseOperation } from "./runtimeSummary";
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { unwrap, api, type Deployment } from "../../api";
import { ErrorBox } from "../../components/Feedback";

export function ReleaseComparison({
  applicationId,
  releases,
}: {
  applicationId: string;
  releases: Deployment[];
}) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const comparison = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) =>
      api
        .GET("/api/v1/applications/{id}/release-comparison", {
          params: {
            path: { id: applicationId! },
            query: {
              from: from,
              to: to,
            },
          },
        })
        .then(unwrap),
  });
  const fromId = from || releases[1]?.id || "",
    toId = to || releases[0]?.id || "";
  return (
    <section
      className="panel release-comparison"
      aria-label="Release Comparison"
    >
      <h2>Compare Releases</h2>
      <p className="small muted">
        Compare frozen requested definitions and image references. This does not
        inspect current runtime, validate rollback compatibility, or restore
        application data.
      </p>
      {releases.length < 2 ? (
        <p className="muted">
          Two recorded releases are needed for comparison.
        </p>
      ) : (
        <>
          <div className="form-grid compact">
            <div>
              <Label htmlFor="compare-from">From Release</Label>
              <NativeSelect
                id="compare-from"
                value={fromId}
                onChange={(e) => {
                  setFrom(e.target.value);
                  comparison.reset();
                }}
              >
                {releases.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.id.slice(0, 8)} · {releaseOperation(r)} · v
                    {r.definitionVersion}
                  </option>
                ))}
              </NativeSelect>
            </div>
            <div>
              <Label htmlFor="compare-to">To Release</Label>
              <NativeSelect
                id="compare-to"
                value={toId}
                onChange={(e) => {
                  setTo(e.target.value);
                  comparison.reset();
                }}
              >
                {releases.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.id.slice(0, 8)} · {releaseOperation(r)} · v
                    {r.definitionVersion}
                  </option>
                ))}
              </NativeSelect>
            </div>
          </div>
          <Button
            variant="outline"

            disabled={
              !fromId || !toId || fromId === toId || comparison.isPending
            }
            onClick={() => {
              setFrom(fromId);
              setTo(toId);
              comparison.mutate({ from: fromId, to: toId });
            }}
          >
            {comparison.isPending ? "Comparing…" : "Compare Selected Releases"}
          </Button>
        </>
      )}
      <ErrorBox error={comparison.error} />
      {comparison.data && (
        <>
          <div className="form-grid compact">
            {[comparison.data.from, comparison.data.to].map(
              (release, index) => (
                <div key={`${index}-${release.id}`}>
                  <h3>
                    {index === 0 ? "From" : "To"} · {release.id.slice(0, 8)}
                  </h3>
                  <p className="small muted">
                    {release.operation} · definition v
                    {release.definitionVersion} · {release.state}
                    {release.commit
                      ? ` · commit ${release.commit.slice(0, 8)}`
                      : ""}
                  </p>
                  {release.artifacts.map((a) => (
                    <div key={a.component}>
                      <strong className="small">{a.component}</strong>
                      <code className="image-ref">
                        {a.image || "No image recorded"}
                      </code>
                      <p className="small muted">
                        {a.immutable
                          ? "Digest reference recorded"
                          : "Mutable or missing reference; reproducibility is not established"}
                      </p>
                    </div>
                  ))}
                </div>
              ),
            )}
          </div>
          {comparison.data.changes.length ? (
            <div className="table-responsive">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Field</TableHead>
                    <TableHead>From</TableHead>
                    <TableHead>To</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {comparison.data.changes.map((c) => (
                    <TableRow key={`${c.component}-${c.field}`}>
                      <TableCell>
                        {c.component} · {c.field}
                      </TableCell>
                      <TableCell>{c.before}</TableCell>
                      <TableCell>{c.after}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <p role="status">
              No frozen component configuration or selection changes.
            </p>
          )}
        </>
      )}
    </section>
  );
}
