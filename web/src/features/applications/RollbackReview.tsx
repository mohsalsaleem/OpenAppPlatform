import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { unwrap, api, type Application, type Deployment } from "../../api";
import { useCanOperate } from "../../access";
import { ErrorBox } from "../../components/Feedback";
import { DeploymentDialog } from "../../components/DeploymentDialog";

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
      api
        .GET("/api/v1/applications/{id}/rollback-plan", {
          params: {
            path: { id: application.id! },
            query: {
              releaseId: release.id,
              expectedVersion: application.version,
            },
          },
        })
        .then(unwrap),
  });
  const apply = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/applications/{id}/rollbacks", {
          params: { path: { id: application.id! } },
          body: {
            releaseId: plan.data!.releaseId,
            planHash: plan.data!.planHash,
            expectedVersion: plan.data!.definitionVersion,
            acknowledgeDataCompatibility: ack,
          },
          headers: { "Idempotency-Key": key },
        })
        .then(unwrap),
    onSuccess: () => {
      setOpen(false);
      client.invalidateQueries({ queryKey: ["deployments", application.id] });
      client.invalidateQueries({ queryKey: ["instances", application.id] });
      onQueued();
    },
  });
  return (
    <>
      <Button
        variant="outline"

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
        Review Rollback to {release.id.slice(0, 8)}
      </Button>
      {open && (
        <DeploymentDialog onClose={() => !apply.isPending && setOpen(false)}>
          <h2 id="deploy-title">Review Image Rollback</h2>
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
              <Label className="rollback-ack">
                <Checkbox
                  checked={ack}
                  onCheckedChange={(e) => setAck(e === true)}
                />
                I reviewed that this older image is compatible with the
                application’s current data and schema.
              </Label>
            </>
          )}
          <div className="form-actions">
            <Button
              variant="outline"

              disabled={apply.isPending}
              onClick={() => setOpen(false)}
            >
              Cancel
            </Button>
            <Button
              variant="default"

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
              {apply.isPending ? "Queuing…" : "Roll Back Images"}
            </Button>
          </div>
        </DeploymentDialog>
      )}
    </>
  );
}
