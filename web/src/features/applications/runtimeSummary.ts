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

/** Advisory UI availability; the server rechecks persisted claims at admission. */
export function blockedComponents(
  components: Component[],
  releases: Deployment[],
) {
  const claims = (definitions: Component[], selected: string[]) => {
    const out = new Map<string, "read" | "write">(
      selected.map((name) => [name, "write"]),
    );
    const visited = new Set<string>();
    const visit = (name: string) => {
      if (visited.has(name)) return;
      visited.add(name);
      const component = definitions.find((value) => value.name === name);
      for (const dependency of [
        ...(component?.dependsOn || []),
        ...Object.values(component?.services || {}),
      ]) {
        if (!out.has(dependency)) out.set(dependency, "read");
        visit(dependency);
      }
    };
    selected.forEach(visit);
    return out;
  };
  const active = releases.filter(
    (release) =>
      ["queued", "running", "attention"].includes(release.state) ||
      (release.state === "abandoned" && !release.control?.resolvedAt),
  );
  const existing = active.map((release) =>
    claims(
      release.manifest.components,
      release.steps.length
        ? release.steps.map((step) => step.component)
        : release.manifest.components.map((component) => component.name),
    ),
  );
  return new Set(
    components
      .filter((component) => {
        const proposed = claims(components, [component.name]);
        return existing.some((current) =>
          [...proposed].some(
            ([name, mode]) =>
              current.has(name) &&
              (mode === "write" || current.get(name) === "write"),
          ),
        );
      })
      .map((component) => component.name),
  );
}
