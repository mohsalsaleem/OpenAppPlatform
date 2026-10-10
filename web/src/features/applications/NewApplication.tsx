import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Button } from "@/components/ui/button";
import { useCanOperate } from "../../access";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Component as ComponentIcon, Plus, X } from "lucide-react";
import { unwrap, api, type Manifest, type Component } from "../../api";
import { RuntimeVariables } from "./RuntimeVariables";
import { ErrorBox } from "../../components/Feedback";
export function NewApplication() {
  const canOperate = useCanOperate();
  const navigate = useNavigate();
  const client = useQueryClient();
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api.GET("/api/v1/targets").then(unwrap),
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
    queryFn: () =>
      api
        .GET("/api/v1/targets/{id}/capabilities", {
          params: { path: { id: selected!.id! } },
        })
        .then(unwrap),
    enabled: !!selected,
  });
  const resources = useQuery({
    queryKey: ["resources", selected?.id],
    queryFn: () =>
      api
        .GET("/api/v1/targets/{id}/resources", {
          params: { path: { id: selected!.id! } },
        })
        .then(unwrap),
    enabled: !!selected,
  });
  const mutation = useMutation({
    mutationFn: (manifest: Manifest) =>
      api.POST("/api/v1/applications", { body: manifest }).then(unwrap),
    onSuccess: (a) => {
      client.invalidateQueries({ queryKey: ["applications"] });
      client.invalidateQueries({ queryKey: ["application-groups"] });
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
        management: c.resourceId ? "observe" : undefined,
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
          <h1>Create Application</h1>
          <p className="muted">
            Define the components. Deploy when you’re ready.
          </p>
        </div>
      </div>
      <form onSubmit={submit}>
        <section className="panel">
          <div className="section-heading">
            <h2>Application Details</h2>
            <span className="small muted">
              Creating a definition does not deploy workloads.
            </span>
          </div>
          <div className="form-grid">
            <div>
              <Label htmlFor="app-name">Application Name</Label>
              <Input
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
              <Label htmlFor="target">Deployment Target</Label>
              <NativeSelect
                id="target"
                value={target || targets.data?.[0]?.id || ""}
                onChange={(e) => setTarget(e.target.value)}
              >
                {targets.data?.map((t) => (
                  <option value={t.id} key={t.id}>
                    {t.name} · {t.environment}
                  </option>
                ))}
              </NativeSelect>
            </div>
          </div>
          <ErrorBox error={targets.error} />
        </section>
        <section className="panel">
          <div className="section-heading">
            <h2>Web Components</h2>
            <Button
              variant="outline"
              type="button"

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
              <Plus size={16} /> Add Component
            </Button>
          </div>
          {components.map((c, i) => (
            <div className="component-form" key={c.editorId}>
              <div className="section-heading">
                <span className="small">
                  <ComponentIcon size={16} /> Component {i + 1}
                </span>
                {components.length > 1 && (
                  <Button
                    variant="ghost"
                    size="icon"
                    type="button"

                    aria-label={`Remove Component ${i + 1}`}
                    onClick={() =>
                      setComponents(components.filter((_, n) => n !== i))
                    }
                  >
                    <X size={16} />
                  </Button>
                )}
              </div>
              <div className="form-grid">
                <div>
                  <Label htmlFor={`name-${i}`}>Component Name</Label>
                  <Input
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
                  <Label htmlFor={`adopt-${i}`}>Resource Ownership</Label>
                  <NativeSelect
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
                                hostPort: r ? 0 : v.hostPort,
                                env: {},
                                services: {},
                                serviceEndpoints: {},
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
                      ?.filter(
                        (r) =>
                          !r.applicationId &&
                          r.image &&
                          r.artifactKind === "image",
                      )
                      .map((r) => (
                        <option key={r.id} value={r.id}>
                          Observe {r.name}
                        </option>
                      ))}
                  </NativeSelect>
                </div>
                <div>
                  <Label htmlFor={`image-${i}`}>Container Image</Label>
                  <Input
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
                    <Label htmlFor={`port-${i}`}>Internal Port</Label>
                    <Input
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
                    <Label htmlFor={`instances-${i}`}>Instances</Label>
                    <Input
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
                  <Label htmlFor={`host-port-${i}`}>Host Port</Label>
                  <Input
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
                    0 adds no host-port mapping. Existing platform routing
                    remains. A fixed host port requires one instance.
                  </small>
                </div>
              </div>
              <details className="advanced-configuration">
                <summary>Environment and Service Connections</summary>
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
              </details>
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
          <Button
            variant="default"

            disabled={
              !canOperate ||
              !selected ||
              mutation.isPending ||
              components.some((c) => invalid[c.editorId])
            }
          >
            {mutation.isPending ? "Creating…" : "Create Application"}
          </Button>
        </div>
      </form>
    </>
  );
}
