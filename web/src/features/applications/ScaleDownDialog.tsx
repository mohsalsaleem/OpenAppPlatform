import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Deployment } from "../../api";
import { DeploymentDialog } from "../../components/DeploymentDialog";
import { ErrorBox } from "../../components/Feedback";

export function ScaleDownDialog({
  applicationId,
  component,
  instances,
  version,
  onClose,
  onQueued,
}: {
  applicationId: string;
  component: string;
  instances: number;
  version: number;
  onClose: () => void;
  onQueued: () => void;
}) {
  const client = useQueryClient();
  const [wanted, setWanted] = useState(instances - 1);
  const scale = useMutation({
    mutationFn: () =>
      api<Deployment>(`/applications/${applicationId}/scale-down`, {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify({
          component,
          instances: wanted,
          expectedVersion: version,
        }),
      }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["deployments", applicationId] });
      client.invalidateQueries({ queryKey: ["instances", applicationId] });
      onQueued();
    },
  });
  const ordinals = Array.from(
    { length: instances - wanted },
    (_, i) => instances - i,
  );
  return (
    <DeploymentDialog onClose={onClose}>
      <h2 id="deploy-title">Scale Down {component}?</h2>
      <label htmlFor="retirement-count">Remaining Instances</label>
      <select
        id="retirement-count"
        value={wanted}
        disabled={scale.isPending}
        onChange={(e) => setWanted(+e.target.value)}
      >
        {Array.from({ length: instances - 1 }, (_, i) => i + 1).map((count) => (
          <option key={count} value={count}>
            {count}
          </option>
        ))}
      </select>
      <p>
        Retire{" "}
        {ordinals.map((ordinal) => `${component} / ${ordinal}`).join(", ")}.
        Remaining Instances keep their current configuration.
      </p>
      <p className="muted">
        Requests on stopped instances can be interrupted. Traffic draining is
        not available. Containers and volumes are retained. The desired replica
        count changes only after every selected instance is confirmed stopped.
      </p>
      <ErrorBox error={scale.error} />
      <div className="form-actions">
        <button
          className="secondary"
          disabled={scale.isPending}
          onClick={onClose}
        >
          Cancel
        </button>
        <button
          className="primary"
          disabled={scale.isPending}
          onClick={() => scale.mutate()}
        >
          {scale.isPending ? "Queuing…" : "Retire Instances"}
        </button>
      </div>
    </DeploymentDialog>
  );
}
