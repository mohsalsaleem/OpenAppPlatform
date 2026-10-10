import { DeploymentDialog } from "../../components/DeploymentDialog";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Deployment } from "../../api";
import { useCanOperate, useIsOwner } from "../../access";
import { ErrorBox } from "../../components/Feedback";

export function ReleaseControls({ deployment: d }: { deployment: Deployment }) {
  const canOperate = useCanOperate();
  const owner = useIsOwner();
  const client = useQueryClient();
  const [mode, setMode] = useState("");
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
    mutationFn: () =>
      api<Deployment>(
        `/deployments/${d.id}/${mode === "reconcile" ? "reconcile-abandoned" : "control"}`,
        {
          method: "POST",
          body: JSON.stringify(
            mode === "reconcile"
              ? {
                  expectedUpdatedAt: reviewedAt,
                  acknowledgeCurrentRuntime: ack,
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
                  acknowledgePreparedChanges: ack,
                  acknowledgeProviderMayContinue: ack,
                },
          ),
        },
      ),
    onSuccess: () => {
      setMode("");
      setAck(false);
      client.invalidateQueries({ queryKey: ["deployments", d.applicationId] });
    },
  });
  const review = (value: string) => {
    setMode(value);
    setReviewedAt(d.updatedAt);
    setAck(false);
    mutation.reset();
  };
  return (
    <div className="recovery-panel">
      {d.control && (
        <p className="small">
          {d.control.reason} ·{" "}
          {d.control.mode === "abandon"
            ? d.control.resolvedAt
              ? "Owner reconciled provider operations; tracking stays closed."
              : "Application fenced until owner reconciliation. Provider work may continue."
            : "Cancelled before dispatch. Prepared changes may remain."}
        </p>
      )}
      {canOperate && ["queued", "running", "attention"].includes(d.state) && (
        <div className="log-buttons">
          {d.steps.length > 0 && d.steps.every(preDispatch) && (
            <button className="secondary" onClick={() => review("cancel")}>
              Review cancellation
            </button>
          )}
          {owner && d.operation !== "scale-down" && (
            <button className="secondary" onClick={() => review("abandon")}>
              Review abandonment
            </button>
          )}
        </div>
      )}
      {owner && d.state === "abandoned" && !d.control?.resolvedAt && (
        <button className="secondary" onClick={() => review("reconcile")}>
          Review reconciliation
        </button>
      )}
      {mode && (
        <DeploymentDialog
          onClose={() => {
            if (!mutation.isPending) setMode("");
          }}
        >
          <h2 id="deploy-title">
            {mode === "reconcile"
              ? "Reconcile abandoned release"
              : mode === "cancel"
                ? "Cancel before dispatch"
                : "Abandon OAP tracking"}
          </h2>
          <p className="small muted">
            {mode === "reconcile"
              ? "Inspect the operator first. Every possible provider operation must be terminal. This releases the application fence and keeps unfinished steps closed."
              : "Prepared resources and configuration may remain. This does not undo changes or stop provider work."}
          </p>
          {mode !== "reconcile" && (
            <label>
              Reason
              <input
                maxLength={512}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </label>
          )}
          {mode === "reconcile" &&
            unresolved.map((s) => (
              <label key={`${s.component}:${s.ordinal}`}>
                {s.component} #{s.ordinal} exact provider operation ID
                <input
                  value={ids[`${s.component}:${s.ordinal}`] || ""}
                  onChange={(e) =>
                    setIds({
                      ...ids,
                      [`${s.component}:${s.ordinal}`]: e.target.value,
                    })
                  }
                />
              </label>
            ))}
          <label className="rollback-ack">
            <input
              type="checkbox"
              checked={ack}
              onChange={(e) => setAck(e.target.checked)}
            />
            {mode === "reconcile"
              ? "I inspected and accept the current runtime."
              : mode === "abandon"
                ? "I accept retained changes, continuing provider work and the application fence."
                : "I accept that prepared changes may remain."}
          </label>
          <ErrorBox error={mutation.error} />
          <div className="form-actions">
            <button
              className="secondary"
              disabled={mutation.isPending}
              onClick={() => setMode("")}
            >
              Close
            </button>{" "}
            <button
              className="primary"
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
            </button>
          </div>
        </DeploymentDialog>
      )}
    </div>
  );
}
