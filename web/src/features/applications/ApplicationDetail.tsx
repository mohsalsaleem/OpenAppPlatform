import { FieldSet, FieldLegend } from "@/components/ui/field";
import { Disclosure } from "@/components/Disclosure";
import { DatabaseLinkDialog } from "./DatabaseLinkDialog";
import { Endpoints } from "./Endpoints";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { useApplicationEvents } from "@/lib/use-application-events";
import { consumeSnapshots } from "@/lib/streams";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  replicaSummary,
  blockedComponents,
  releaseOperation,
} from "./runtimeSummary";
import { ConfigurationRestoreReview } from "./ConfigurationRestoreReview";
import { healthCheckSummary } from "./HealthCheckFields";
import { ReleaseControls } from "./ReleaseControls";
import { RollbackReview } from "./RollbackReview";
import { ReleaseComparison } from "./ReleaseComparison";
import { WorkflowOwnership } from "./WorkflowOwnership";
import { EnvironmentNavigation } from "./EnvironmentNavigation";
import { SourceEvents } from "./SourceEvents";
import { useCanOperate, useIsOwner } from "../../access";
import { useState, useEffect, useRef } from "react";
import { Link, useParams, useLocation } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowUpRight,
  Component as ComponentIcon,
  Terminal,
} from "lucide-react";
import { unwrap, api, type Application, type Instance } from "../../api";
import { DeploymentDialog } from "../../components/DeploymentDialog";
import { ScaleDownDialog } from "./ScaleDownDialog";
import { RecoveryPanel } from "./RecoveryPanel";
import { ConfigurationEditor } from "./ConfigurationEditor";
import { Status } from "../../components/Status";
import { ErrorBox, Loading } from "../../components/Feedback";
const when = (s: string) =>
  new Date(s).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
