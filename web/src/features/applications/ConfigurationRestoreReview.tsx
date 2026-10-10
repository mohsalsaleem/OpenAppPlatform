import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Application, type Deployment } from "../../api";
import { useCanOperate } from "../../access";
import { DeploymentDialog } from "../../components/DeploymentDialog";
import { ErrorBox } from "../../components/Feedback";
type Plan = {
  releaseId: string;
  definitionVersion: number;
  planHash: string;
  changes: {
    component: string;
    field: string;
    before: string;
    after: string;
  }[];
};
export function ConfigurationRestoreReview({
  application,
  release,
}: {
  application: Application;
  release: Deployment;
}) {
  const canOperate = useCanOperate(),
    client = useQueryClient();
  const [open, setOpen] = useState(false),
    [ack, setAck] = useState(false);
  const plan = useMutation({
    mutationFn: () =>
      api<Plan>(
        `/applications/${application.id}/configuration-restore-plan?releaseId=${release.id}&expectedVersion=${application.version}`,
      ),
  });
  const save = useMutation({
    mutationFn: () =>
      api<Application>(
        `/applications/${application.id}/configuration-restores`,
        {
          method: "POST",
          body: JSON.stringify({
            releaseId: plan.data!.releaseId,
            expectedVersion: plan.data!.definitionVersion,
            planHash: plan.data!.planHash,
            acknowledgeEffects: ack,
          }),
        },
      ),
    onSuccess: (app) => {
      setOpen(false);
      client.setQueryData(["application", application.id], app);
      client.invalidateQueries({ queryKey: ["application-groups"] });
      client.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  return (
    <>
      <button
        className="secondary"
        disabled={!canOperate}
        onClick={() => {
          setOpen(true);
          setAck(false);
          save.reset();
          plan.reset();
          plan.mutate();
        }}
      >
        Review Configuration from {release.id.slice(0, 8)}
      </button>
      {open && (
        <DeploymentDialog
          onClose={() => {
            if (!save.isPending) setOpen(false);
          }}
        >
          <h2 id="deploy-title">Restore Saved Configuration?</h2>
          <p>
            Use this release’s settings for your next deployment. Current saved
            images stay selected. Running instances, application data, database
            schema and GitHub triggers stay unchanged.
          </p>
          <p className="small muted">
            Review compatibility with the selected images and current data
            before deploying. Configuration restoration saves a new version;
            deployment is a separate review.
          </p>
          {plan.isPending && (
            <p role="status">Preparing configuration review…</p>
          )}
          <ErrorBox error={plan.error || save.error} />
          {plan.data && (
            <>
              <div className="table-responsive">
                <table>
                  <thead>
                    <tr>
                      <th>Setting</th>
                      <th>Saved Now</th>
                      <th>Restore</th>
                    </tr>
                  </thead>
                  <tbody>
                    {plan.data.changes.map((c) => (
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
              <p className="small muted">
                Variable and endpoint values are hidden in this review.
              </p>
              <label className="rollback-ack">
                <input
                  type="checkbox"
                  checked={ack}
                  onChange={(e) => setAck(e.target.checked)}
                />
                I reviewed the settings and their effects. This saves
                configuration for a later deployment.
              </label>
            </>
          )}
          <div className="form-actions">
            <button
              className="secondary"
              disabled={save.isPending}
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
                !ack ||
                save.isPending
              }
              onClick={() => save.mutate()}
            >
              {save.isPending ? "Saving…" : "Restore Saved Configuration"}
            </button>
          </div>
        </DeploymentDialog>
      )}
    </>
  );
}
