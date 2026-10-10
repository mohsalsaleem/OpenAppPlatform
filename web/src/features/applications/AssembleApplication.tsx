import { useCanOperate } from "../../access";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Plus } from "lucide-react";
import { api, type Application, type Resource, type Target } from "../../api";
import { ErrorBox, Loading } from "../../components/Feedback";
import { Status } from "../../components/Status";

export function AssembleApplication() {
  const canOperate = useCanOperate();
  const { id } = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [selected, setSelected] = useState<Record<string, string>>({});
  const [review, setReview] = useState(false);
  const [layoutId, setLayoutId] = useState("");
  const applications = useQuery({
    queryKey: ["applications"],
    queryFn: () => api<Application[]>("/applications"),
  });
  const layout = useQuery({
    queryKey: ["layout", layoutId],
    queryFn: () =>
      api<{
        name: string;
        definitionVersion: number;
        components: { name: string; kind: string }[];
      }>(`/applications/${layoutId}/layout`),
    enabled: !!layoutId,
  });
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<Target[]>("/targets"),
  });
  const resources = useQuery({
    queryKey: ["resources", id],
    queryFn: () => api<Resource[]>(`/targets/${id}/resources`),
  });
  const target = targets.data?.find((t) => t.id === id);
  const chosen =
    resources.data?.filter((r) => selected[r.id] !== undefined) ?? [];
  const layoutReady = !layoutId || (!!layout.data && !layout.isFetching);
  const valid =
    layoutReady &&
    /^[a-z][a-z0-9-]{0,47}$/.test(name) &&
    chosen.length > 0 &&
    chosen.length <= 16 &&
    chosen.every(
      (r) => !r.applicationId && /^[a-z][a-z0-9-]{0,47}$/.test(selected[r.id]),
    ) &&
    new Set(chosen.map((r) => selected[r.id])).size === chosen.length;
  const save = useMutation({
    mutationFn: () =>
      api<Application>("/applications", {
        method: "POST",
        body: JSON.stringify({
          name,
          targetId: id,
          environment: target!.environment,
          components: chosen.map((r) => ({
            name: selected[r.id],
            kind: "web",
            image: r.image ?? "",
            port: r.port ?? 0,
            instances: 1,
            strategy: "standard",
            resourceId: r.id,
            management: "observe",
          })),
        }),
      }),
    onSuccess: (a) => {
      client.invalidateQueries({ queryKey: ["applications"] });
      client.invalidateQueries({ queryKey: ["application-groups"] });
      client.invalidateQueries({ queryKey: ["resources", id] });
      navigate(`/applications/${a.id}`);
    },
  });
  if (resources.isPending || targets.isPending) return <Loading />;
  if (!target)
    return <ErrorBox error={targets.error ?? new Error("Target not found")} />;
  return (
    <>
      <Link className="back" to="/targets">
        <ArrowLeft size={16} /> Deployment targets
      </Link>
      <div className="page-heading">
        <div>
          <h1>Group existing services</h1>
          <p className="muted">
            {target.name} · {target.environment}
          </p>
        </div>
      </div>
      <div className="tabs" aria-label="Assembly steps">
        <span className={!review ? "assembly-current" : "muted"}>
          1. Select services
        </span>
        <span className={review ? "assembly-current" : "muted"}>
          2. Review application
        </span>
      </div>
      <ErrorBox error={resources.error || save.error || layout.error} />
      <label>
        Reuse a component layout
        <select
          value={layoutId}
          onChange={(e) => {
            setLayoutId(e.target.value);
            setSelected({});
          }}
        >
          <option value="">Start with resource names</option>
          {applications.data?.map((a) => (
            <option key={a.id} value={a.id}>
              {a.manifest.name} · {a.manifest.environment}
            </option>
          ))}
        </select>
      </label>
      {layout.data && (
        <p className="small muted">
          Reusing component names from definition{" "}
          {layout.data.definitionVersion}. Select this environment’s existing
          resources and review every mapping. Credentials, images, routes and
          resource IDs are not copied.
        </p>
      )}
      {!review ? (
        <section className="panel">
          <div className="section-heading">
            <div>
              <h2>Existing resources</h2>
              <p className="muted">
                Select up to 16 services. Grouping enables health and logs
                without changing workloads.
              </p>
            </div>
            <button
              className="secondary"
              onClick={() => {
                setSelected({});
                resources.refetch();
              }}
              disabled={resources.isFetching}
            >
              Refresh resources
            </button>
          </div>
          {resources.data?.length === 0 && (
            <p className="muted">No resources found in this target scope.</p>
          )}
          {resources.data?.map((r) => {
            const supported =
              r.artifactKind === "image" || r.artifactKind === "source";
            const blocked = !!r.applicationId || !supported || !layoutReady;
            return (
              <div className="assembly-resource" key={r.id}>
                <input
                  type="checkbox"
                  aria-label={`Select ${r.name}`}
                  checked={selected[r.id] !== undefined}
                  disabled={blocked}
                  onChange={(e) => {
                    const next = { ...selected };
                    if (e.target.checked) {
                      next[r.id] = r.name
                        .toLowerCase()
                        .replace(/[^a-z0-9-]+/g, "-")
                        .replace(/^-+|-+$/g, "")
                        .slice(0, 48);
                      const suggested =
                        layout.data?.components[chosen.length]?.name;
                      if (suggested && !Object.values(next).includes(suggested))
                        next[r.id] = suggested;
                      if (!/^[a-z]/.test(next[r.id]))
                        next[r.id] = `service-${chosen.length + 1}`;
                    } else delete next[r.id];
                    setSelected(next);
                  }}
                />
                <div className="grow">
                  <strong>{r.name}</strong>
                  <p className="small muted">
                    {r.artifactKind ?? "Unsupported"} · {r.id}
                  </p>
                  {r.applicationId && (
                    <Link
                      className="assembly-link"
                      to={`/applications/${r.applicationId}`}
                    >
                      Already grouped · open application
                    </Link>
                  )}
                </div>
                <Status value={r.status} />
              </div>
            );
          })}
          <div className="form-actions">
            <button
              className="primary"
              disabled={
                !chosen.length || chosen.length > 16 || resources.isError
              }
              onClick={() => setReview(true)}
            >
              Review {chosen.length} services
            </button>
          </div>
        </section>
      ) : (
        <section className="panel">
          <h2>Application mapping</h2>
          <p className="muted">
            All components start observe-only. No deployment, restart, or
            operator configuration changes.
          </p>
          <div className="configuration-fields">
            <label htmlFor="assembly-name">Application name</label>
            <input
              id="assembly-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-application"
            />
          </div>
          {chosen.map((r) => (
            <div className="component-form configuration-fields" key={r.id}>
              <label htmlFor={`mapping-${r.id}`}>
                Component name for {r.name}
              </label>
              <input
                id={`mapping-${r.id}`}
                value={selected[r.id]}
                onChange={(e) =>
                  setSelected({ ...selected, [r.id]: e.target.value })
                }
              />
              <p className="small muted">
                {r.id} · {r.artifactKind} · observe-only
              </p>
              <code className="image-ref">
                {r.image || "Source build stays with the operator"}
              </code>
            </div>
          ))}
          <p className="small muted">
            Names must be unique lowercase slugs, beginning with a letter, up to
            48 characters. Credentials and environment variables are not
            imported.
          </p>
          <div className="form-actions">
            <button
              className="secondary"
              disabled={save.isPending}
              onClick={() => setReview(false)}
            >
              Back to selection
            </button>
            <button
              className="primary"
              disabled={!canOperate || !valid || save.isPending}
              onClick={() => save.mutate()}
            >
              <Plus size={16} />
              {save.isPending ? "Grouping…" : "Create observed application"}
            </button>
          </div>
        </section>
      )}
    </>
  );
}