export function ApplicationDetail() {
  const canOperate = useCanOperate();
  const isOwner = useIsOwner();
  const [linkDatabase, setLinkDatabase] = useState(false);
  const { id } = useParams();
  const client = useQueryClient();
  const live = useApplicationEvents(id);
  const [handoff, setHandoff] = useState("");
  const location = useLocation();
  const [tab, setTab] = useState("overview");
  useEffect(() => {
    if (new URLSearchParams(location.search).get("tab") === "deployments") setTab("deployments");
  }, [location.search, location.key]);
  const [scaleReview, setScaleReview] = useState<{
    component: string;
    instances: number;
    version: number;
  } | null>(null);
  const [review, setReview] = useState<Application | null>(null);
  const [selectedComponents, setSelectedComponents] = useState<string[]>([]);
  const [logs, setLogs] = useState("");
  const [logLoading, setLogLoading] = useState(false);
  const logRequest = useRef(0);
  const [logInstance, setLogInstance] = useState("");
  const [logError, setLogError] = useState("");
  const [restartReview, setRestartReview] = useState<Instance | null>(null);
  const app = useQuery({
    queryKey: ["application", id],
    queryFn: () =>
      api
        .GET("/api/v1/applications/{id}", { params: { path: { id: id! } } })
        .then(unwrap),
  });
  const releases = useQuery({
    queryKey: ["deployments", id],
    queryFn: () =>
      api
        .GET("/api/v1/applications/{id}/deployments", {
          params: { path: { id: id! } },
        })
        .then(unwrap),
    refetchInterval: live ? false : 2000,
  });
  const scrolledOperation = useRef("");
  useEffect(() => {
    if (tab !== "deployments" || !location.hash.startsWith("#operation-") || scrolledOperation.current === location.key) return;
    const element = document.getElementById(location.hash.slice(1));
    if (element) {
      element.scrollIntoView({ block: "start" });
      scrolledOperation.current = location.key;
    }
  }, [tab, releases.data, location.hash, location.key]);
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api.GET("/api/v1/targets").then(unwrap),
  });
  const instances = useQuery({
    queryKey: ["instances", id],
    queryFn: () =>
      api
        .GET("/api/v1/applications/{id}/instances", {
          params: { path: { id: id! } },
        })
        .then(unwrap),
    refetchInterval: 5000,
  });
  useEffect(() => {
    const latest = releases.data?.[0];
    if (latest?.state === "succeeded" && latest.operation === "scale-down") {
      client.invalidateQueries({ queryKey: ["application", id] });
      client.invalidateQueries({ queryKey: ["instances", id] });
    }
  }, [releases.data?.[0]?.id, releases.data?.[0]?.state, client, id]);
  const deploy = useMutation({
    mutationFn: (definitionVersion: number) =>
      api
        .POST("/api/v1/applications/{id}/deployments", {
          params: {
            path: { id: id! },
            header: { "Idempotency-Key": crypto.randomUUID() },
          },
          body: {
            expectedVersion: definitionVersion,
            components: selectedComponents,
          },
        })
        .then(unwrap),
    onSuccess: () => {
      setReview(null);
      setTab("deployments");
      client.invalidateQueries({ queryKey: ["deployments", id] });
    },
  });
  const capabilities = useQuery({
    queryKey: ["capabilities", app.data?.manifest.targetId],
    queryFn: () =>
      api
        .GET("/api/v1/targets/{id}/capabilities", {
          params: { path: { id: app.data!.manifest.targetId! } },
        })
        .then(unwrap),
    enabled: !!app.data,
  });
  const restart = useMutation({
    mutationFn: ({
      instance,
      version,
    }: {
      instance: Instance;
      version: number;
    }) =>
      api
        .POST("/api/v1/applications/{id}/restarts", {
          params: {
            path: { id: id! },
            header: { "Idempotency-Key": crypto.randomUUID() },
          },
          body: {
            component: instance.component,
            ordinal: instance.ordinal,
            expectedVersion: version,
          },
        })
        .then(unwrap),
    onSuccess: () => {
      setRestartReview(null);
      setTab("deployments");
      client.invalidateQueries({ queryKey: ["deployments", id] });
      client.invalidateQueries({ queryKey: ["instances", id] });
    },
  });
  const enableManagement = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/applications/{id}/management", {
          params: { path: { id: id! } },
          body: {
            component: handoff,
            expectedVersion: app.data!.version,
          },
        })
        .then(unwrap),
    onSuccess: () => {
      setHandoff("");
      client.invalidateQueries({ queryKey: ["application", id] });
    },
  });
  async function showLogs(component: string, ordinal = 1) {
    const request = ++logRequest.current;
    setTab("logs");
    setLogLoading(true);
    setLogError("");
    setLogs("");
    setLogInstance(`${component} / ${ordinal}`);
    try {
      const x = await api
        .GET("/api/v1/applications/{id}/logs/{component}", {
          params: {
            path: { id: id!, component: component! },
            query: { ordinal: ordinal },
          },
        })
        .then(unwrap);
      if (request === logRequest.current) setLogs(x.logs);
    } catch (e) {
      if (request === logRequest.current) setLogError((e as Error).message);
    } finally {
      if (request === logRequest.current) setLogLoading(false);
    }
  }
  useEffect(() => {
    if (tab !== "logs" || !logInstance || !id) return;
    const [component, ordinal] = logInstance.split(" / ");
    const stop = new AbortController();
    const request = logRequest.current;
    void consumeSnapshots(
      `/applications/${encodeURIComponent(id)}/logs/${encodeURIComponent(component)}/stream?ordinal=${ordinal}`,
      stop.signal,
      (value) => {
        if (request !== logRequest.current) return;
        setLogs((value as { logs: string }).logs);
        setLogLoading(false);
        setLogError("");
      },
      () => {},
    );
    return () => stop.abort();
  }, [id, tab, logInstance]);
  if (app.isPending) return <Loading />;
  if (!app.data) return <ErrorBox error={app.error} />;
  const a = app.data;
  const latest = releases.data?.[0];
  const observed = a.manifest.components.every(
    (c) => c.management === "observe",
  );
  const active = releases.data?.some(
    (release) =>
      ["queued", "running", "attention"].includes(release.state) ||
      (release.state === "abandoned" && !release.control?.resolvedAt),
  );
  const blocked = blockedComponents(a.manifest.components, releases.data || []);
  const available = a.manifest.components.filter(
    (component) =>
      component.management !== "observe" && !blocked.has(component.name),
  );
  return (
    <>
      <Link className="back" to="/">
        <ArrowLeft size={16} /> Applications
      </Link>
      <div className="page-heading">
        <div>
          <div className="title-line">
            <h1>{a.manifest.name}</h1>
            <EnvironmentNavigation applicationId={id!} />
          </div>
          <p className="muted">
            {a.manifest.components.length} component
            {a.manifest.components.length === 1 ? "" : "s"} ·{" "}
            {targets.data?.find((t) => t.id === a.manifest.targetId)?.name ||
              a.manifest.targetId}
          </p>
          <p className="runtime-summary" role="status">
            {replicaSummary(
              a.manifest.components,
              instances.data,
              !!instances.error,
            )}
          </p>
        </div>
        {a.manifest.components.every((c) => c.management === "observe") ? (
          <span className="status neutral">Managed Externally</span>
        ) : (
          <Button
            variant="default"

            disabled={
              !canOperate || !available.length || !capabilities.data?.standard
            }
            onClick={() => {
              setSelectedComponents(available.map((c) => c.name));
              deploy.reset();
              setReview(structuredClone(a));
            }}
          >
            <ArrowUpRight size={17} />
            {!available.length &&
            latest?.state === "abandoned" &&
            !latest.control?.resolvedAt
              ? "Reconciliation Required"
              : !available.length && active
                ? "Deployment in Progress"
                : "Review Deployment"}
          </Button>
        )}
      </div>
      {active && (
        <div role="status" className="operation-notice">
          {latest?.state === "abandoned"
            ? "Tracking is closed. Review the platform’s current state in Activity before another deployment."
            : latest?.state === "attention"
              ? "This deployment needs review. Open Activity to inspect the affected instances."
              : "A deployment is in progress. Follow its status in Activity."}
        </div>
      )}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList
          variant="line"
          aria-label="Application views"
          className="w-full justify-start border-b rounded-none mb-4"
        >
          {Object.entries({
            overview: "Overview",
            deployments: "Activity",
            endpoints: "Endpoints",
            configuration: "Settings",
            logs: "Logs",
          }).map(([value, label]) => (
            <TabsTrigger key={value} value={value}>
              {label}
            </TabsTrigger>
          ))}
        </TabsList>
        <ErrorBox error={releases.error || logError} />
        <TabsContent value="overview" forceMount hidden={tab !== "overview"}>
          <ErrorBox error={capabilities.error} />
          <section className="panel">
            <div className="section-heading">
              <h2>Components</h2>
              {isOwner && capabilities.data?.databaseLinks && (
                <Button
                  variant="outline"
                  disabled={!!active || a.manifest.components.length >= 16}
                  onClick={() => setLinkDatabase(true)}
                >
                  Link Database
                </Button>
              )}
              <Button
                variant="outline"
                disabled={instances.isFetching}
                onClick={() => instances.refetch()}
              >
                Refresh Health
              </Button>
            </div>
            <ErrorBox error={instances.error} />
            {instances.isPending && <Loading />}
            {a.manifest.components.map((c) => {
              const rows =
                instances.data?.filter((v) => v.component === c.name) || [];
              const primary = rows.find((v) => !v.retired);
              const expanded = c.instances > 1 || rows.some((v) => v.retired);
              return (
                <div className="component-group" key={c.name}>
                  <div className="component-row" key={c.name}>
                    <span className="app-icon">
                      <ComponentIcon size={20} />
                    </span>
                    <div className="grow">
                      <strong>{c.name}</strong>
                      {c.kind === "database" && (
                        <p className="small muted">
                          <code className="image-ref">
                            {c.databaseEndpoint}
                          </code>
                        </p>
                      )}
                      <div className="component-badges">
                        <Badge variant="secondary">
                          {c.kind === "database"
                            ? "PostgreSQL"
                            : c.kind === "worker"
                              ? "Worker"
                              : "Web Service"}
                        </Badge>
                        <Badge variant="outline">
                          {c.management === "observe"
                            ? "Observe-only"
                            : "OAP Managed"}
                        </Badge>
                        <span className="small muted">
                          {c.instances} configured
                        </span>
                      </div>
                      <Disclosure title={<>Image Details</>}>
                        {c.healthCheck && (
                          <p className="small muted">
                            {healthCheckSummary(c.healthCheck)}
                          </p>
                        )}
                        {c.kind === "web" && (
                          <p className="small muted">Internal Port: {c.port}</p>
                        )}
                        {instances.data
                          ?.filter((v) => v.component === c.name && !v.retired)
                          .map((v) => (
                            <div key={v.ordinal}>
                              <span className="small">
                                Running · instance {v.ordinal}
                              </span>
                              <code className="image-ref">
                                {v.resource?.image || "Image unavailable"}
                              </code>
                            </div>
                          ))}
                        <span className="small">
                          {c.management === "observe"
                            ? "Observed Image Snapshot"
                            : "Saved for the next deployment"}
                        </span>
                        <code className="image-ref">
                          {c.image || "Managed by the platform"}
                        </code>
                      </Disclosure>
                      {instances.data?.some(
                        (v) =>
                          !v.retired &&
                          v.component === c.name &&
                          v.resource?.image &&
                          v.resource.image !== c.image,
                      ) &&
                        c.management !== "observe" && (
                          <p className="configuration-drift" role="status">
                            Saved image differs from the running image. Review
                            before deploying.
                          </p>
                        )}
                    </div>

                    {c.management === "observe" &&
                      instances.data?.find((i) => i.component === c.name)
                        ?.resource?.artifactKind === "image" &&
                      !instances.data
                        ?.find((i) => i.component === c.name)
                        ?.resource?.description?.startsWith(
                          "OpenAppPlatform:",
                        ) &&
                      a.manifest.targetId &&
                      capabilities.data?.managementHandoff &&
                      c.kind !== "database" && (
                        <Button
                          variant="outline"

                          onClick={() => setHandoff(c.name)}
                          disabled={!canOperate || !!active}
                        >
                          Enable Management for {c.name}
                        </Button>
                      )}
                    {capabilities.data?.retirement &&
                      c.instances > 1 &&
                      !c.resourceId && (
                        <Button
                          variant="outline"

                          disabled={!canOperate || !!active}
                          onClick={() =>
                            setScaleReview({
                              component: c.name,
                              instances: c.instances,
                              version: a.version,
                            })
                          }
                        >
                          Scale Down {c.name}
                        </Button>
                      )}
                    {!expanded && primary ? (
                      <div className="component-actions">
                        <Status value={primary.status} />
                        {primary.resource?.url &&
                          /^https?:\/\//.test(primary.resource.url) && (
                            <Button asChild variant="outline" size="sm">
                              <a
                                href={primary.resource.url}
                                target="_blank"
                                rel="noreferrer"
                              >
                                Open
                              </a>
                            </Button>
                          )}
                        <Button
                          variant="outline"
                          size="sm"
                          aria-label={`View ${c.name} instance ${primary.ordinal} logs`}
                          disabled={!primary.resourceId}
                          onClick={() => showLogs(c.name, primary.ordinal)}
                        >
                          <Terminal size={15} />
                          Logs
                        </Button>
                        {capabilities.data?.restart &&
                          c.kind !== "database" && (
                            <Button
                              variant="outline"
                              size="sm"
                              aria-label={`Restart ${c.name} / ${primary.ordinal}`}
                              disabled={
                                !canOperate ||
                                !!active ||
                                !primary.resourceId ||
                                primary.retired ||
                                c.management === "observe"
                              }
                              onClick={() => setRestartReview(primary)}
                            >
                              Restart
                            </Button>
                          )}
                      </div>
                    ) : (
                      <div className="component-actions">
                        <span className="small muted">
                          {replicaSummary(
                            [c],
                            instances.data,
                            !!instances.error,
                          )}
                        </span>
                        <Button
                          variant="outline"
                          size="sm"
                          aria-label={`View ${c.name} logs`}
                          disabled={!primary?.resourceId}
                          onClick={() => showLogs(c.name)}
                        >
                          <Terminal size={15} />
                          Logs
                        </Button>
                      </div>
                    )}
                  </div>
                  {!expanded && primary?.error && (
                    <p className="error-inline" role="alert">
                      {primary.error}
                    </p>
                  )}
                  <Disclosure
                    className="component-instance-details"
                    defaultOpen={expanded}
                    title={<>Instance Details for {c.name}</>}
                  >
                    {rows.map((instance) => (
                      <div
                        className="step"
                        key={`${instance.component}-${instance.ordinal}`}
                      >
                        <div className="grow">
                          <strong>
                            {instance.component} / {instance.ordinal}
                            {instance.retired
                              ? " · retained after retirement"
                              : ""}
                          </strong>
                          <p className="small muted">
                            Checked {when(instance.checkedAt)}
                          </p>
                          {instance.error && (
                            <p className="error-inline">{instance.error}</p>
                          )}
                          {instance.resource?.url &&
                            /^https?:\/\//.test(instance.resource.url) && (
                              <a
                                href={instance.resource.url}
                                target="_blank"
                                rel="noreferrer"
                              >
                                Open Service
                              </a>
                            )}
                        </div>
                        <Status value={instance.status} />
                        {capabilities.data?.restart &&
                          c.kind !== "database" && (
                            <Button
                              variant="outline"

                              disabled={
                                !canOperate ||
                                !!active ||
                                !instance.resourceId ||
                                instance.retired ||
                                a.manifest.components.find(
                                  (c) => c.name === instance.component,
                                )?.management === "observe"
                              }
                              onClick={() => setRestartReview(instance)}
                            >
                              Restart {instance.component} / {instance.ordinal}
                            </Button>
                          )}
                        <Button
                          variant="ghost"
                          size="icon"

                          aria-label={`View ${instance.component} instance ${instance.ordinal} logs`}
                          disabled={!instance.resourceId}
                          onClick={() =>
                            showLogs(instance.component, instance.ordinal)
                          }
                        >
                          <Terminal size={18} />
                        </Button>
                      </div>
                    ))}
                  </Disclosure>
                </div>
              );
            })}
          </section>
          <section className="panel release-banner">
            <div>
              <p className="eyebrow">LATEST DEPLOYMENT</p>
              <h2>
                {latest
                  ? `${releaseOperation(latest)} · ${latest.id.slice(0, 8)}`
                  : observed
                    ? "Observing Existing Services"
                    : "Ready for Your First Deployment"}
              </h2>
              <p className="muted">
                {latest
                  ? `${when(latest.createdAt)}${latest.source ? ` · ${latest.source.repository}@${latest.source.commit.slice(0, 8)}` : ""}`
                  : observed
                    ? "Health and logs are available. Workloads remain managed by your operator."
                    : "Your definition is saved. Deployment is a separate action."}
              </p>
            </div>
            {latest ? (
              <Status value={latest.state} />
            ) : (
              <span className="status neutral">
                {observed ? "Observe-only" : "Not deployed"}
              </span>
            )}
          </section>
        </TabsContent>
        <TabsContent
          value="deployments"
          forceMount
          hidden={tab !== "deployments"}
        >
          <SourceEvents
            applicationId={a.id}
            version={a.version}
            releases={releases.data ?? []}
          />
          <ReleaseComparison
            applicationId={a.id}
            releases={releases.data ?? []}
          />
          <section className="panel">
            <div className="section-heading">
              <h2>Release History</h2>
              <span className="small muted">
                Provider completion and workload state are tracked separately.
              </span>
            </div>
            {releases.data?.length === 0 && (
              <p className="muted">No deployments yet.</p>
            )}
            {releases.data?.map((d) => (
              <article className="release scroll-mt-4" id={`operation-${d.id}`} key={d.id}>
                <div className="section-heading">
                  <div>
                    <strong>
                      {releaseOperation(d)} · {d.id.slice(0, 8)}
                    </strong>
                    <p className="small muted">
                      {when(d.createdAt)} · definition v{d.definitionVersion}
                      {d.steps.some((step) => step.rollbackFrom)
                        ? ` · rollback from ${d.steps.find((step) => step.rollbackFrom)!.rollbackFrom!.slice(0, 8)}`
                        : ""}
                      {d.source
                        ? ` · ${d.source.repository}@${d.source.commit.slice(0, 8)}`
                        : ""}
                    </p>
                  </div>
                  <Status value={d.state} />
                </div>
                {d.manifest.components
                  .filter((c) => c.healthCheck)
                  .map((c) => (
                    <p className="small muted" key={`health-${c.name}`}>
                      {c.name} · {healthCheckSummary(c.healthCheck)}
                    </p>
                  ))}
                {d.manifest.components
                  .filter((c) => c.readiness || c.healthCheck?.mode === "http")
                  .map((c) => (
                    <p className="small muted" key={`readiness-${c.name}`}>
                      {c.name} readiness ·{" "}
                      {c.healthCheck?.mode === "http" ||
                      c.readiness?.requireHealthy
                        ? "Operator healthy status required"
                        : "Operator running/health status"}{" "}
                      · {c.readiness?.timeoutSeconds || 900}s observation
                      timeout
                    </p>
                  ))}
                {d.manifest.components
                  .filter((c) => c.dependsOn?.length)
                  .map((c) => (
                    <p className="small muted" key={`dependencies-${c.name}`}>
                      {c.name} waits for {c.dependsOn!.join(", ")}
                    </p>
                  ))}
                {d.steps.map((s) => (
                  <div className="step" key={`${s.component}-${s.ordinal}`}>
                    <div>
                      <strong>
                        {s.component} / {s.ordinal}
                        {s.action ? ` · ${s.action}` : ""}
                      </strong>
                      <Disclosure title={<>Instance Details</>}>
                        <p className="small muted">
                          {s.resourceId
                            ? `Resource ${s.resourceId}`
                            : "Resource not prepared"}
                          {s.observed ? ` · ${s.observed}` : ""}
                        </p>
                      </Disclosure>
                      {s.error && <p className="error-inline">{s.error}</p>}
                    </div>
                    <Status value={s.phase} />
                  </div>
                ))}
                {d.state === "succeeded" &&
                  (d.operation || "deploy") === "deploy" && (
                    <RollbackReview
                      application={a}
                      release={d}
                      onQueued={() => setTab("deployments")}
                    />
                  )}
                {d.state === "succeeded" &&
                  (d.operation || "deploy") === "deploy" &&
                  !observed && (
                    <ConfigurationRestoreReview application={a} release={d} />
                  )}
                {d.state === "attention" && <RecoveryPanel deployment={d} />}
                <ReleaseControls deployment={d} />
              </article>
            ))}
          </section>
        </TabsContent>
        <TabsContent value="endpoints">
          <Endpoints applicationId={id!} version={a.version} />
        </TabsContent>
        <TabsContent value="logs" forceMount hidden={tab !== "logs"}>
          <section className="panel">
            <div className="section-heading">
              <h2>Component Logs</h2>
              <div className="log-buttons">
                {instances.data
                  ?.filter((i) => i.resourceId)
                  .map((i) => (
                    <Button
                      variant="outline"

                      key={`${i.component}-${i.ordinal}`}
                      onClick={() => showLogs(i.component, i.ordinal)}
                    >
                      {i.component} / {i.ordinal}
                    </Button>
                  ))}
              </div>
            </div>
            <p className="small muted">
              Latest 100 lines
              {logInstance
                ? ` from ${logInstance}`
                : " from the selected instance"}
              . Logs can include application data.
            </p>
            <pre className="log-output">
              {logLoading
                ? "Fetching logs…"
                : logs ||
                  (logError
                    ? "Logs could not be loaded. Select the instance to retry."
                    : logInstance
                      ? "No log lines returned for this instance."
                      : "Select a deployed component to fetch its logs.")}
            </pre>
          </section>
        </TabsContent>
        {linkDatabase && (
          <DatabaseLinkDialog
            application={a}
            docker={
              targets.data?.find((t) => t.id === a.manifest.targetId)
                ?.operator === "docker"
            }
            onClose={() => setLinkDatabase(false)}
          />
        )}
        {handoff && (
          <DeploymentDialog onClose={() => setHandoff("")}>
            <h2 id="deploy-title">Enable Management for {handoff}?</h2>
            <p>
              Allow Open App Platform to deploy and restart this existing
              image-backed service. This saves permission only; it does not
              change or restart the workload.
            </p>
            <p className="muted">
              Source, variables, routes, volumes, and native operator triggers
              stay with the operator. Deploying can interrupt requests. An
              application can deploy once all its components support management.
            </p>
            <ErrorBox error={enableManagement.error} />
            <div className="form-actions">
              <Button
                variant="outline"

                disabled={enableManagement.isPending}
                onClick={() => setHandoff("")}
              >
                Cancel
              </Button>
              <Button
                variant="default"

                disabled={enableManagement.isPending}
                onClick={() => enableManagement.mutate()}
              >
                Confirm Management Handoff
              </Button>
            </div>
          </DeploymentDialog>
        )}
        <TabsContent
          value="configuration"
          forceMount
          hidden={tab !== "configuration"}
        >
          <ConfigurationEditor key={a.id} application={a} />
          <EnvironmentNavigation applicationId={id!} organization />
          <WorkflowOwnership application={a} capabilities={capabilities.data} />
        </TabsContent>
      </Tabs>
      {scaleReview && (
        <ScaleDownDialog
          applicationId={id!}
          component={scaleReview.component}
          instances={scaleReview.instances}
          version={scaleReview.version}
          onClose={() => setScaleReview(null)}
          onQueued={() => {
            setScaleReview(null);
            setTab("deployments");
          }}
        />
      )}
      {restartReview && (
        <DeploymentDialog onClose={() => setRestartReview(null)}>
          <h2 id="deploy-title">
            Restart {restartReview.component} / {restartReview.ordinal}?
          </h2>
          <p>
            This restarts the existing instance and can interrupt requests.
            Saved configuration changes are not applied. Other instances keep
            running.
          </p>
          <ErrorBox error={restart.error} />
          <div className="form-actions">
            <Button
              variant="outline"

              disabled={restart.isPending}
              onClick={() => setRestartReview(null)}
            >
              Cancel
            </Button>
            <Button
              variant="default"

              disabled={restart.isPending}
              onClick={() =>
                restart.mutate({ instance: restartReview, version: a.version })
              }
            >
              {restart.isPending ? "Queuing…" : "Restart Now"}
            </Button>
          </div>
        </DeploymentDialog>
      )}
      {review && (
        <DeploymentDialog onClose={() => setReview(null)}>
          <h2 id="deploy-title">Deploy {a.manifest.name}?</h2>
          <p>
            This starts standard deployments for {selectedComponents.length}{" "}
            component
            {selectedComponents.length === 1 ? "" : "s"} on{" "}
            <strong>
              {targets.data?.find((t) => t.id === review.manifest.targetId)
                ?.name || review.manifest.targetId}
            </strong>{" "}
            using definition v{review.version}.
          </p>
          <p className="muted">
            Existing managed instances may restart. Standard deployment can
            cause downtime. No databases or volumes will be removed.
          </p>
          {review.manifest.components.some((c) => c.kind === "database") && (
            <div
              role="region"
              aria-label="Databases Outside This Release"
              className="component-badges"
            >
              {review.manifest.components
                .filter((c) => c.kind === "database")
                .map((c) => (
                  <Badge key={c.name} variant="outline">
                    {c.name} · Operator Managed
                  </Badge>
                ))}
            </div>
          )}
          <FieldSet className="gap-3">
            <FieldLegend>Components to Deploy</FieldLegend>
            {review.manifest.components
              .filter((c) => c.management !== "observe")
              .map((c) => (
                <Label key={c.name}>
                  <Checkbox
                    disabled={blocked.has(c.name)}
                    checked={selectedComponents.includes(c.name)}
                    onCheckedChange={(checked) => {
                      deploy.reset();
                      setSelectedComponents((names) =>
                        checked === true
                          ? [...names, c.name]
                          : names.filter((name) => name !== c.name),
                      );
                    }}
                  />
                  {c.name}
                  {blocked.has(c.name) ? " · In Use" : ""}
                </Label>
              ))}
            {active && (
              <p className="text-sm text-muted-foreground">
                Components involved in another active release or its
                dependencies remain protected.
              </p>
            )}
          </FieldSet>
          <section
            className="deployment-images"
            aria-label="Deployment image changes"
          >
            <h3>Running → Proposed Images</h3>
            {review.manifest.components
              .filter((c) => selectedComponents.includes(c.name))
              .map((c) => (
                <div key={c.name}>
                  <strong>
                    {c.name} · {c.instances} instance
                    {c.instances === 1 ? "" : "s"}
                  </strong>
                  {instances.isFetching && !instances.data ? (
                    <p>Checking running images…</p>
                  ) : instances.error ? (
                    <p className="error-inline">
                      Running images unavailable. Refresh health before
                      deploying.
                    </p>
                  ) : (
                    <>
                      {instances.data
                        ?.filter((v) => v.component === c.name && !v.retired)
                        .map((v) => (
                          <div key={v.ordinal}>
                            <span className="small">
                              Current instance {v.ordinal}
                            </span>
                            <code className="image-ref">
                              {v.resource?.image || "Image unavailable"}
                            </code>
                          </div>
                        ))}
                      {!instances.isPending &&
                        !instances.data?.some(
                          (v) => v.component === c.name && !v.retired,
                        ) && (
                          <p className="small muted">No existing instance.</p>
                        )}
                    </>
                  )}
                  <span className="small">Proposed image</span>
                  <code className="image-ref">{c.image}</code>
                </div>
              ))}
          </section>
          {review.manifest.components
            .filter((c) => c.healthCheck)
            .map((c) => (
              <p className="small muted" key={`native-${c.name}`}>
                {c.name} · {healthCheckSummary(c.healthCheck)}
              </p>
            ))}
          {review.manifest.components
            .filter((c) => c.readiness || c.healthCheck?.mode === "http")
            .map((c) => (
              <p className="small muted" key={c.name}>
                {c.name}:{" "}
                {c.healthCheck?.mode === "http" || c.readiness?.requireHealthy
                  ? "requires operator-reported healthy status"
                  : "uses operator running/health status"}
                ; observation timeout {c.readiness?.timeoutSeconds || 900}s.
              </p>
            ))}
          {review.manifest.components
            .filter((c) => c.dependsOn?.length)
            .map((c) => (
              <p className="small muted" key={`dependencies-${c.name}`}>
                {c.name} waits for {c.dependsOn!.join(", ")}; all dependency
                replicas must pass readiness.
              </p>
            ))}
          <ErrorBox error={deploy.error} />
          <div className="form-actions">
            <Button
              variant="outline"

              onClick={() => setReview(null)}
              disabled={deploy.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="default"

              onClick={() => deploy.mutate(review.version)}
              disabled={
                !selectedComponents.length ||
                selectedComponents.some((name) => blocked.has(name)) ||
                deploy.isPending ||
                instances.isPending ||
                !!instances.error
              }
            >
              {deploy.isPending ? "Queuing…" : "Deploy Now"}
            </Button>
          </div>
        </DeploymentDialog>
      )}
    </>
  );
}
