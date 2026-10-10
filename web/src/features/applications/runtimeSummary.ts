import type { Component, Deployment, Instance } from "../../api";
export function releaseOperation(release: Deployment) {
  return release.steps.some((step) => step.rollbackFrom)
    ? "Rollback"
    : release.operation === "restart"
      ? "Restart"
      : release.operation === "scale-down"
        ? "Scale Down"
        : "Deployment";
}
export function replicaSummary(
  components: Component[],
  instances?: Instance[],
  failed = false,
) {
  if (failed) return "Health unavailable";
  if (!instances) return "Checking health…";
  const expected = components.reduce((total, c) => total + c.instances, 0);
  const active = instances.filter(
    (i) =>
      !i.retired &&
      components.some(
        (c) => c.name === i.component && i.ordinal <= c.instances,
      ),
  );
  if (!active.length) return "No running instances";
  const healthy = active.filter(
    (i) => i.status === "running:healthy" && !i.error,
  ).length;
  const running = active.filter(
    (i) => i.status.startsWith("running") && !i.error,
  ).length;
  return `${healthy}/${expected} healthy · ${running}/${expected} running`;
}

export function operatorName(value?: string) {
  return value === "coolify"
    ? "Coolify"
    : value === "docker"
      ? "Docker"
      : value || "the existing platform";
}
