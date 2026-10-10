import type { ReadResponse } from "../../api";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/native-select";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { unwrap, api } from "../../api";
import { ErrorBox } from "../../components/Feedback";
type Member = ReadResponse<"/api/v1/access/members">[number];

function auditAction(action: string) {
  if (action === "auth.login") return "Sign In";
  if (action.startsWith("github.push")) return "GitHub Push";
  const [method, path = ""] = action.split(" ");
  if (path.endsWith("/auth/setup")) return "Create Owner Account";
  if (path.endsWith("/auth/invitations/accept")) return "Accept Invitation";
  if (path.endsWith("/auth/login")) return "Sign In";
  if (path.endsWith("/auth/logout")) return "Sign Out";
  if (path.includes("/access/invitations")) return "Create Invitation";
  if (path.includes("/access/credentials"))
    return method === "DELETE" ? "Revoke Agent Token" : "Create Agent Token";
  if (path.includes("/access/members")) return "Update Member Access";
  if (path.endsWith("/configuration-restores"))
    return "Restore Saved Configuration";
  if (path.endsWith("/rollbacks")) return "Roll Back Images";
  if (path.endsWith("/restarts")) return "Restart Instance";
  if (path.endsWith("/deployments")) return "Deploy Application";
  if (path.endsWith("/scale-down")) return "Retire Instances";
  if (path.endsWith("/control")) return "Update Deployment Tracking";
  if (path.includes("/source")) return "Update GitHub Workflow";
  if (path.includes("/targets")) return "Update Deployment Target";
  if (path.includes("/application-groups"))
    return "Update Environment Organization";
  if (path.includes("/applications"))
    return method === "POST"
      ? "Create or Manage Application"
      : "Update Application";
  return "Workspace Action";
}
export function Access() {
  const [section, setSection] = useState("members");
  const client = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<"viewer" | "operator">("viewer");
  const [credentialName, setCredentialName] = useState("");
  const [scope, setScope] = useState<"read" | "operate">("read");
  const [applicationId, setApplicationId] = useState("");
  const [issued, setIssued] = useState("");
  const members = useQuery({
    queryKey: ["members"],
    queryFn: () => api.GET("/api/v1/access/members").then(unwrap),
  });
  const credentials = useQuery({
    queryKey: ["credentials"],
    queryFn: () => api.GET("/api/v1/access/credentials").then(unwrap),
  });
  const audit = useQuery({
    queryKey: ["audit"],
    queryFn: () => api.GET("/api/v1/access/audit").then(unwrap),
    refetchInterval: 10000,
  });
  const apps = useQuery({
    queryKey: ["applications"],
    queryFn: () => api.GET("/api/v1/applications").then(unwrap),
  });
  const invite = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/access/invitations", { body: { email, role } })
        .then(unwrap),
    onSuccess: (v) => {
      setIssued(`Invitation Code: ${v.invite}`);
      setEmail("");
      client.invalidateQueries({ queryKey: ["audit"] });
    },
  });
  const issue = useMutation({
    mutationFn: () =>
      api
        .POST("/api/v1/access/credentials", {
          body: { name: credentialName, scope, applicationId },
        })
        .then(unwrap),
    onSuccess: (v) => {
      setIssued(`Agent token: ${v.token}`);
      setCredentialName("");
      client.invalidateQueries({ queryKey: ["credentials"] });
      client.invalidateQueries({ queryKey: ["audit"] });
    },
  });
  const update = useMutation({
    mutationFn: (m: Member) =>
      api
        .PATCH("/api/v1/access/members/{id}", {
          params: { path: { id: m.id! } },
          body: { role: m.role as "viewer" | "operator", active: m.active },
        })
        .then(unwrap),
    onSuccess: () => client.invalidateQueries({ queryKey: ["members"] }),
  });
  const revoke = useMutation({
    mutationFn: (id: string) =>
      api
        .DELETE("/api/v1/access/credentials/{id}", {
          params: { path: { id: id! } },
        })
        .then(unwrap),
    onSuccess: () => client.invalidateQueries({ queryKey: ["credentials"] }),
  });
  return (
    <Tabs value={section} onValueChange={setSection}>
      <div className="page-heading">
        <div>
          <h1>Workspace Settings</h1>
          <p className="muted">
            Workspace Access · manage members, agent tokens and audit history.
          </p>
        </div>
      </div>
      <TabsList
        variant="line"
        aria-label="Workspace Access"
        className="w-full justify-start border-b rounded-none mb-4"
      >
        <TabsTrigger value="members">Members</TabsTrigger>
        <TabsTrigger value="agents">Agent Tokens</TabsTrigger>
        <TabsTrigger value="audit">Audit Log</TabsTrigger>
      </TabsList>
      <ErrorBox
        error={
          members.error ||
          credentials.error ||
          audit.error ||
          invite.error ||
          issue.error ||
          update.error ||
          revoke.error
        }
      />
      {issued && (
        <section className="panel">
          <h2>Save This Code</h2>
          <p className="muted">
            Shown once here. Share invitation codes privately; store agent
            tokens in your secret store.
          </p>
          <pre className="one-time-secret">{issued}</pre>
          <Button variant="outline" onClick={() => setIssued("")}>
            Dismiss Code
          </Button>
        </section>
      )}
      <TabsContent
        value="members"
        forceMount
        hidden={section !== "members"}
        asChild
      >
        <section className="panel">
          <h2>Members</h2>
          {members.data?.map((m) => (
            <div className="step" key={m.id}>
              <div className="grow">
                <strong>{m.name}</strong>
                <p className="small muted">
                  {m.email} · {m.active ? "Active" : "Disabled"}
                </p>
              </div>
              {m.role === "owner" ? (
                <span className="environment">Owner</span>
              ) : (
                <>
                  <NativeSelect
                    className="access-select"
                    aria-label={`Role for ${m.name}`}
                    value={m.role}
                    disabled={update.isPending}
                    onChange={(e) =>
                      update.mutate({ ...m, role: e.target.value })
                    }
                  >
                    <option value="viewer">Viewer</option>
                    <option value="operator">Operator</option>
                  </NativeSelect>
                  <Button
                    variant="outline"

                    disabled={update.isPending}
                    onClick={() => update.mutate({ ...m, active: !m.active })}
                  >
                    {m.active ? "Disable" : "Enable"} {m.name}
                  </Button>
                </>
              )}
            </div>
          ))}
          <form
            className="form-grid configuration-fields"
            onSubmit={(e) => {
              e.preventDefault();
              invite.mutate();
            }}
          >
            <div>
              <Label htmlFor="invite-email">Invite Email</Label>
              <Input
                id="invite-email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </div>
            <div>
              <Label htmlFor="invite-role">Member Role</Label>
              <NativeSelect
                id="invite-role"
                value={role}
                onChange={(e) =>
                  setRole(e.target.value as "viewer" | "operator")
                }
              >
                <option value="viewer">Viewer · read only</option>
                <option value="operator">Operator · manage applications</option>
              </NativeSelect>
            </div>
            <div className="form-actions">
              <Button variant="default" disabled={invite.isPending}>
                Create Invitation
              </Button>
            </div>
          </form>
        </section>
      </TabsContent>
      <TabsContent
        value="agents"
        forceMount
        hidden={section !== "agents"}
        asChild
      >
        <section className="panel">
          <h2>Agent Tokens</h2>
          <p className="muted">
            Each token is limited to one application and expires after 30 days.
          </p>
          <form
            className="form-grid configuration-fields"
            onSubmit={(e) => {
              e.preventDefault();
              issue.mutate();
            }}
          >
            <div>
              <Label htmlFor="credential-name">Token Name</Label>
              <Input
                id="credential-name"
                value={credentialName}
                onChange={(e) => setCredentialName(e.target.value)}
                maxLength={80}
                required
              />
            </div>
            <div>
              <Label htmlFor="credential-app">Application Access</Label>
              <NativeSelect
                id="credential-app"
                value={applicationId}
                onChange={(e) => setApplicationId(e.target.value)}
                required
              >
                <option value="">Choose application</option>
                {apps.data?.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.manifest.name}
                  </option>
                ))}
              </NativeSelect>
            </div>
            <div>
              <Label htmlFor="credential-scope">Agent Scope</Label>
              <NativeSelect
                id="credential-scope"
                value={scope}
                onChange={(e) => setScope(e.target.value as "read" | "operate")}
              >
                <option value="read">Read</option>
                <option value="operate">Operate · changes allowed</option>
              </NativeSelect>
            </div>
            <div className="form-actions">
              <Button variant="default" disabled={issue.isPending}>
                Create Agent Token
              </Button>
            </div>
          </form>
          {credentials.data?.map((c) => (
            <div className="step" key={c.id}>
              <div className="grow">
                <strong>{c.name}</strong>
                <p className="small muted">
                  {c.scope} ·{" "}
                  {apps.data?.find((a) => a.id === c.applicationId)?.manifest
                    .name ?? c.applicationId}{" "}
                  · expires {new Date(c.expiresAt).toLocaleDateString()}
                </p>
              </div>
              <Button
                variant="outline"

                disabled={c.revoked || revoke.isPending}
                onClick={() => revoke.mutate(c.id)}
              >
                {c.revoked ? "Revoked" : "Revoke"} {c.name}
              </Button>
            </div>
          ))}
        </section>
      </TabsContent>
      <TabsContent
        value="audit"
        forceMount
        hidden={section !== "audit"}
        asChild
      >
        <section className="panel">
          <h2>Audit Log</h2>
          <p className="muted">
            Latest 100 actions and request outcomes. Open application Activity
            for deployment progress.
          </p>
          {audit.data?.map((event) => (
            <div className="step" key={event.id}>
              <div className="grow">
                <strong>{auditAction(event.action)}</strong>
                <details>
                  <summary>Request Details</summary>
                  <code>{event.action}</code>
                  <p className="small">
                    Event {event.id} · Actor {event.subject} · HTTP{" "}
                    {event.status || "pending"}
                  </p>
                </details>
                <p className="small muted">
                  {event.kind === "agent"
                    ? `Agent: ${event.credentialName || event.subject || "Unnamed Agent"}`
                    : event.subject || "Unknown Actor"}{" "}
                  · {new Date(event.createdAt).toLocaleString()}
                </p>
              </div>
              <span className="environment">
                {!event.status
                  ? "Pending"
                  : event.status === 202
                    ? "Accepted"
                    : event.status >= 500
                      ? "Failed"
                      : event.status < 400
                        ? "Succeeded"
                        : "Rejected"}
              </span>
            </div>
          ))}
        </section>
      </TabsContent>
    </Tabs>
  );
}
