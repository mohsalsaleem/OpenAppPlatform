import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { unwrap, api, type Deployment } from "../../api";
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
      api
        .POST("/api/v1/deployments/{id}/recover", {
          params: { path: { id: deployment.id! } },
          body: {
            component,
            ordinal,
            remoteDeploymentId,
            retryPreparation,
            retryFinalization,
            expectedUpdatedAt: deployment.updatedAt,
          },
        })
        .then(unwrap),
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
      <h3>Recovery Controls</h3>
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
          <Button
            variant="outline"

            disabled={recovery.isPending}
            onClick={() => recovery.mutate({ retryFinalization: true })}
          >
            Retry Saving Replica Count
          </Button>
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
                <Button
                  variant="outline"

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
                </Button>
              </div>
            );
          return (
            <div className="recovery-step" key={key}>
              <Label htmlFor={`recovery-${deployment.id}-${key}`}>
                Provider Deployment ID for {s.component} / {s.ordinal}
              </Label>
              <Input
                id={`recovery-${deployment.id}-${key}`}
                value={remote}
                readOnly={!!s.remoteDeploymentId}
                maxLength={256}
                onChange={(e) => setIds({ ...ids, [key]: e.target.value })}
              />
              <Button
                variant="outline"

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
              </Button>
            </div>
          );
        })}
    </div>
  );
}
