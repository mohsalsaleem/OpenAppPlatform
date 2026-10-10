import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Deployment } from "../../api";
import { Status } from "../../components/Status";
import { ErrorBox } from "../../components/Feedback";
import { useState } from "react";
import { DeploymentDialog } from "../../components/DeploymentDialog";
type Event = {
  id: string;
  commit: string;
  state: string;
  error?: string;
  deploymentId?: string;
  updatedAt: string;
  definitionVersion: number;
  binding: { repository: string; branch: string };
  builds: Record<
    string,
    {
      id: string;
      state: string;
      image?: string;
      provider?: {
        id: string;
        commit: string;
        state: string;
        resourceStatus: string;
      };
    }
  >;
};
export function SourceEvents({
  applicationId,
  version,
  releases,
}: {
  applicationId: string;
  version: number;
  releases: Deployment[];
}) {
  const client = useQueryClient();
  const [review, setReview] = useState<Event | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [approveCommit, setApproveCommit] = useState(false);
  const me = useQuery({
    queryKey: ["identity"],
    queryFn: () => api<{ role: string; kind: string }>("/auth/me"),
    retry: false,
  });
  const events = useQuery({
    queryKey: ["source-events", applicationId],
    queryFn: () => api<Event[]>(`/applications/${applicationId}/source-events`),
    refetchInterval: 5000,
  });
  const recover = useMutation({
    mutationFn: () =>
      api(`/source-events/${review!.id}/recover`, {
        method: "POST",
        body: JSON.stringify({
          expectedUpdatedAt: review!.updatedAt,
          expectedVersion: version,
          retryBuild: confirmed,
          approveSourceCommit: approveCommit,
        }),
      }),
    onSuccess: () => {
      setReview(null);
      client.invalidateQueries({ queryKey: ["source-events", applicationId] });
    },
  });
  if (!events.data?.length) return <ErrorBox error={events.error} />;
  return (
    <section className="panel">
      <div className="section-heading">
        <h2>GitHub Activity</h2>
        <span className="small muted">Pinned commits and build results</span>
      </div>
      {events.data.map((e) => (
        <article className="release" key={e.id}>
          <div className="section-heading">
            <div>
              <strong>
                {e.binding.repository} · {e.commit.slice(0, 8)}
              </strong>
              <p className="small muted">
                {e.binding.branch} · {new Date(e.updatedAt).toLocaleString()}
              </p>
            </div>
            <Status
              value={
                e.state === "released"
                  ? releases.find((r) => r.id === e.deploymentId)?.state ||
                    "released"
                  : e.state
              }
            />
          </div>
          {Object.entries(e.builds).map(([name, b]) => (
            <div className="step" key={name}>
              <div className="grow">
                <strong>{name}</strong>
                <details>
                  <summary>Build Details</summary>
                  <code className="image-ref">
                    {b.image ||
                      (b.provider?.id
                        ? `Provider deployment ${b.provider.id}`
                        : `Build ${b.id}`)}
                  </code>
                </details>
              </div>
              <Status value={b.state} />
            </div>
          ))}
          {e.error && <p className="error-inline">{e.error}</p>}
          {e.state === "attention" &&
            me.data?.role === "owner" &&
            me.data.kind === "session" && (
              <button
                className="secondary"
                onClick={() => {
                  setConfirmed(false);
                  setApproveCommit(false);
                  setReview(e);
                }}
              >
                Review Source Recovery
              </button>
            )}
        </article>
      ))}
      {review && (
        <DeploymentDialog onClose={() => setReview(null)}>
          <h2 id="deploy-title">Recover GitHub delivery?</h2>
          <p>
            This uses the current definition version {version}. Built digests
            are retained. Inspect any uncertain build IDs before authorizing
            another build.
          </p>
          <label>
            <input
              className="inline-checkbox"
              type="checkbox"
              checked={confirmed}
              onChange={(e) => setConfirmed(e.target.checked)}
            />{" "}
            I inspected the uncertain build and authorize a retry.
          </label>
          <label>
            <input
              className="inline-checkbox"
              type="checkbox"
              checked={approveCommit}
              onChange={(e) => setApproveCommit(e.target.checked)}
            />{" "}
            I reviewed the repository branch head and authorize this commit if
            it was superseded.
          </label>
          <ErrorBox error={recover.error} />
          <div className="form-actions">
            <button className="secondary" onClick={() => setReview(null)}>
              Cancel
            </button>
            <button
              className="primary"
              disabled={
                recover.isPending ||
                (!confirmed &&
                  Object.values(review.builds).some(
                    (b) => b.state === "building",
                  ))
              }
              onClick={() => recover.mutate()}
            >
              Recover Delivery
            </button>
          </div>
        </DeploymentDialog>
      )}
    </section>
  );
}
