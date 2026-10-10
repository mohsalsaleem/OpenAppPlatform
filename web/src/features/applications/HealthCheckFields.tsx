import type { HealthCheck } from "../../api";
export function healthCheckSummary(h?: HealthCheck) {
  if (!h) return "Existing operator/image check";
  if (h.mode === "image") return "Image health check";
  return `HTTP GET ${h.path}; interval ${h.intervalSeconds || 10}s, timeout ${h.timeoutSeconds || 3}s, retries ${h.retries || 3}, start period ${h.startPeriodSeconds || 0}s`;
}
export function invalidHealthCheck(h?: HealthCheck) {
  if (!h || h.mode === "image") return false;
  if (
    !h.path ||
    h.path.length > 256 ||
    !/^\/[A-Za-z0-9/_~.\-]*$/.test(h.path) ||
    h.path.startsWith("//")
  )
    return true;
  return [
    [h.intervalSeconds ?? 10, 1, 300],
    [h.timeoutSeconds ?? 3, 1, 30],
    [h.retries ?? 3, 1, 20],
    [h.startPeriodSeconds ?? 0, 0, 600],
  ].some(([v, min, max]) => !Number.isInteger(v) || v < min || v > max);
}
export function HealthCheckFields({
  name,
  healthCheck,
  allowExisting,
  onChange,
}: {
  name: string;
  healthCheck?: HealthCheck;
  allowExisting: boolean;
  onChange: (value: HealthCheck | undefined) => void;
}) {
  const h = healthCheck;
  return (
    <fieldset className="readiness-fields">
      <legend>Native health check for {name}</legend>
      <label>
        Health check mode for {name}
        <select
          value={h?.mode || "existing"}
          onChange={(e) =>
            onChange(
              e.target.value === "existing"
                ? undefined
                : e.target.value === "image"
                  ? { mode: "image" }
                  : {
                      mode: "http",
                      path: "/health/ready",
                      intervalSeconds: 10,
                      timeoutSeconds: 3,
                      retries: 3,
                      startPeriodSeconds: 10,
                    },
            )
          }
        >
          {allowExisting && (
            <option value="existing">Keep existing operator/image check</option>
          )}
          <option value="http">HTTP readiness check</option>
          <option value="image">Use image health check</option>
        </select>
      </label>
      {h?.mode === "http" && (
        <>
          <label>
            Health check path for {name}
            <input
              value={h.path || ""}
              maxLength={256}
              onChange={(e) => onChange({ ...h, path: e.target.value })}
            />
          </label>
          <div className="form-grid">
            {(
              [
                {
                  key: "intervalSeconds",
                  label: "Interval",
                  min: 1,
                  max: 300,
                  defaultValue: 10,
                },
                {
                  key: "timeoutSeconds",
                  label: "Attempt timeout",
                  min: 1,
                  max: 30,
                  defaultValue: 3,
                },
                {
                  key: "retries",
                  label: "Retries",
                  min: 1,
                  max: 20,
                  defaultValue: 3,
                },
                {
                  key: "startPeriodSeconds",
                  label: "Start period",
                  min: 0,
                  max: 600,
                  defaultValue: 0,
                },
              ] as const
            ).map((f) => (
              <label key={f.key}>
                {f.label} for {name}
                {f.key !== "retries" ? " (seconds)" : ""}
                <input
                  type="number"
                  min={f.min}
                  max={f.max}
                  value={h[f.key] ?? f.defaultValue}
                  onChange={(e) =>
                    onChange({ ...h, [f.key]: Number(e.target.value) })
                  }
                />
              </label>
            ))}
          </div>
          <p className="small muted">
            Runs HTTP GET inside the container at 127.0.0.1 on its internal
            port. The image needs wget (Docker) or curl/wget (Coolify). Releases
            wait for healthy status.
          </p>
          {invalidHealthCheck(h) && (
            <p className="error-inline">
              Use a local path and timings within the displayed limits.
            </p>
          )}
        </>
      )}
      <p className="small muted">
        Saved settings apply on the next image deployment. Using the image check
        removes the HTTP override; an image without a health check will report
        running only.
      </p>
    </fieldset>
  );
}
