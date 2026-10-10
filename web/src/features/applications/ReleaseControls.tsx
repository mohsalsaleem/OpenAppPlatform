import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { DeploymentDialog } from "../../components/DeploymentDialog";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { unwrap, api, type Deployment } from "../../api";
import { useCanOperate, useIsOwner } from "../../access";
import { ErrorBox } from "../../components/Feedback";

export function ReleaseControls({ deployment: d }: { deployment: Deployment }) {
  const canOperate = useCanOperate();
  const owner = useIsOwner();
  const client = useQueryClient();
  const [mode, setMode] = useState<"" | "cancel" | "abandon" | "reconcile">("");
  const [reason, setReason] = useState("");
  const [reviewedAt, setReviewedAt] = useState("");
  const [ack, setAck] = useState(false);
  const [ids, setIds] = useState<Record<string, string>>({});
  const preDispatch = (s: Deployment["steps"][number]) =>
    !s.remoteDeploymentId &&
    ["pending", "prepared"].includes(
      s.phase === "attention" ? s.recoveryPhase || "" : s.phase,
    );
  const unresolved = d.steps.filter(
    (s) => !preDispatch(s) && !s.remoteDeploymentId,
  );
  const mutation = useMutation({
    mutationFn: () => {
      if (!ack || !mode)
        throw new Error("Review and acknowledge the effects first");
      return api
        .POST(
          mode === "reconcile"
            ? "/api/v1/deployments/{id}/reconcile-abandoned"
            : "/api/v1/deployments/{id}/control",
          {
            params: { path: { id: d.id! } },
            body:
              mode === "reconcile"
                ? {
                    expectedUpdatedAt: reviewedAt,
                    acknowledgeCurrentRuntime: true,
                    operations: unresolved.map((s) => ({
                      component: s.component,
                      ordinal: s.ordinal,
                      remoteDeploymentId:
                        ids[`${s.component}:${s.ordinal}`] || "",
                    })),
                  }
                : {
                    mode,
                    reason,
                    expectedUpdatedAt: reviewedAt,
                    acknowledgePreparedChanges: true,
                    acknowledgeProviderMayContinue: ack,
                  },
          },
        )
        .then(unwrap);
    },
    onSuccess: () => {
      setMode("");
      setAck(false);
      client.invalidateQueries({ queryKey: ["deployments", d.applicationId] });
    },
  });
  const review = (value: "cancel" | "abandon" | "reconcile") => {
    setMode(value);
    setReviewedAt(d.updatedAt);
    setAck(false);
    mutation.reset();
  };
  const actionable =
    canOperate &&
    ["queued", "running", "attention"].includes(d.state) &&
    ((d.steps.length > 0 && d.steps.every(preDispatch)) ||
      (owner && d.operation !== "scale-down"));
  if (!d.control && !actionable && !(owner && d.state === "abandoned"))
    return null;
  return (
    <div className="recovery-panel">
      {d.control && (
        <p className="small">
          {d.control.reason} ·{" "}
          {d.control.mode === "abandon"
            ? d.control.resolvedAt
              ? "Owner reconciled provider operations; tracking stays closed."
              : "Selected components remain protected until owner reconciliation. Provider work may continue."
            : "Cancelled before dispatch. Prepared changes may remain."}
        </p>
      )}
      {canOperate && ["queued", "running", "attention"].includes(d.state) && (
        <div className="log-buttons">
          {d.steps.length > 0 && d.steps.every(preDispatch) && (
            <Button variant="outline" onClick={() => review("cancel")}>
              Review Cancellation
            </Button>
          )}
          {owner && d.operation !== "scale-down" && (
            <Button variant="outline" onClick={() => review("abandon")}>
              Review Abandonment
            </Button>
          )}
        </div>
      )}
      {owner && d.state === "abandoned" && !d.control?.resolvedAt && (
        <Button variant="outline" onClick={() => review("reconcile")}>
          Review Reconciliation
        </Button>
      )}
      {mode && (
        <DeploymentDialog
          onClose={() => {
            setMode("");
          }}
        >
          <h2 id="deploy-title">
            {mode === "reconcile"
              ? "Reconcile Abandoned Release"
              : mode === "cancel"
                ? "Cancel Before Dispatch"
                : "Close Deployment Tracking"}
          </h2>
          <p className="small muted">
            {mode === "reconcile"
              ? "Inspect the operator first. Every possible provider operation must be terminal. This releases the component and dependency guards and keeps unfinished steps closed."
              : "Prepared resources and configuration may remain. This does not undo changes or stop provider work."}
          </p>
          {mode !== "reconcile" && (
            <Label>
              Reason
              <Input
                maxLength={512}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </Label>
          )}
          {mode === "reconcile" &&
            unresolved.map((s) => (
              <Label key={`${s.component}:${s.ordinal}`}>
                {s.component} #{s.ordinal} exact provider operation ID
                <Input
                  value={ids[`${s.component}:${s.ordinal}`] || ""}
                  onChange={(e) =>
                    setIds({
                      ...ids,
                      [`${s.component}:${s.ordinal}`]: e.target.value,
                    })
                  }
                />
              </Label>
            ))}
          <Label className="rollback-ack">
            <Checkbox
              checked={ack}
              onCheckedChange={(e) => setAck(e === true)}
            />
            {mode === "reconcile"
              ? "I inspected and accept the current runtime."
              : mode === "abandon"
                ? "I accept retained changes, continuing provider work and the component and dependency guards."
                : "I accept that prepared changes may remain."}
          </Label>
          <ErrorBox error={mutation.error} />
          <div className="form-actions">
            <Button
              variant="outline"

              disabled={mutation.isPending}
              onClick={() => setMode("")}
            >
              Close
            </Button>{" "}
            <Button
              variant="default"

              disabled={
                mutation.isPending ||
                !ack ||
                (mode !== "reconcile" && !reason.trim()) ||
                (mode === "reconcile" &&
                  unresolved.some(
                    (s) => !ids[`${s.component}:${s.ordinal}`]?.trim(),
                  ))
              }
              onClick={() => mutation.mutate()}
            >
              Confirm{" "}
              {mode === "reconcile"
                ? "reconciliation"
                : mode === "cancel"
                  ? "cancellation"
                  : "abandonment"}
            </Button>
          </div>
        </DeploymentDialog>
      )}
    </div>
  );
}
