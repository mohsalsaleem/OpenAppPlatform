import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Component as ComponentIcon, Plus, X } from "lucide-react";
import {
  api,
  type Application,
  type Target,
  type Manifest,
  type Resource,
  type Component,
  type Capabilities,
} from "../../api";
import { RuntimeVariables } from "./RuntimeVariables";
import { ErrorBox, Loading } from "../../components/Feedback";
export function NewApplication() {
  const navigate = useNavigate();
  const client = useQueryClient();
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<Target[]>("/targets"),
  });
  const [name, setName] = useState("");
  const [target, setTarget] = useState("");
  const [components, setComponents] = useState<
    (Component & { editorId: string })[]
  >([
    {
      editorId: crypto.randomUUID(),
      name: "web",
      image: "nginx:1.27-alpine",
      port: 80,
      instances: 1,
      hostPort: 0,
      strategy: "standard",
      kind: "web",
      resourceId: "",
    },
  ]);
  const selected = targets.data?.find(
    (t) => t.id === (target || targets.data?.[0]?.id),
  );
  const [invalid, setInvalid] = useState<Record<string, boolean>>({});
  const capabilities = useQuery({
    queryKey: ["capabilities", selected?.id],
    queryFn: () => api<Capabilities>(`/targets/${selected!.id}/capabilities`),
    enabled: !!selected,
  });
  const resources = useQuery({
    queryKey: ["resources", selected?.id],
    queryFn: () => api<Resource[]>(`/targets/${selected!.id}/resources`),
    enabled: !!selected,
  });
  const mutation = useMutation({
    mutationFn: (manifest: Manifest) =>
      api<Application>("/applications", {
        method: "POST",
        body: JSON.stringify(manifest),
      }),
    onSuccess: (a) => {
      client.invalidateQueries({ queryKey: ["applications"] });
      navigate(`/applications/${a.id}`);
    },
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!selected || components.some((c) => invalid[c.editorId])) return;
    mutation.mutate({
      name,
      targetId: selected.id,
      environment: selected.environment,
      components: components.map(({ editorId: _editorId, ...c }) => ({
        ...c,
        resourceId: c.resourceId || undefined,
      })),
    });
  }
  return (
    <>
      <Link className="back" to="/">
        <ArrowLeft size={16} /> Applications
      </Link>
      <div className="page-heading">
        <div>
          <p className="eyebrow">APPLICATION SETUP</p>
          <h1>Make room for your next idea</h1>
          <p className="muted">
            Define the components. Deploy when you’re ready.
          </p>
        </div>
      </div>
      <form onSubmit={submit}>
        <section className="panel">
          <div className="section-heading">
            <h2>Application identity</h2>
            <span className="small muted">
              Creating a definition does not deploy workloads.
            </span>
          </div>
          <div className="form-grid">
            <div>
              <label htmlFor="app-name">Application name</label>
              <input
                id="app-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                pattern="[a-z][a-z0-9-]{0,47}"
                required
                placeholder="my-application"
              />
              <small className="muted">
                Lowercase letters, numbers, and hyphens.
              </small>
            </div>
            <div>
              <label htmlFor="target">Deployment target</label>
              <select
                id="target"
                value={target || targets.data?.[0]?.id || ""}
                onChange={(e) => setTarget(e.target.value)}
              >
                {targets.data?.map((t) => (
                  <option value={t.id} key={t.id}>
                    {t.name} · {t.environment}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <ErrorBox error={targets.error} />
        </section>
        <section className="panel">
          <div className="section-heading">
            <h2>Web components</h2>
            <button
              type="button"
              className="secondary"
              onClick={() =>
                setComponents([
                  ...components,
                  {
                    editorId: crypto.randomUUID(),
                    name: `web-${components.length + 1}`,
                    image: "nginx:1.27-alpine",
                    port: 80,
                    instances: 1,
                    hostPort: 0,
                    strategy: "standard",
                    kind: "web",
                    resourceId: "",
                  },
                ])
              }
              disabled={components.length >= 16}
            >
              <Plus size={16} /> Add component
            </button>
          </div>
          {components.map((c, i) => (
            <div className="component-form" key={c.editorId}>
              <div className="section-heading">
                <span className="small">
                  <ComponentIcon size={16} /> Component {i + 1}
                </span>
                {components.length > 1 && (
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`Remove component ${i + 1}`}
                    onClick={() =>
                      setComponents(components.filter((_, n) => n !== i))
                    }
                  >
                    <X size={16} />
                  </button>
                )}
              </div>
              <div className="form-grid">
                <div>
                  <label htmlFor={`name-${i}`}>Component name</label>
                  <input
                    id={`name-${i}`}
                    value={c.name}
                    onChange={(e) =>
                      setComponents(
                        components.map((v, n) =>
                          n === i ? { ...v, name: e.target.value } : v,
                        ),
                      )
                    }
                    required
                  />
                </div>
                <div>
                  <label htmlFor={`adopt-${i}`}>Resource ownership</label>
                  <select
                    id={`adopt-${i}`}
                    value={c.resourceId}
                    onChange={(e) => {
                      const r = resources.data?.find(
                        (r) => r.id === e.target.value,
                      );
                      setComponents(
                        components.map((v, n) =>
                          n === i
                            ? {
                                ...v,
                                resourceId: e.target.value,
                                image: r?.image || v.image,
                                instances: r ? 1 : v.instances,
                                port: r?.port || v.port,
                              }
                            : v,
                        ),
                      );
                    }}
                  >
                    <option value="">
                      Create a new managed resource on deployment
                    </option>
                    {resources.data
                      ?.filter((r) => r.image && r.artifactKind === "image")
                      .map((r) => (
                        <option key={r.id} value={r.id}>
                          Adopt {r.name}
                        </option>
                      ))}
                  </select>
                </div>
                <div>
                  <label htmlFor={`image-${i}`}>Container image</label>
                  <input
                    id={`image-${i}`}
                    value={c.image}
                    readOnly={!!c.resourceId}
                    onChange={(e) =>
                      setComponents(
                        components.map((v, n) =>
                          n === i ? { ...v, image: e.target.value } : v,
                        ),
                      )
                    }
                    required
                  />
                  <small className="muted">
                    Use an image digest for reproducible releases.
                  </small>
                </div>
                <div className="form-grid compact">
                  <div>
                    <label htmlFor={`port-${i}`}>Internal port</label>
                    <input
                      id={`port-${i}`}
                      type="number"
                      min="1"
                      max="65535"
                      value={c.port}
                      onChange={(e) =>
                        setComponents(
                          components.map((v, n) =>
                            n === i ? { ...v, port: +e.target.value } : v,
                          ),
                        )
                      }
                    />
                  </div>
                  <div>
                    <label htmlFor={`instances-${i}`}>Instances</label>
                    <input
                      id={`instances-${i}`}
                      type="number"
                      min="1"
                      max={c.resourceId ? "1" : "4"}
                      value={c.instances}
                      onChange={(e) =>
                        setComponents(
                          components.map((v, n) =>
                            n === i ? { ...v, instances: +e.target.value } : v,
                          ),
                        )
                      }
                    />
                  </div>
                </div>
                <div>
                  <label htmlFor={`host-port-${i}`}>Host port</label>
                  <input
                    id={`host-port-${i}`}
                    type="number"
                    min="0"
                    max="65535"
                    value={c.hostPort}
                    onChange={(e) =>
                      setComponents(
                        components.map((v, n) =>
                          n === i ? { ...v, hostPort: +e.target.value } : v,
                        ),
                      )
                    }
                  />
                  <small className="muted">
                    0 keeps the component private. A fixed host port requires
                    one instance.
                  </small>
                </div>
              </div>
              <RuntimeVariables
                key={`${selected?.id}-${c.editorId}-${c.resourceId}`}
                component={c}
                onChange={(field, value) =>
                  setComponents(
                    components.map((v, n) =>
                      n === i ? { ...v, [field]: value } : v,
                    ),
                  )
                }
                onValidity={(valid) =>
                  setInvalid((current) => ({
                    ...current,
                    [c.editorId]: !valid,
                  }))
                }
                environmentSupported={!!capabilities.data?.environment}
                connectionsSupported={
                  !!(
                    capabilities.data?.applicationDns ||
                    capabilities.data?.serviceEndpoints
                  )
                }
                endpointsSupported={!!capabilities.data?.serviceEndpoints}
              />
            </div>
          ))}
          <ErrorBox error={resources.error || capabilities.error} />
          <p className="small muted">
            Standard deployment uses the operator’s lifecycle and may cause
            downtime. Instances are grouped; traffic balancing is not yet
            provided.
          </p>
        </section>
        <ErrorBox error={mutation.error} />
        <div className="form-actions">
          <Link className="button secondary" to="/">
            Cancel
          </Link>
          <button
            className="primary"
            disabled={
              !selected ||
              mutation.isPending ||
              components.some((c) => invalid[c.editorId])
            }
          >
            {mutation.isPending ? "Creating…" : "Create application"}
          </button>
        </div>
      </form>
    </>
  );
}
