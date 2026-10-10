import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { unwrap, api, type Target } from "../../api";
import { ErrorBox } from "../../components/Feedback";
export function ExistingEnvironment({ targets }: { targets: Target[] }) {
  const client = useQueryClient();
  const [sourceTargetId, setSource] = useState("");
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [environment, setEnvironment] = useState("");
  const me = useQuery({
    queryKey: ["identity"],
    queryFn: () => api.GET("/api/v1/auth/me").then(unwrap),
    retry: false,
  });
  const connect = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/targets/scopes", {
          body: { id, name, environment, sourceTargetId },
        })
        .then(unwrap),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["targets"] });
      setSource("");
      setId("");
      setName("");
      setEnvironment("");
    },
  });
  const source = targets.find((t) => t.id === sourceTargetId);
  const valid =
    source &&
    name.trim() &&
    /^[a-z][a-z0-9-]{0,47}$/.test(id) &&
    /^[a-z][a-z0-9-]{0,47}$/.test(environment) &&
    environment !== source.environment &&
    !targets.some((t) => t.id === id);
  if (
    me.data?.role !== "owner" ||
    me.data?.kind !== "session" ||
    !targets.some((t) => t.operator === "coolify")
  )
    return null;
  return (
    <details className="panel">
      <summary>Connect Another Existing Coolify Environment</summary>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          connect.mutate();
        }}
      >
        <p className="muted">
          Reuse a configured operator’s project, server and credential
          reference. Verification reads the existing environment; workloads and
          deployment triggers stay unchanged.
        </p>
        <div className="form-grid">
          <Label>
            Existing Operator Connection
            <NativeSelect
              value={sourceTargetId}
              onChange={(e) => setSource(e.target.value)}
            >
              <option value="">Select a configured Coolify connection</option>
              {targets
                .filter((t) => t.operator === "coolify")
                .map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
            </NativeSelect>
          </Label>
          <Label>
            New Connection Name
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Label>
          <Label>
            New Target ID
            <Input value={id} onChange={(e) => setId(e.target.value)} />
          </Label>
          <Label>
            Existing Operator Environment
            <Input
              value={environment}
              onChange={(e) => setEnvironment(e.target.value)}
            />
          </Label>
        </div>
        <Button
          variant="outline"
          className="button primary"
          disabled={!valid || connect.isPending}
        >
          {connect.isPending ? "Verifying…" : "Verify and Connect Environment"}
        </Button>
        <ErrorBox error={connect.error} />
        {connect.isSuccess && (
          <p role="status">
            Environment connection added. Group its existing resources when
            ready.
          </p>
        )}
      </form>
    </details>
  );
}
