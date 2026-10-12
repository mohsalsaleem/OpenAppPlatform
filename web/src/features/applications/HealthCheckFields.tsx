import { Field } from "@/components/ui/field";
import { FieldSet, FieldLegend } from "@/components/ui/field";
import { Disclosure } from "@/components/Disclosure";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import type { HealthCheck } from "../../api";
const timings = [
  {
    key: "intervalSeconds",
    label: "Interval",
    min: 1,
    max: 300,
    defaultValue: 10,
  },
  {
    key: "timeoutSeconds",
    label: "Attempt Timeout",
    min: 1,
    max: 30,
    defaultValue: 3,
  },
  { key: "retries", label: "Retries", min: 1, max: 20, defaultValue: 3 },
  {
    key: "startPeriodSeconds",
    label: "Start Period",
    min: 0,
    max: 600,
    defaultValue: 0,
  },
] as const;
function pathError(h: HealthCheck) {
  return !h.path ||
    h.path.length > 256 ||
    !/^\/[A-Za-z0-9/_~.\-]*$/.test(h.path) ||
    h.path.startsWith("//")
    ? "Use a path starting with /, such as /health/ready (up to 256 characters; letters, numbers, /, _, ~, . and -)."
    : "";
}
export function healthCheckSummary(h?: HealthCheck) {
  if (!h) return "Existing health check";
  if (h.mode === "image") return "Image health check";
  return `HTTP GET ${h.path}; interval ${h.intervalSeconds ?? 10}s, timeout ${h.timeoutSeconds ?? 3}s, retries ${h.retries ?? 3}, start period ${h.startPeriodSeconds ?? 0}s`;
}
export function invalidHealthCheck(h?: HealthCheck) {
  return (
    !!h &&
    h.mode === "http" &&
    (!!pathError(h) ||
      timings.some(
        (f) =>
          !Number.isInteger(h[f.key] ?? f.defaultValue) ||
          (h[f.key] ?? f.defaultValue) < f.min ||
          (h[f.key] ?? f.defaultValue) > f.max,
      ))
  );
}
export function HealthCheckFields({
  name,
  healthCheck: h,
  allowExisting,
  allowHTTP = true,
  onChange,
}: {
  name: string;
  healthCheck?: HealthCheck;
  allowExisting: boolean;
  allowHTTP?: boolean;
  onChange: (value: HealthCheck | undefined) => void;
}) {
  const prefix = `health-${name}`;
  return (
    <FieldSet className="configuration-section gap-3">
      <FieldLegend className="mb-0">Health Check for {name}</FieldLegend>
      <Field className="gap-2">
        <Label htmlFor={`${prefix}-mode`}>Health Check Mode for {name}</Label>
        <Select
          value={h?.mode || "existing"}
          onValueChange={(value) =>
            onChange(
              value === "existing"
                ? undefined
                : value === "image"
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
          <SelectTrigger id={`${prefix}-mode`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent position="popper">
            {allowExisting && (
              <SelectItem value="existing">
                Keep Existing Health Check
              </SelectItem>
            )}
            {allowHTTP && (
              <SelectItem value="http">Container HTTP Check</SelectItem>
            )}
            <SelectItem value="image">Use Image Health Check</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {h?.mode === "http" && (
        <>
          <p className="small muted">
            Deployments wait for healthy status when this HTTP check is
            selected.
          </p>
          <Label htmlFor={`${prefix}-path`}>Health Check Path for {name}</Label>
          <Input
            id={`${prefix}-path`}
            value={h.path || ""}
            maxLength={256}
            aria-invalid={!!pathError(h)}
            aria-describedby={`${prefix}-path-help`}
            onChange={(e) => onChange({ ...h, path: e.target.value })}
          />
          <p
            id={`${prefix}-path-help`}
            role={pathError(h) ? "alert" : undefined}
            className={pathError(h) ? "error-inline" : "small muted"}
          >
            {pathError(h) ||
              "A path inside this container, for example /health/ready."}
          </p>
          <Disclosure title={<>Check Timing</>}>
            <div className="form-grid">
              {timings.map((f) => {
                const value = h[f.key] ?? f.defaultValue;
                const invalid =
                  !Number.isInteger(value) || value < f.min || value > f.max;
                return (
                  <div key={f.key}>
                    <Label htmlFor={`${prefix}-${f.key}`}>
                      {f.label} for {name}
                      {f.key !== "retries" ? " (seconds)" : ""}
                    </Label>
                    <Input
                      id={`${prefix}-${f.key}`}
                      type="number"
                      min={f.min}
                      max={f.max}
                      value={value}
                      aria-invalid={invalid}
                      aria-describedby={`${prefix}-${f.key}-help`}
                      onChange={(e) =>
                        onChange({ ...h, [f.key]: Number(e.target.value) })
                      }
                    />
                    <p
                      id={`${prefix}-${f.key}-help`}
                      role={invalid ? "alert" : undefined}
                      className={invalid ? "error-inline" : "small muted"}
                    >
                      {invalid
                        ? `Enter a whole number from ${f.min} to ${f.max}.`
                        : `${f.min}–${f.max}${f.key !== "retries" ? " seconds" : " attempts"}.`}
                    </p>
                  </div>
                );
              })}
            </div>
          </Disclosure>
          <Disclosure title={<>Container Requirements</>}>
            <p className="small muted">
              Runs HTTP GET at 127.0.0.1 on the container’s internal port. The
              image needs wget (Docker) or curl/wget (Coolify).
            </p>
          </Disclosure>
        </>
      )}
      <p className="small muted">
        Applies on the next image deployment. An image without a health check
        reports running, with health unknown.
      </p>
    </FieldSet>
  );
}
