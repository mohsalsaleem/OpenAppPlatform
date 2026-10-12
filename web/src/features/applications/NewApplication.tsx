import { HostPortFields, invalidHostPort } from "./HostPortFields";
import { Disclosure } from "@/components/Disclosure";
import { OperatorConfiguration } from "./OperatorConfiguration";
import { StorageAndLimits, invalidStorageAndLimits } from "./StorageAndLimits";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
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
  const [portMapping, setPortMapping] = useState<Record<string, boolean>>({});
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
    if (
      !selected ||
      components.some(
        (c) =>
          invalid[c.editorId] ||
          invalidStorageAndLimits(c) ||
          invalidHostPort(c, !!portMapping[c.editorId]),
      )
    )
      return;
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
              <Select
                value={target || targets.data?.[0]?.id || ""}
                onValueChange={(value) => setTarget(value)}
              >
                <SelectTrigger id="target" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper">
                  {targets.data?.map((t) => (
                    <SelectItem value={t.id} key={t.id}>
                      {t.name} · {t.environment}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
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
                  <Label htmlFor={`kind-${i}`}>Component Type</Label>
                  <Select
                    value={c.kind}
                    disabled={!!c.resourceId}
                    onValueChange={(value) =>
                      setComponents(
                        components.map((v, n) =>
                          n === i
                            ? {
                                ...v,
                                kind: value as Component["kind"],
                                port: value === "worker" ? 0 : 80,
                                hostPort: 0,
                                healthCheck:
                                  value === "worker"
                                    ? { mode: "image" }
                                    : undefined,
                              }
                            : v,
                        ),
                      )
                    }
                  >
                    <SelectTrigger id={`kind-${i}`} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent position="popper">
                      <SelectItem value="web">Web Service</SelectItem>
                      <SelectItem
                        value="worker"
                        disabled={!capabilities.data?.workers}
                      >
                        Long-Running Worker
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  {c.kind === "worker" && (
                    <small className="muted">
                      Use an image with a worker entrypoint. Workers have no
                      public endpoint; readiness uses the running process or an
                      image health check.
                    </small>
                  )}
                </div>
                <div>
                  <Label htmlFor={`adopt-${i}`}>Resource Ownership</Label>
                  <Select
                    disabled={c.kind === "worker"}
                    value={c.resourceId || "new"}
                    onValueChange={(value) => {
                      setPortMapping((current) => ({
                        ...current,
                        [c.editorId]: false,
                      }));
                      setInvalid((current) => ({
                        ...current,
                        [c.editorId]: false,
                      }));
                      const r = resources.data?.find((r) => r.id === value);
                      setComponents(
                        components.map((v, n) =>
                          n === i
                            ? {
                                ...v,
                                resourceId: value === "new" ? "" : value,
                                image: r?.image || v.image,
                                instances: r ? 1 : v.instances,
                                hostPort: r ? 0 : v.hostPort,
                                volumes: r ? [] : v.volumes,
                                limits: r ? undefined : v.limits,
                                env: {},
                                secretRefs: {},
                                services: {},
                                serviceEndpoints: {},
                                port: r?.port || v.port,
                              }
                            : v,
                        ),
                      );
                    }}
                  >
                    <SelectTrigger id={`adopt-${i}`} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent position="popper">
                      <SelectItem value="new">
                        Create a new managed resource on deployment
                      </SelectItem>
                      {resources.data
                        ?.filter(
                          (r) =>
                            !r.applicationId &&
                            r.image &&
                            r.artifactKind === "image",
                        )
                        .map((r) => (
                          <SelectItem key={r.id} value={r.id}>
                            Observe {r.name}
                          </SelectItem>
                        ))}
                    </SelectContent>
                  </Select>
                </div>
                {c.resourceId ? (
                  <OperatorConfiguration component={c} target={selected} />
                ) : (
                  <>
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
                      {c.kind !== "worker" && (
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
                      )}
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
                                n === i
                                  ? { ...v, instances: +e.target.value }
                                  : v,
                              ),
                            )
                          }
                        />
                      </div>
                    </div>
                    <HostPortFields
                      component={c}
                      enabled={!!portMapping[c.editorId]}
                      onEnabledChange={(enabled) =>
                        setPortMapping({
                          ...portMapping,
                          [c.editorId]: enabled,
                        })
                      }
                      onPortChange={(port) =>
                        setComponents(
                          components.map((v, n) =>
                            n === i ? { ...v, hostPort: port } : v,
                          ),
                        )
                      }
                    />
                  </>
                )}
              </div>
              {!c.resourceId && (
                <>
                  <StorageAndLimits
                    component={c}
                    capabilities={capabilities.data}
                    onChange={(next) =>
                      setComponents(
                        components.map((v, n) =>
                          n === i ? { ...v, ...next } : v,
                        ),
                      )
                    }
                  />
                </>
              )}
              {!c.resourceId && (
                <Disclosure
                  className="advanced-configuration"
                  title={<>Environment and Service Connections</>}
                >
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
                </Disclosure>
              )}
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
          <Button asChild variant="outline">
            <Link to="/">Cancel</Link>
          </Button>
          <Button
            variant="default"

            disabled={
              !canOperate ||
              !selected ||
              mutation.isPending ||
              components.some(
                (c) =>
                  invalid[c.editorId] ||
                  invalidStorageAndLimits(c) ||
                  invalidHostPort(c, !!portMapping[c.editorId]),
              )
            }
          >
            {mutation.isPending ? "Creating…" : "Create Application"}
          </Button>
        </div>
      </form>
    </>
  );
}
