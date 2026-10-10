export function statusLabel(value: string) {
  const labels: Record<string, string> = {
    succeeded: "Completed",
    failed: "Failed",
    attention: "Needs Review",
    queued: "Queued",
    running: "Running",
    cancelled: "Cancelled",
    abandoned: "Tracking Closed",
    pending: "Pending",
    prepared: "Prepared",
    "running:healthy": "Healthy",
    "running:unhealthy": "Unhealthy",
    "running:starting": "Starting",
    exited: "Stopped",
    unknown: "Unknown",
    unavailable: "Unavailable",
    retired: "Retired",
    "not deployed": "Not Deployed",
    restarting: "Restarting",
    created: "Created",
    paused: "Paused",
    missing: "Missing",
    "retired:running": "Retired · Still Running",
    released: "Deployment Created",
    building: "Building",
    built: "Built",
  };
  return (
    labels[value] ||
    value.replaceAll("_", " ").replace(/\b\w/g, (char) => char.toUpperCase())
  );
}
export function Status({ value }: { value: string }) {
  const good = value === "succeeded" || value === "running:healthy";
  const bad =
    ["failed", "attention", "retired:running", "missing"].includes(value) ||
    value.includes("unhealthy") ||
    value.startsWith("exited");
  return (
    <span
      title={value}
      className={`status ${good ? "good" : bad ? "bad" : "neutral"}`}
    >
      <span aria-hidden="true" className="dot" />
      {statusLabel(value)}
    </span>
  );
}
