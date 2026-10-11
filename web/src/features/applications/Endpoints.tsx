import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpRight } from "lucide-react";
import { api, unwrap } from "@/api";
import type { components } from "@/generated/api";
import { useIsOwner } from "@/access";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { ErrorBox, Loading } from "@/components/Feedback";
type Plan = components["schemas"]["RoutePlan"];
export function Endpoints({
  applicationId,
  version,
}: {
  applicationId: string;
  version: number;
}) {
  const owner = useIsOwner();
  const client = useQueryClient();
  const [plan, setPlan] = useState<Plan | null>(null);
  const routes = useQuery({
    queryKey: ["routes", applicationId],
    queryFn: () =>
      api
        .GET("/api/v1/applications/{id}/routes", {
          params: { path: { id: applicationId } },
        })
        .then(unwrap),
    refetchInterval: 10000,
  });
  useEffect(() => {
    const saved = routes.data?.publications.find((p) => p.id === plan?.id);
    if (saved && saved.state !== plan?.state) setPlan(saved);
  }, [routes.data, plan]);
  const review = useMutation({
    mutationFn: (name: string) =>
      api
        .POST("/api/v1/applications/{id}/route-reviews", {
          params: { path: { id: applicationId } },
          body: { name, expectedVersion: version },
        })
        .then(unwrap),
    onSuccess: (value) => {
      publish.reset();
      setPlan(value);
      client.invalidateQueries({ queryKey: ["routes", applicationId] });
    },
  });
  const publish = useMutation({
    mutationFn: ({ id, action }: { id: string; action: "apply" | "cancel" }) =>
      api
        .POST("/api/v1/applications/{id}/route-publications/{plan}", {
          params: { path: { id: applicationId, plan: id } },
          body: { action },
        })
        .then(unwrap),
    onSuccess: () => {
      setPlan(null);
      client.invalidateQueries({ queryKey: ["routes", applicationId] });
    },
    onError: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: ["routes", applicationId] }),
        client.invalidateQueries({ queryKey: ["application", applicationId] }),
      ]);
    },
  });
  if (routes.isLoading) return <Loading />;
  const uncertain = routes.data?.publications.find(
    (p) => p.state === "publishing",
  );
  return (
    <section className="panel">
      <div className="section-heading">
        <div>
          <h2>Endpoints</h2>
          <p className="muted">
            Stable addresses shared by a component’s instances.
          </p>
        </div>
      </div>
      <ErrorBox error={routes.error || review.error} />
      {uncertain && (
        <div role="alert" className="notice">
          A routing update needs recovery. Deployments and configuration changes
          are paused until its outcome is confirmed.{" "}
          {owner && (
            <Button
              variant="outline"
              onClick={() => {
                publish.reset();
                setPlan(uncertain);
              }}
            >
              Review Recovery
            </Button>
          )}
        </div>
      )}
      {!routes.data?.endpoints.length && (
        <p className="muted">
          No shared endpoints are configured. Existing instance addresses remain
          available in Overview.
        </p>
      )}
      {routes.data?.endpoints.map((endpoint) => (
        <div
          className="flex items-start justify-between gap-4 border-b py-4 last:border-0"
          key={endpoint.name}
        >
          <div className="min-w-0">
            <strong>{endpoint.component}</strong>
            <p>
              <a
                className="inline-flex items-center gap-1 break-all"
                href={endpoint.url}
                target="_blank"
                rel="noreferrer"
              >
                {endpoint.url}
                <ArrowUpRight size={14} />
              </a>
            </p>
            <p className="muted">
              {endpoint.status === "in-sync"
                ? `${endpoint.current.length} backends · Membership Current`
                : endpoint.status === "update-needed"
                  ? `${endpoint.current.length} current → ${endpoint.desired.length} desired backends · Update Available`
                  : "Review Blocked"}
            </p>
            {endpoint.message && (
              <p className="text-sm text-destructive">{endpoint.message}</p>
            )}
          </div>
          {owner && (
            <Button
              variant="outline"
              disabled={
                endpoint.status === "blocked" || review.isPending || !!uncertain
              }
              onClick={() => review.mutate(endpoint.name)}
            >
              Review Routing
            </Button>
          )}
        </div>
      ))}
      {!!routes.data?.publications.length && (
        <div className="mt-5">
          <h3>Recent Routing Reviews</h3>
          {routes.data.publications.slice(0, 5).map((p) => (
            <div
              key={p.id}
              className="flex items-center justify-between gap-4 py-2 text-sm"
            >
              <span>
                {p.component} ·{" "}
                {p.state === "applied"
                  ? "Applied"
                  : p.state === "publishing"
                    ? "Needs Recovery"
                    : p.state === "cancelled"
                      ? "Cancelled"
                      : "Ready for Review"}
              </span>
              {owner && p.state === "prepared" && !uncertain && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    publish.reset();
                    setPlan(p);
                  }}
                >
                  Open Review
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      <Dialog
        open={!!plan}
        onOpenChange={(open) => {
          if (!open && !publish.isPending) setPlan(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {plan?.state === "publishing"
                ? "Recover Routing Update"
                : "Review Routing Update"}
            </DialogTitle>
            <DialogDescription>
              Apply only the reviewed backend membership. The endpoint address
              and TLS settings stay the same.
            </DialogDescription>
          </DialogHeader>
          {plan && (
            <>
              <p className="text-sm break-all">{plan.url}</p>
              <div className="grid grid-cols-2 gap-4 text-sm">
                <div>
                  <h3>Add Backends</h3>
                  {plan.added.length ? (
                    plan.added.map((u) => (
                      <p className="break-all" key={u}>
                        {u}
                      </p>
                    ))
                  ) : (
                    <p className="muted">None</p>
                  )}
                </div>
                <div>
                  <h3>Remove Backends</h3>
                  {plan.removed.length ? (
                    plan.removed.map((u) => (
                      <p className="break-all" key={u}>
                        {u}
                      </p>
                    ))
                  ) : (
                    <p className="muted">None</p>
                  )}
                </div>
              </div>
              <p className="muted text-sm">
                {plan.members.filter((m) => !m.retired).length} healthy backends
                · Application version {plan.expectedVersion}. Ownership and
                native configuration are checked again before applying.
              </p>
              <p className="muted text-sm">
                Native health checks continue independently of OAP. Connection
                draining is not available yet.
              </p>
              {plan.state === "prepared" &&
                plan.expectedVersion !== version && (
                  <p className="text-sm text-destructive" role="alert">
                    This review is outdated. Discard it and review the current
                    application.
                  </p>
                )}
              <ErrorBox error={publish.error} />
              <DialogFooter>
                <Button
                  variant="outline"
                  disabled={publish.isPending}
                  onClick={() =>
                    publish.mutate({ id: plan.id, action: "cancel" })
                  }
                >
                  {plan.state === "publishing"
                    ? "Cancel If Unchanged"
                    : "Discard Review"}
                </Button>
                <Button
                  disabled={publish.isPending}
                  onClick={() =>
                    publish.mutate({ id: plan.id, action: "apply" })
                  }
                >
                  {publish.isPending
                    ? "Checking Routing…"
                    : plan.state === "publishing"
                      ? "Resume Update"
                      : "Apply Routing"}
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </section>
  );
}
