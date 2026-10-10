import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Application, type Deployment } from "../../api";
import { useCanOperate } from "../../access";
import { ErrorBox } from "../../components/Feedback";
import { DeploymentDialog } from "../../components/DeploymentDialog";

type Plan = {
  releaseId: string;
  definitionVersion: number;
  planHash: string;
  artifactAvailability: string;
  instances: {
    component: string;
    ordinal: number;
    currentImage: string;
    restoreImage: string;
  }[];
};
export function RollbackReview({
  application,
  release,
  onQueued,
}: {
  application: Application;
  release: Deployment;
  onQueued: () => void;
}) {
  const canOperate = useCanOperate(),
    client = useQueryClient();
  const [open, setOpen] = useState(false),
    [ack, setAck] = useState(false);
  const [key, setKey] = useState("");
  const plan = useMutation({
    mutationFn: () =>
      api<Plan>(
        `/applications/${application.id}/rollback-plan?releaseId=${release.id}&expectedVersion=${application.version}`,
      ),
  });
  const apply = useMutation({
    mutationFn: () =>
      api<Deployment>(`/applications/${application.id}/rollbacks`, {
        method: "POST",
        headers: { "Idempotency-Key": key },
        body: JSON.stringify({
          releaseId: plan.data!.releaseId,
          planHash: plan.data!.planHash,
          expectedVersion: plan.data!.definitionVersion,
          acknowledgeDataCompatibility: ack,
        }),
      }),
    onSuccess: () => {
      setOpen(false);
      client.invalidateQueries({ queryKey: ["deployments", application.id] });
      client.invalidateQueries({ queryKey: ["instances", application.id] });
      onQueued();
    },
  });
  return (
    <>
      <button
        className="secondary"
        disabled={!canOperate || apply.isPending}
        onClick={() => {
          setOpen(true);
          setAck(false);
          setKey(crypto.randomUUID());
          apply.reset();
          plan.reset();
          plan.mutate();
        }}
      >
        Review rollback to {release.id.slice(0, 8)}
      </button>
      {open && (
        <DeploymentDialog onClose={() => !apply.isPending && setOpen(false)}>
          <h2 id="deploy-title">Review image rollback</h2>
          <p>
            Creates a new standard release using this snapshot’s immutable
            images. Existing configuration and topology must match. Requests may
            be interrupted. Databases, volumes and desired configuration are not
            restored.
          </p>
          <ErrorBox error={plan.error || apply.error} />
          {plan.isPending && (
            <p>
              Checking recorded verification, current bindings and operator
              ownership…
            </p>
          )}
          {plan.data && (
            <>
              {plan.data.instances.map((i) => (
                <div className="step" key={`${i.component}-${i.ordinal}`}>
                  <div>
                    <strong>
                      {i.component} / {i.ordinal}
                    </strong>
                    <p className="small muted">
                      From <code className="image-ref">{i.currentImage}</code>
                    </p>
                    <p className="small muted">
                      Restore{" "}
                      <code className="image-ref">{i.restoreImage}</code>
                    </p>
                  </div>
                </div>
              ))}
              <p className="small muted">{plan.data.artifactAvailability}</p>
              <label className="rollback-ack">
                <input
                  type="checkbox"
                  checked={ack}
                  onChange={(e) => setAck(e.target.checked)}
                />
                I reviewed that this older image is compatible with the
                application’s current data and schema.
              </label>
            </>
          )}
          <div className="form-actions">
            <button
              className="secondary"
              disabled={apply.isPending}
              onClick={() => setOpen(false)}
            >
              Cancel
            </button>
            <button
              className="primary"
              disabled={
                !canOperate ||
                !plan.data ||
                !!plan.error ||
                plan.isPending ||
                !ack ||
                apply.isPending
              }
              onClick={() => apply.mutate()}
            >
              {apply.isPending ? "Queuing…" : "Roll back images"}
            </button>
          </div>
        </DeploymentDialog>
      )}
    </>
  );
}
