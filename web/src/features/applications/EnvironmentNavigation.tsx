import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Link } from "react-router-dom";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { unwrap, api } from "../../api";
import { ErrorBox } from "../../components/Feedback";

export function EnvironmentNavigation({
  applicationId,
  organization = false,
}: {
  applicationId: string;
  organization?: boolean;
}) {
  const client = useQueryClient();
  const [selected, setSelected] = useState("");
  const groups = useQuery({
    queryKey: ["application-groups"],
    queryFn: () => api.GET("/api/v1/application-groups").then(unwrap),
  });
  const me = useQuery({
    queryKey: ["identity"],
    queryFn: () => api.GET("/api/v1/auth/me").then(unwrap),
    retry: false,
  });
  const group = groups.data?.find((g) =>
    g.environments.some((e) => e.id === applicationId),
  );
  const candidates =
    groups.data?.filter(
      (g) =>
        g.id !== group?.id &&
        g.environments.length === 1 &&
        !group?.environments.some(
          (e) =>
            e.manifest.environment === g.environments[0].manifest.environment,
        ),
    ) ?? [];
  const canLink = me.data?.role === "owner" && me.data?.kind === "session";
  const link = useMutation({
    mutationFn: () => {
      const source = candidates.find((g) => g.environments[0].id === selected);
      if (!group || !source)
        throw new Error("Refresh and select an available environment");
      return api
        .POST("/api/v1/application-groups/{id}/environments", {
          params: { path: { id: group.id! } },
          body: {
            applicationId: selected,
            expectedGroupVersion: group.version,
            expectedSourceVersion: source.version,
          },
        })
        .then(unwrap);
    },
    onSuccess: () => {
      setSelected("");
      client.invalidateQueries({ queryKey: ["application-groups"] });
      client.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  const unlink = useMutation({
    mutationFn: () =>
      api
        .POST(
          "/api/v1/application-groups/{id}/environments/{environment}/unlink",
          {
            params: { path: { id: group!.id, environment: applicationId } },
            body: { expectedVersion: group!.version },
          },
        )
        .then(unwrap),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["application-groups"] });
      client.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  if (
    !group ||
    (organization &&
      (!canLink ||
        (group.environments.length === 1 && candidates.length === 0)))
  )
    return null;
  if (!organization)
    return (
      <nav
        className="environment-switcher"
        aria-label="Application Environments"
      >
        {group.environments.map((e) => (
          <Link
            key={e.id}
            reloadDocument
            to={`/applications/${e.id}`}
            className="environment"
            aria-current={e.id === applicationId ? "page" : undefined}
          >
            {e.manifest.environment}
          </Link>
        ))}
      </nav>
    );
  return (
    <section className="panel">
      <div className="section-heading">
        <h2>Environment Organization</h2>
        <span className="small muted">{group.name}</span>
      </div>
      {canLink && group.environments.length > 1 && (
        <Button
          variant="outline"
          className="button"
          onClick={() => unlink.mutate()}
          disabled={unlink.isPending}
        >
          Make This Environment Standalone
        </Button>
      )}
      <ErrorBox error={unlink.error} />
      {canLink && candidates.length > 0 && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            link.mutate();
          }}
        >
          <Label>
            Existing Environment
            <NativeSelect
              aria-label="Existing Environment"
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
            >
              <option value="">
                Select an existing application environment
              </option>
              {candidates.map((g) => (
                <option key={g.id} value={g.environments[0].id}>
                  {g.name} · {g.environments[0].manifest.environment}
                </option>
              ))}
            </NativeSelect>
          </Label>
          <p className="small muted">
            Linking changes organization only. Deployments, resource IDs and
            permissions stay with each environment.
          </p>
          <Button
            variant="outline"
            className="button"
            disabled={!selected || link.isPending}
          >
            Link Environment
          </Button>
          <ErrorBox error={link.error} />
        </form>
      )}
    </section>
  );
}
