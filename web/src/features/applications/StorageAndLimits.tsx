import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Plus, X } from "lucide-react";
import type { Component, Capabilities } from "../../api";

export function invalidStorageAndLimits(c: Component) {
  const l = c.limits;
  if (
    l &&
    (!Number.isInteger(l.memoryMiB) ||
      l.memoryMiB < 64 ||
      l.memoryMiB > 65536 ||
      !Number.isInteger(l.cpuMillis) ||
      l.cpuMillis < 50 ||
      l.cpuMillis > 64000)
  )
    return true;
  const mounts = c.volumes || [];
  return (
    mounts.length > 4 ||
    (mounts.length > 0 && c.instances !== 1) ||
    mounts.some(
      (v, i) =>
        !/^[a-z][a-z0-9-]{0,47}$/.test(v.name) ||
        v.mountPath.length > 256 ||
        !/^\/[a-zA-Z0-9._/-]+$/.test(v.mountPath) ||
        v.mountPath.endsWith("/") ||
        v.mountPath.includes("//") ||
        v.mountPath.split("/").some((p) => p === "." || p === "..") ||
        /^\/(proc|sys|dev|etc|run|var\/run)(\/|$)/.test(v.mountPath) ||
        mounts
          .slice(0, i)
          .some(
            (o) =>
              o.name === v.name ||
              o.mountPath === v.mountPath ||
              o.mountPath.startsWith(v.mountPath + "/") ||
              v.mountPath.startsWith(o.mountPath + "/"),
          ),
    )
  );
}

export function StorageAndLimits({
  component: c,
  saved,
  capabilities,
  onChange,
}: {
  component: Component;
  saved?: Component;
  capabilities?: Capabilities;
  onChange: (component: Component) => void;
}) {
  if (c.resourceId || c.management === "observe") return null;
  const mounts = c.volumes || [];
  const locked = !!saved?.volumes?.length;
  return (
    <div className="storage-and-limits">
      <fieldset className="readiness-fields">
        <legend>Resource Limits</legend>
        <Label>
          <Checkbox
            aria-label={`Set Resource Limits for ${c.name}`}
            checked={!!c.limits}
            disabled={!capabilities?.resourceLimits || !!saved?.limits}
            onCheckedChange={(checked) =>
              onChange({
                ...c,
                limits:
                  checked === true
                    ? { memoryMiB: 128, cpuMillis: 250 }
                    : undefined,
              })
            }
          />{" "}
          Customize CPU and Memory Limits
        </Label>
        {c.limits && (
          <div className="form-grid compact">
            <div>
              <Label htmlFor={`memory-${c.name}`}>Memory (MiB)</Label>
              <Input
                id={`memory-${c.name}`}
                type="number"
                min={64}
                max={65536}
                value={c.limits.memoryMiB}
                onChange={(e) =>
                  onChange({
                    ...c,
                    limits: { ...c.limits!, memoryMiB: +e.target.value },
                  })
                }
              />
            </div>
            <div>
              <Label htmlFor={`cpu-${c.name}`}>CPU (millicores)</Label>
              <Input
                id={`cpu-${c.name}`}
                type="number"
                min={50}
                max={64000}
                value={c.limits.cpuMillis}
                onChange={(e) =>
                  onChange({
                    ...c,
                    limits: { ...c.limits!, cpuMillis: +e.target.value },
                  })
                }
              />
            </div>
          </div>
        )}
        {!capabilities?.resourceLimits && (
          <p className="small muted">Unavailable on this target.</p>
        )}
      </fieldset>
      <fieldset className="readiness-fields">
        <legend>Persistent Volumes</legend>
        {mounts.map((v, i) => (
          <div className="form-grid compact" key={i}>
            <div>
              <Label htmlFor={`volume-name-${c.name}-${i}`}>Volume Name</Label>
              <Input
                id={`volume-name-${c.name}-${i}`}
                value={v.name}
                readOnly={locked}
                onChange={(e) =>
                  onChange({
                    ...c,
                    volumes: mounts.map((m, n) =>
                      n === i ? { ...m, name: e.target.value } : m,
                    ),
                  })
                }
              />
            </div>
            <div>
              <Label htmlFor={`volume-path-${c.name}-${i}`}>Mount Path</Label>
              <Input
                id={`volume-path-${c.name}-${i}`}
                placeholder="/data"
                value={v.mountPath}
                readOnly={locked}
                onChange={(e) =>
                  onChange({
                    ...c,
                    volumes: mounts.map((m, n) =>
                      n === i ? { ...m, mountPath: e.target.value } : m,
                    ),
                  })
                }
              />
            </div>
            {!locked && (
              <Button
                type="button"
                variant="ghost"
                aria-label={`Remove Volume ${i + 1} for ${c.name}`}
                onClick={() =>
                  onChange({ ...c, volumes: mounts.filter((_, n) => n !== i) })
                }
              >
                <X size={15} />
              </Button>
            )}
          </div>
        ))}
        {!locked && (
          <Button
            type="button"
            variant="outline"
            disabled={
              !capabilities?.namedVolumes ||
              c.instances !== 1 ||
              mounts.length >= 4
            }
            onClick={() =>
              onChange({
                ...c,
                volumes: [...mounts, { name: "", mountPath: "" }],
              })
            }
          >
            <Plus size={15} /> Add Volume
          </Button>
        )}
        <p className="small muted">
          {locked
            ? "Retained across deployments. Saved mounts stay fixed; manage migrations and backups in the operator."
            : capabilities?.namedVolumes
              ? "One instance. Data is retained; the image must provide a writable mount directory. Mounts stay fixed after saving."
              : "Unavailable on this target."}
        </p>
      </fieldset>
      {invalidStorageAndLimits(c) && (
        <p className="error-inline" role="alert">
          Check limit ranges, unique volume names and absolute, non-overlapping
          mount paths. Volumes require one instance.
        </p>
      )}
    </div>
  );
}
