import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, type Deployment } from "../../api";
import { ErrorBox } from "../../components/Feedback";

type Summary = {
  id: string;
  definitionVersion: number;
  state: string;
  operation: string;
  commit?: string;
  artifacts: { component: string; image: string; immutable: boolean }[];
};
type Comparison = {
  from: Summary;
  to: Summary;
  changes: {
    component: string;
    field: string;
    before: string;
    after: string;
  }[];
};

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
      api<Comparison>(
        `/applications/${applicationId}/release-comparison?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
      ),
  });
  const fromId = from || releases[1]?.id || "",
    toId = to || releases[0]?.id || "";
  return (
    <section className="panel" aria-label="Release comparison">
      <h2>Compare releases</h2>
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
              <label htmlFor="compare-from">Earlier release</label>
              <select
                id="compare-from"
                value={fromId}
                onChange={(e) => {
                  setFrom(e.target.value);
                  comparison.reset();
                }}
              >
                {releases.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.id.slice(0, 8)} · {r.operation || "deploy"} · v
                    {r.definitionVersion}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label htmlFor="compare-to">Later release</label>
              <select
                id="compare-to"
                value={toId}
                onChange={(e) => {
                  setTo(e.target.value);
                  comparison.reset();
                }}
              >
                {releases.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.id.slice(0, 8)} · {r.operation || "deploy"} · v
                    {r.definitionVersion}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <button
            className="secondary"
            disabled={
              !fromId || !toId || fromId === toId || comparison.isPending
            }
            onClick={() => comparison.mutate({ from: fromId, to: toId })}
          >
            {comparison.isPending ? "Comparing…" : "Compare selected releases"}
          </button>
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
                    {index === 0 ? "Earlier" : "Later"} ·{" "}
                    {release.id.slice(0, 8)}
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
              <table>
                <thead>
                  <tr>
                    <th>Field</th>
                    <th>Earlier</th>
                    <th>Later</th>
                  </tr>
                </thead>
                <tbody>
                  {comparison.data.changes.map((c) => (
                    <tr key={`${c.component}-${c.field}`}>
                      <td>
                        {c.component} · {c.field}
                      </td>
                      <td>{c.before}</td>
                      <td>{c.after}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
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
