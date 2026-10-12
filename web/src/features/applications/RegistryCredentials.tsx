import { Disclosure } from "@/components/Disclosure";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Component } from "../../api";

export function RegistryCredentials({
  component,
  supported,
  operator,
  onChange,
}: {
  component: Component;
  supported: boolean;
  operator?: string;
  onChange: (value: string) => void;
}) {
  if (component.resourceId || component.management === "observe") return null;
  if (!supported)
    return operator === "coolify" ? (
      <p className="small muted">
        Private images use the registry login configured in Coolify.
      </p>
    ) : null;
  return (
    <Disclosure
      title="Private Registry"
      defaultOpen={!!component.registryCredentialRef}
    >
      <Label htmlFor={`registry-${component.name}`}>Credential Reference</Label>
      <Input
        id={`registry-${component.name}`}
        aria-label={`Registry Credential Reference for ${component.name}`}
        value={component.registryCredentialRef ?? ""}
        placeholder="registry-read-v1"
        onChange={(event) => onChange(event.target.value)}
      />
      <p className="small muted">
        Optional. Use a reference granted by the platform owner. Credentials are
        used only to pull the image.
      </p>
    </Disclosure>
  );
}
