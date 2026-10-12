import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Disclosure } from "@/components/Disclosure";
import type { Component } from "../../api";

export function invalidHostPort(c: Component, enabled: boolean) {
  if (c.resourceId || c.management === "observe" || c.kind !== "web")
    return false;
  return (
    enabled &&
    (!Number.isInteger(c.hostPort) ||
      !c.hostPort ||
      c.hostPort < 1024 ||
      c.hostPort > 65535 ||
      c.instances !== 1)
  );
}

export function HostPortFields({
  component: c,
  enabled,
  onEnabledChange,
  onPortChange,
}: {
  component: Component;
  enabled: boolean;
  onEnabledChange: (enabled: boolean) => void;
  onPortChange: (port: number) => void;
}) {
  if (c.kind !== "web" || c.resourceId || c.management === "observe")
    return null;
  const mapped = enabled || !!c.hostPort;
  return (
    <div className="space-y-2">
      <Label>
        <Checkbox
          aria-label={`Map Host Port for ${c.name}`}
          checked={mapped}
          disabled={c.instances !== 1}
          onCheckedChange={(checked) => {
            onEnabledChange(checked === true);
            if (checked !== true) onPortChange(0);
          }}
        />
        Map Host Port
      </Label>
      {mapped && (
        <div className="space-y-2">
          <Label htmlFor={`host-port-${c.name}`}>Host Port</Label>
          <Input
            id={`host-port-${c.name}`}
            aria-label={`Host Port for ${c.name}`}
            type="number"
            min={1024}
            max={65535}
            value={c.hostPort || 0}
            aria-invalid={invalidHostPort(c, mapped)}
            onChange={(e) => onPortChange(+e.target.value)}
          />
          {invalidHostPort(c, mapped) && (
            <p className="error-inline" role="alert">
              Choose a port from 1024–65535 for one instance.
            </p>
          )}
        </div>
      )}
      <Disclosure title="Port Mapping Help">
        <p className="small muted">
          A fixed host port requires one instance. Leave mapping off to keep
          native routing or the application network.
        </p>
      </Disclosure>
    </div>
  );
}
