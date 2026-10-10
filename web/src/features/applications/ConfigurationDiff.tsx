import { healthCheckSummary } from "./HealthCheckFields";
import type { Manifest } from "../../api";
export function ConfigurationDiff({
  before,
  after,
}: {
  before: Manifest;
  after: Manifest;
}) {
  const rows: { field: string; before: string; after: string }[] = [];
  const text = (v: unknown) => (v === undefined ? "Not set" : String(v));
  for (const c of after.components) {
    const old = before.components.find((o) => o.name === c.name);
    if (!old) continue;
    for (const field of ["image", "port", "instances", "hostPort"] as const) {
      if ((old[field] ?? 0) !== (c[field] ?? 0))
        rows.push({
          field: `${c.name} · ${field}`,
          before: text(old[field]),
          after: text(c[field]),
        });
    }
    const previousDependencies =
      [...(old.dependsOn || [])].sort().join(", ") || "None";
    const proposedDependencies =
      [...(c.dependsOn || [])].sort().join(", ") || "None";
    if (previousDependencies !== proposedDependencies)
      rows.push({
        field: `${c.name} · startup dependencies`,
        before: previousDependencies,
        after: proposedDependencies,
      });
    if (
      healthCheckSummary(old.healthCheck) !== healthCheckSummary(c.healthCheck)
    )
      rows.push({
        field: `${c.name} · native health check`,
        before: healthCheckSummary(old.healthCheck),
        after: healthCheckSummary(c.healthCheck),
      });
    const oldReadiness = old.readiness ?? {},
      nextReadiness = c.readiness ?? {};
    for (const [field, a, b] of [
      [
        "require healthy",
        !!oldReadiness.requireHealthy,
        !!nextReadiness.requireHealthy,
      ],
      [
        "observation timeout (seconds)",
        oldReadiness.timeoutSeconds || 900,
        nextReadiness.timeoutSeconds || 900,
      ],
    ] as const) {
      if (a !== b)
        rows.push({
          field: `${c.name} · ${field}`,
          before: String(a),
          after: String(b),
        });
    }
    for (const field of ["env", "services", "serviceEndpoints"] as const) {
      const a = old[field] ?? {},
        b = c[field] ?? {};
      for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
        if (a[key] !== b[key])
          rows.push({
            field: `${c.name} · ${field} · ${key}`,
            before:
              a[key] === undefined
                ? "Not set"
                : field === "services"
                  ? a[key]
                  : "Value hidden",
            after:
              b[key] === undefined
                ? "Removed"
                : field === "services"
                  ? b[key]
                  : a[key] === undefined
                    ? "Added (value hidden)"
                    : "Updated (value hidden)",
          });
      }
    }
  }
  if (!rows.length) return null;
  return (
    <section className="configuration-diff" aria-label="Configuration Changes">
      <h3>Changes for Future Deployments</h3>
      <p className="small muted">
        Saving updates this environment’s definition. It does not deploy or
        restart components. Variable values and endpoint addresses are hidden in
        this summary.
      </p>
      <div className="table-responsive">
        <table>
          <thead>
            <tr>
              <th>Field</th>
              <th>Current</th>
              <th>Proposed</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.field}>
                <td>{r.field}</td>
                <td>{r.before}</td>
                <td>{r.after}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
