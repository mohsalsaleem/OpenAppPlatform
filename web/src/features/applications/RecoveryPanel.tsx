import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Deployment } from "../../api";
import { ErrorBox } from "../../components/Feedback";
export function RecoveryPanel({ deployment }: { deployment: Deployment }) {
  const client = useQueryClient();
  const [ids, setIds] = useState<Record<string, string>>({});
  const recovery = useMutation({
    mutationFn: ({
      component,
      ordinal,
      remoteDeploymentId,
      retryPreparation,
      retryFinalization,
    }: {
      component?: string;
      ordinal?: number;
      remoteDeploymentId?: string;
      retryPreparation?: boolean;
      retryFinalization?: boolean;
    }) =>
      api<Deployment>(`/deployments/${deployment.id}/recover`, {
        method: "POST",
        body: JSON.stringify({
          component,
          ordinal,
          remoteDeploymentId,
          retryPreparation,
          retryFinalization,
          expectedUpdatedAt: deployment.updatedAt,
        }),
      }),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: ["deployments", deployment.applicationId],
      });
      client.invalidateQueries({
        queryKey: ["instances", deployment.applicationId],
      });
    },
  });
  return (
    <div className="recovery-panel">
      <h3>Recovery controls</h3>
      <p className="small muted">
        Resume a known pre-dispatch phase after preparation or dependency
        checks. After dispatch starts, recheck the exact provider deployment.
        This does not dispatch the operation again. If its ID was lost, inspect
        the operator and enter the ID belonging to this instance. Other
        unfinished instances resume only after every attention state is
        resolved.
      </p>
      <ErrorBox error={recovery.error} />
      {deployment.operation === "scale-down" &&
        deployment.steps.every((s) => s.phase === "succeeded") && (
          <button
            className="secondary"
            disabled={recovery.isPending}
            onClick={() => recovery.mutate({ retryFinalization: true })}
          >
            Retry saving replica count
          </button>
        )}
      {deployment.steps
        .filter((s) => s.phase === "attention")
        .map((s) => {
          const key = `${s.component}-${s.ordinal}`;
          const remote = s.remoteDeploymentId || ids[key] || "";
          if (
            ["pending", "prepared"].includes(s.recoveryPhase || "") &&
            !s.remoteDeploymentId
          )
            return (
              <div className="recovery-step" key={key}>
                <p>
                  {s.component} / {s.ordinal}: paused before dispatch.
                </p>
                <button
                  className="secondary"
                  disabled={recovery.isPending}
                  onClick={() =>
                    recovery.mutate({
                      component: s.component,
                      ordinal: s.ordinal,
                      retryPreparation: true,
                    })
                  }
                >
                  {s.recoveryPhase === "prepared"
                    ? "Recheck dependencies before dispatch"
                    : "Retry preparation"}
                </button>
              </div>
            );
          return (
            <div className="recovery-step" key={key}>
              <label htmlFor={`recovery-${deployment.id}-${key}`}>
                Provider deployment ID for {s.component} / {s.ordinal}
              </label>
              <input
                id={`recovery-${deployment.id}-${key}`}
                value={remote}
                readOnly={!!s.remoteDeploymentId}
                maxLength={256}
                onChange={(e) => setIds({ ...ids, [key]: e.target.value })}
              />
              <button
                className="secondary"
                disabled={recovery.isPending || !remote}
                onClick={() =>
                  recovery.mutate({
                    component: s.component,
                    ordinal: s.ordinal,
                    remoteDeploymentId: remote,
                  })
                }
              >
                {recovery.isPending
                  ? "Checking…"
                  : "Recheck provider deployment"}
              </button>
            </div>
          );
        })}
    </div>
  );
}
