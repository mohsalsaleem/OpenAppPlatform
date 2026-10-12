import { Disclosure } from "@/components/Disclosure";
import type { ReadResponse } from "../../api";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { unwrap, api, type Deployment } from "../../api";
import { Status } from "../../components/Status";
import { ErrorBox } from "../../components/Feedback";
import { useState } from "react";
import { DeploymentDialog } from "../../components/DeploymentDialog";
type Event = ReadResponse<"/api/v1/applications/{id}/source-events">[number];
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
    queryFn: () => api.GET("/api/v1/auth/me").then(unwrap),
    retry: false,
  });
  const events = useQuery({
    queryKey: ["source-events", applicationId],
    queryFn: () =>
      api
        .GET("/api/v1/applications/{id}/source-events", {
          params: { path: { id: applicationId! } },
        })
        .then(unwrap),
    refetchInterval: 5000,
  });
  const recover = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/source-events/{id}/recover", {
          params: { path: { id: review!.id! } },
          body: {
            expectedUpdatedAt: review!.updatedAt,
            expectedVersion: version,
            retryBuild: confirmed,
            approveSourceCommit: approveCommit,
          },
        })
        .then(unwrap),
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
                <Disclosure title={<>Build Details</>}>
                  <code className="image-ref">
                    {b.image ||
                      (b.provider?.id
                        ? `Provider deployment ${b.provider.id}`
                        : `Build ${b.id}`)}
                  </code>
                </Disclosure>
              </div>
              <Status value={b.state} />
            </div>
          ))}
          {e.error && <p className="error-inline">{e.error}</p>}
          {e.state === "attention" &&
            me.data?.role === "owner" &&
            me.data.kind === "session" && (
              <Button
                variant="outline"

                onClick={() => {
                  setConfirmed(false);
                  setApproveCommit(false);
                  setReview(e);
                }}
              >
                Review Source Recovery
              </Button>
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
          <Label>
            <Checkbox
              className="inline-checkbox"

              checked={confirmed}
              onCheckedChange={(e) => setConfirmed(e === true)}
            />{" "}
            I inspected the uncertain build and authorize a retry.
          </Label>
          <Label>
            <Checkbox
              className="inline-checkbox"

              checked={approveCommit}
              onCheckedChange={(e) => setApproveCommit(e === true)}
            />{" "}
            I reviewed the repository branch head and authorize this commit if
            it was superseded.
          </Label>
          <ErrorBox error={recover.error} />
          <div className="form-actions">
            <Button variant="outline" onClick={() => setReview(null)}>
              Cancel
            </Button>
            <Button
              variant="default"

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
            </Button>
          </div>
        </DeploymentDialog>
      )}
    </section>
  );
}
