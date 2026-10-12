import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Checkbox } from "@/components/ui/checkbox";
import { DeploymentDialog } from "../../components/DeploymentDialog";
import { ErrorBox } from "../../components/Feedback";
import { api, unwrap, type Application } from "../../api";

export function DatabaseLinkDialog({
  application,
  docker,
  onClose,
}: {
  application: Application;
  docker: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [resourceId, setResourceId] = useState("");
  const [name, setName] = useState("database");
  const [endpoint, setEndpoint] = useState("");
  const [ack, setAck] = useState(false);
  const resources = useQuery({
    queryKey: ["resources", application.manifest.targetId],
    queryFn: () =>
      api
        .GET("/api/v1/targets/{id}/resources", {
          params: { path: { id: application.manifest.targetId } },
        })
        .then(unwrap),
  });
  const eligible = (resources.data || []).filter(
    (r) =>
      !r.applicationId &&
      !r.description?.startsWith("OpenAppPlatform:") &&
      (r.artifactKind === "database" || (docker && r.artifactKind === "image")),
  );
  let validEndpoint = false;
  try {
    const u = new URL(endpoint);
    validEndpoint =
      ["postgres:", "postgresql:"].includes(u.protocol) &&
      !!u.hostname &&
      !u.username &&
      !u.password &&
      !u.search &&
      !u.hash &&
      endpoint.length <= 1024 &&
      !/\s/.test(endpoint);
  } catch {}
  const link = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/applications/{id}/databases", {
          params: { path: { id: application.id } },
          body: {
            name,
            resourceId,
            endpoint,
            acknowledge: ack,
            expectedVersion: application.version,
          },
        })
        .then(unwrap),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["application", application.id] });
      client.invalidateQueries({ queryKey: ["instances", application.id] });
      client.invalidateQueries({
        queryKey: ["resources", application.manifest.targetId],
      });
      client.invalidateQueries({ queryKey: ["applications"] });
      onClose();
    },
  });
  return (
    <DeploymentDialog onClose={onClose}>
      <h2 id="deploy-title">Link Existing Database</h2>
      <div className="component-badges">
        <Badge variant="secondary">PostgreSQL</Badge>
        <Badge variant="outline">Operator Managed</Badge>
      </div>
      <Label htmlFor="database-resource">Existing Resource</Label>
      <NativeSelect
        id="database-resource"
        value={resourceId}
        disabled={link.isPending}
        onChange={(e) => setResourceId(e.target.value)}
      >
        <option value="">Select a database</option>
        {eligible.map((r) => (
          <option key={r.id} value={r.id}>
            {r.name} · {r.id}
          </option>
        ))}
      </NativeSelect>
      {!resources.isPending && !eligible.length && (
        <p className="small muted">
          No eligible resource in this target. Databases are created and managed
          through your operator.
        </p>
      )}
      {docker && (
        <p className="small muted">
          Select an explicitly scoped PostgreSQL container. OAP does not detect
          its database engine.
        </p>
      )}
      <Label htmlFor="database-name">Component Name</Label>
      <Input
        id="database-name"
        value={name}
        maxLength={48}
        disabled={link.isPending}
        onChange={(e) => setName(e.target.value)}
      />
      <Label htmlFor="database-endpoint">Connection Endpoint</Label>
      <Input
        id="database-endpoint"
        value={endpoint}
        maxLength={1024}
        placeholder="postgresql://database:5432/application"
        disabled={link.isPending}
        onChange={(e) => setEndpoint(e.target.value)}
      />
      <p className="small muted">
        No credentials here. API and worker credentials use Secret References;
        OAP does not import them or test SQL connections.
      </p>
      <Label>
        <Checkbox
          checked={ack}
          disabled={link.isPending}
          onCheckedChange={(v) => setAck(v === true)}
        />
        Allow application members to view this resource’s health and logs. Keep
        database lifecycle, data and backups with the operator.
      </Label>
      <ErrorBox error={resources.error || link.error} />
      <div className="dialog-actions">
        <Button variant="outline" onClick={onClose} disabled={link.isPending}>
          Cancel
        </Button>
        <Button
          disabled={
            link.isPending ||
            !ack ||
            !validEndpoint ||
            !eligible.some((r) => r.id === resourceId) ||
            !/^[a-z][a-z0-9-]{0,47}$/.test(name) ||
            application.manifest.components.some((c) => c.name === name)
          }
          onClick={() => link.mutate()}
        >
          {link.isPending ? "Linking…" : "Link Database"}
        </Button>
      </div>
    </DeploymentDialog>
  );
}
