import { Link } from "react-router-dom";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type ApplicationGroup } from "../../api";
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
    queryFn: () => api<ApplicationGroup[]>("/application-groups"),
  });
  const me = useQuery({
    queryKey: ["identity"],
    queryFn: () => api<{ role: string; kind: string }>("/auth/me"),
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
      return api(`/application-groups/${group.id}/environments`, {
        method: "POST",
        body: JSON.stringify({
          applicationId: selected,
          expectedGroupVersion: group.version,
          expectedSourceVersion: source.version,
        }),
      });
    },
    onSuccess: () => {
      setSelected("");
      client.invalidateQueries({ queryKey: ["application-groups"] });
      client.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  const unlink = useMutation({
    mutationFn: () =>
      api(
        `/application-groups/${group!.id}/environments/${applicationId}/unlink`,
        {
          method: "POST",
          body: JSON.stringify({ expectedVersion: group!.version }),
        },
      ),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["application-groups"] });
      client.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  if (!group) return null;
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
        <button
          className="button"
          onClick={() => unlink.mutate()}
          disabled={unlink.isPending}
        >
          Make This Environment Standalone
        </button>
      )}
      <ErrorBox error={unlink.error} />
      {canLink && candidates.length > 0 && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            link.mutate();
          }}
        >
          <label>
            Existing Environment
            <select
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
            </select>
          </label>
          <p className="small muted">
            Linking changes organization only. Deployments, resource IDs and
            permissions stay with each environment.
          </p>
          <button className="button" disabled={!selected || link.isPending}>
            Link Environment
          </button>
          <ErrorBox error={link.error} />
        </form>
      )}
    </section>
  );
}
