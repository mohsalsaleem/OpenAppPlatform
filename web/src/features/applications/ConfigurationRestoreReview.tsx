import { Button } from "@/components/ui/button";
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { unwrap, api, type Application, type Deployment } from "../../api";
import { useCanOperate } from "../../access";
import { DeploymentDialog } from "../../components/DeploymentDialog";
import { ErrorBox } from "../../components/Feedback";

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
      api
        .GET("/api/v1/applications/{id}/configuration-restore-plan", {
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
  const save = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/applications/{id}/configuration-restores", {
          params: { path: { id: application.id! } },
          body: {
            releaseId: plan.data!.releaseId,
            expectedVersion: plan.data!.definitionVersion,
            planHash: plan.data!.planHash,
            acknowledgeEffects: ack,
          },
        })
        .then(unwrap),
    onSuccess: (app) => {
      setOpen(false);
      client.setQueryData(["application", application.id], app);
      client.invalidateQueries({ queryKey: ["application-groups"] });
      client.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  return (
    <>
      <Button
        variant="outline"

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
      </Button>
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
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Setting</TableHead>
                      <TableHead>Saved Now</TableHead>
                      <TableHead>Restore</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {plan.data.changes.map((c) => (
                      <TableRow key={`${c.component}-${c.field}`}>
                        <TableCell>
                          {c.component} · {c.field}
                        </TableCell>
                        <TableCell>{c.before}</TableCell>
                        <TableCell>{c.after}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <p className="small muted">
                Variable and endpoint values are hidden in this review.
              </p>
              <Label className="rollback-ack">
                <Checkbox
                  checked={ack}
                  onCheckedChange={(e) => setAck(e === true)}
                />
                I reviewed the settings and their effects. This saves
                configuration for a later deployment.
              </Label>
            </>
          )}
          <div className="form-actions">
            <Button
              variant="outline"

              disabled={save.isPending}
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
                !ack ||
                save.isPending
              }
              onClick={() => save.mutate()}
            >
              {save.isPending ? "Saving…" : "Restore Saved Configuration"}
            </Button>
          </div>
        </DeploymentDialog>
      )}
    </>
  );
}
