export function Status({ value }: { value: string }) {
  const good = value === "succeeded" || value.startsWith("running:healthy");
  const bad =
    ["failed", "attention"].includes(value) ||
    value.includes("unhealthy") ||
    value.startsWith("exited");
  return (
    <span className={`status ${good ? "good" : bad ? "bad" : "neutral"}`}>
      <span aria-hidden="true" className="dot" />
      {value.replaceAll("_", " ")}
    </span>
  );
}
