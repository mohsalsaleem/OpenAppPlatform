import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useState, useEffect } from "react";
import type { Component } from "../../api";

export function RuntimeVariables({
  component,
  onChange,
  onValidity,
  environmentSupported,
  connectionsSupported,
  endpointsSupported,
}: {
  component: Component;
  onChange: (
    field: "env" | "secretRefs" | "services" | "serviceEndpoints",
    value: Record<string, string>,
  ) => void;
  onValidity: (valid: boolean) => void;
  environmentSupported: boolean;
  connectionsSupported: boolean;
  endpointsSupported: boolean;
}) {
  const [values, setValues] = useState({
    env: JSON.stringify(component.env || {}, null, 2),
    secretRefs: JSON.stringify(component.secretRefs || {}, null, 2),
    services: JSON.stringify(component.services || {}, null, 2),
    serviceEndpoints: JSON.stringify(component.serviceEndpoints || {}, null, 2),
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(
    () => onValidity(Object.keys(errors).length === 0),
    [errors, component.name],
  );
  function edit(
    field: "env" | "secretRefs" | "services" | "serviceEndpoints",
    text: string,
  ) {
    setValues({ ...values, [field]: text });
    const next = { ...errors };
    try {
      const parsed = JSON.parse(text);
      if (
        !parsed ||
        Array.isArray(parsed) ||
        typeof parsed !== "object" ||
        Object.values(parsed).some((v) => typeof v !== "string")
      )
        throw Error("Use a JSON object with string values.");
      delete next[field];
      onChange(field, parsed);
    } catch {
      next[field] = "Use a JSON object with string values.";
    }
    setErrors(next);
    onValidity(Object.keys(next).length === 0);
  }
  return (
    <div className="form-grid runtime-variables">
      <div>
        <Label htmlFor={`env-${component.name}`}>
          Environment Variables for {component.name}
        </Label>
        <Textarea
          id={`env-${component.name}`}
          rows={4}
          value={values.env}
          readOnly={!!component.resourceId || !environmentSupported}
          onChange={(e) => edit("env", e.target.value)}
        />
        <small className="muted">
          {environmentSupported
            ? "Plain configuration only; values are saved in application and release definitions. Keep secrets in your operator."
            : "Managed environment variables are not supported by this target yet. Configure them through the operator."}
        </small>
        {errors.env && (
          <p role="alert" className="error-inline">
            {errors.env}
          </p>
        )}
      </div>
      <div>
        <Label htmlFor={`secret-refs-${component.name}`}>
          Secret References for {component.name}
        </Label>
        <Textarea
          id={`secret-refs-${component.name}`}
          rows={3}
          value={values.secretRefs}
          readOnly={!!component.resourceId || !environmentSupported}
          onChange={(e) => edit("secretRefs", e.target.value)}
        />
        <small className="muted">
          Map a variable to an authorized reference, such as{" "}
          {JSON.stringify({ DATABASE_URL: "database-url-v1" })}. Your
          administrator binds it to this application. Enter reference names
          only; deployment is blocked when a binding is missing.
        </small>
        {errors.secretRefs && (
          <p role="alert" className="error-inline">
            {errors.secretRefs}
          </p>
        )}
      </div>
      <div>
        <Label htmlFor={`services-${component.name}`}>
          Service Connections for {component.name}
        </Label>
        <Textarea
          id={`services-${component.name}`}
          rows={4}
          value={values.services}
          readOnly={!!component.resourceId || !connectionsSupported}
          onChange={(e) => edit("services", e.target.value)}
        />
        <small className="muted">
          {connectionsSupported
            ? 'Map a variable to a managed component: {"API_URL":"api"}. OAP uses application DNS where supported, or the explicit endpoint below. DNS does not provide health-aware balancing.'
            : "Application DNS connections are not supported by this target yet."}
        </small>
        {errors.services && (
          <p role="alert" className="error-inline">
            {errors.services}
          </p>
        )}
      </div>
      {endpointsSupported && (
        <div className="endpoint-field">
          <Label htmlFor={`endpoints-${component.name}`}>
            Service Endpoints for {component.name}
          </Label>
          <Textarea
            id={`endpoints-${component.name}`}
            rows={3}
            value={values.serviceEndpoints}
            readOnly={!!component.resourceId}
            onChange={(e) => edit("serviceEndpoints", e.target.value)}
          />
          <small className="muted">
            Map a referenced component to its existing HTTP(S) URL, for example{" "}
            {JSON.stringify({ api: "https://api.example.com" })}. Required where
            application DNS is unavailable. Keep credentials out of URLs.
          </small>
          {errors.serviceEndpoints && (
            <p role="alert" className="error-inline">
              {errors.serviceEndpoints}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
