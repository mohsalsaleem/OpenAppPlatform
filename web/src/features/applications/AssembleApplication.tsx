import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { useCanOperate } from "../../access";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Plus } from "lucide-react";
import { unwrap, api } from "../../api";
import { ErrorBox, Loading } from "../../components/Feedback";
import { Status } from "../../components/Status";

export function AssembleApplication() {
  const canOperate = useCanOperate();
  const { id } = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [availability, setAvailability] = useState("all");
  const [name, setName] = useState("");
  const [selected, setSelected] = useState<Record<string, string>>({});
  const [review, setReview] = useState(false);
  const [layoutId, setLayoutId] = useState("");
  const applications = useQuery({
    queryKey: ["applications"],
    queryFn: () => api.GET("/api/v1/applications").then(unwrap),
  });
  const layout = useQuery({
    queryKey: ["layout", layoutId],
    queryFn: () =>
      api
        .GET("/api/v1/applications/{id}/layout", {
          params: { path: { id: layoutId! } },
        })
        .then(unwrap),
    enabled: !!layoutId,
  });
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api.GET("/api/v1/targets").then(unwrap),
  });
  const resources = useQuery({
    queryKey: ["resources", id],
    queryFn: () =>
      api
        .GET("/api/v1/targets/{id}/resources", {
          params: { path: { id: id! } },
        })
        .then(unwrap),
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
      api
        .POST("/api/v1/applications", {
          body: {
            name,
            targetId: id!,
            environment: target!.environment,
            components: chosen.map((r) => ({
              name: selected[r.id],
              kind: "web" as const,
              image: r.image ?? "",
              port: r.port ?? 0,
              instances: 1,
              strategy: "standard" as const,
              resourceId: r.id,
              management: "observe" as const,
            })),
          },
        })
        .then(unwrap),
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
        <ArrowLeft size={16} /> Deployment Targets
      </Link>
      <div className="page-heading">
        <div>
          <h1>Group Existing Services</h1>
          <p className="muted">
            {target.name} · {target.environment}
          </p>
        </div>
      </div>
      <ol
        aria-label="Assembly Steps"
        className="flex flex-wrap items-center gap-6 mb-5"
      >
        <li
          aria-current={!review ? "step" : undefined}
          className="flex items-center gap-2 text-sm"
        >
          <Badge
            variant={!review ? "default" : "secondary"}
            className="size-5 justify-center rounded-full p-0"
          >
            1
          </Badge>{" "}
          Select Services
        </li>
        <li
          aria-current={review ? "step" : undefined}
          className="flex items-center gap-2 text-sm"
        >
          <Badge
            variant={review ? "default" : "secondary"}
            className="size-5 justify-center rounded-full p-0"
          >
            2
          </Badge>{" "}
          Review Application
        </li>
      </ol>
      <ErrorBox error={resources.error || save.error || layout.error} />
      <Label>
        Reuse a Component Layout
        <NativeSelect
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
        </NativeSelect>
      </Label>
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
              <h2>Existing Resources</h2>
              <p className="muted">
                Select up to 16 services. Grouping enables health and logs
                without changing workloads.
              </p>
            </div>
            <Button
              variant="outline"

              onClick={() => {
                setSelected({});
                resources.refetch();
              }}
              disabled={resources.isFetching}
            >
              Refresh Resources
            </Button>
          </div>
          <div className="form-grid">
            <Label>
              Search Services
              <Input
                type="search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Name or resource ID"
              />
            </Label>
            <Label>
              Availability
              <NativeSelect
                value={availability}
                onChange={(e) => setAvailability(e.target.value)}
              >
                <option value="all">All Services</option>
                <option value="available">Available to Group</option>
                <option value="grouped">Already Grouped</option>
              </NativeSelect>
            </Label>
          </div>
          <p className="small muted" role="status">
            {chosen.length} selected
          </p>
          <div className="form-actions selection-actions">
            <Button
              variant="default"

              disabled={
                !chosen.length || chosen.length > 16 || resources.isError
              }
              onClick={() => setReview(true)}
            >
              Review Selection ({chosen.length})
            </Button>
          </div>{" "}
          {resources.data?.length === 0 && (
            <p className="muted">No resources found in this target scope.</p>
          )}
          {resources.data
            ?.filter(
              (r) =>
                `${r.name} ${r.id}`
                  .toLowerCase()
                  .includes(search.toLowerCase()) &&
                (availability === "all" ||
                  (availability === "grouped"
                    ? !!r.applicationId
                    : !r.applicationId &&
                      ["image", "source"].includes(r.artifactKind || ""))),
            )
            .map((r) => {
              const supported =
                r.artifactKind === "image" || r.artifactKind === "source";
              const blocked = !!r.applicationId || !supported || !layoutReady;
              return (
                <div className="assembly-resource" key={r.id}>
                  <Checkbox
                    aria-label={`Select ${r.name}`}
                    checked={selected[r.id] !== undefined}
                    disabled={blocked}
                    onCheckedChange={(e) => {
                      const next = { ...selected };
                      if (e === true) {
                        next[r.id] = r.name
                          .toLowerCase()
                          .replace(/[^a-z0-9-]+/g, "-")
                          .replace(/^-+|-+$/g, "")
                          .slice(0, 48);
                        const suggested =
                          layout.data?.components[chosen.length]?.name;
                        if (
                          suggested &&
                          !Object.values(next).includes(suggested)
                        )
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
        </section>
      ) : (
        <section className="panel">
          <h2>Application Mapping</h2>
          <p className="muted">
            All components start observe-only. No deployment, restart, or
            operator configuration changes.
          </p>
          <div className="configuration-fields">
            <Label htmlFor="assembly-name">Application Name</Label>
            <Input
              id="assembly-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-application"
            />
          </div>
          {chosen.map((r) => (
            <div className="component-form configuration-fields" key={r.id}>
              <Label htmlFor={`mapping-${r.id}`}>
                Component Name for {r.name}
              </Label>
              <Input
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
            <Button
              variant="outline"

              disabled={save.isPending}
              onClick={() => setReview(false)}
            >
              Back to Selection
            </Button>
            <Button
              variant="default"

              disabled={!canOperate || !valid || save.isPending}
              onClick={() => save.mutate()}
            >
              <Plus size={16} />
              {save.isPending ? "Grouping…" : "Create Observed Application"}
            </Button>
          </div>
        </section>
      )}
    </>
  );
}
