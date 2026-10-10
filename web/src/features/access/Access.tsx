import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Application } from "../../api";
import { ErrorBox } from "../../components/Feedback";
type Member = {
  id: string;
  email: string;
  name: string;
  role: string;
  active: boolean;
};
type Credential = {
  id: string;
  name: string;
  scope: string;
  applicationId: string;
  expiresAt: string;
  revoked: boolean;
};
type Event = {
  credentialName: string;
  kind: string;
  id: string;
  subject: string;
  action: string;
  status: number;
  createdAt: string;
};
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
  const [role, setRole] = useState("viewer");
  const [credentialName, setCredentialName] = useState("");
  const [scope, setScope] = useState("read");
  const [applicationId, setApplicationId] = useState("");
  const [issued, setIssued] = useState("");
  const members = useQuery({
    queryKey: ["members"],
    queryFn: () => api<Member[]>("/access/members"),
  });
  const credentials = useQuery({
    queryKey: ["credentials"],
    queryFn: () => api<Credential[]>("/access/credentials"),
  });
  const audit = useQuery({
    queryKey: ["audit"],
    queryFn: () => api<Event[]>("/access/audit"),
    refetchInterval: 10000,
  });
  const apps = useQuery({
    queryKey: ["applications"],
    queryFn: () => api<Application[]>("/applications"),
  });
  const invite = useMutation({
    mutationFn: () =>
      api<{ invite: string }>("/access/invitations", {
        method: "POST",
        body: JSON.stringify({ email, role }),
      }),
    onSuccess: (v) => {
      setIssued(`Invitation Code: ${v.invite}`);
      setEmail("");
      client.invalidateQueries({ queryKey: ["audit"] });
    },
  });
  const issue = useMutation({
    mutationFn: () =>
      api<{ token: string }>("/access/credentials", {
        method: "POST",
        body: JSON.stringify({ name: credentialName, scope, applicationId }),
      }),
    onSuccess: (v) => {
      setIssued(`Agent token: ${v.token}`);
      setCredentialName("");
      client.invalidateQueries({ queryKey: ["credentials"] });
      client.invalidateQueries({ queryKey: ["audit"] });
    },
  });
  const update = useMutation({
    mutationFn: (m: Member) =>
      api(`/access/members/${m.id}`, {
        method: "PATCH",
        body: JSON.stringify({ role: m.role, active: m.active }),
      }),
    onSuccess: () => client.invalidateQueries({ queryKey: ["members"] }),
  });
  const revoke = useMutation({
    mutationFn: (id: string) =>
      api(`/access/credentials/${id}`, { method: "DELETE" }),
    onSuccess: () => client.invalidateQueries({ queryKey: ["credentials"] }),
  });
  return (
    <>
      <div className="page-heading">
        <div>
          <h1>Workspace Settings</h1>
          <p className="muted">
            Workspace Access · manage members, agent tokens and audit history.
          </p>
        </div>
      </div>
      <nav className="tabs" aria-label="Workspace Access">
        <button
          aria-current={section === "members" ? "page" : undefined}
          className={section === "members" ? "selected" : ""}
          onClick={() => setSection("members")}
        >
          Members
        </button>
        <button
          aria-current={section === "agents" ? "page" : undefined}
          className={section === "agents" ? "selected" : ""}
          onClick={() => setSection("agents")}
        >
          Agent Tokens
        </button>
        <button
          aria-current={section === "audit" ? "page" : undefined}
          className={section === "audit" ? "selected" : ""}
          onClick={() => setSection("audit")}
        >
          Audit Log
        </button>
      </nav>
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
          <button className="secondary" onClick={() => setIssued("")}>
            Dismiss Code
          </button>
        </section>
      )}
      <section className="panel" hidden={section !== "members"}>
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
                <select
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
                </select>
                <button
                  className="secondary"
                  disabled={update.isPending}
                  onClick={() => update.mutate({ ...m, active: !m.active })}
                >
                  {m.active ? "Disable" : "Enable"} {m.name}
                </button>
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
            <label htmlFor="invite-email">Invite Email</label>
            <input
              id="invite-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </div>
          <div>
            <label htmlFor="invite-role">Member Role</label>
            <select
              id="invite-role"
              value={role}
              onChange={(e) => setRole(e.target.value)}
            >
              <option value="viewer">Viewer · read only</option>
              <option value="operator">Operator · manage applications</option>
            </select>
          </div>
          <div className="form-actions">
            <button className="primary" disabled={invite.isPending}>
              Create Invitation
            </button>
          </div>
        </form>
      </section>
      <section className="panel" hidden={section !== "agents"}>
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
            <label htmlFor="credential-name">Token Name</label>
            <input
              id="credential-name"
              value={credentialName}
              onChange={(e) => setCredentialName(e.target.value)}
              maxLength={80}
              required
            />
          </div>
          <div>
            <label htmlFor="credential-app">Application Access</label>
            <select
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
            </select>
          </div>
          <div>
            <label htmlFor="credential-scope">Agent Scope</label>
            <select
              id="credential-scope"
              value={scope}
              onChange={(e) => setScope(e.target.value)}
            >
              <option value="read">Read</option>
              <option value="operate">Operate · changes allowed</option>
            </select>
          </div>
          <div className="form-actions">
            <button className="primary" disabled={issue.isPending}>
              Create Agent Token
            </button>
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
            <button
              className="secondary"
              disabled={c.revoked || revoke.isPending}
              onClick={() => revoke.mutate(c.id)}
            >
              {c.revoked ? "Revoked" : "Revoke"} {c.name}
            </button>
          </div>
        ))}
      </section>
      <section className="panel" hidden={section !== "audit"}>
        <h2>Audit Log</h2>
        <p className="muted">
          Latest 100 actions and request outcomes. Open application Activity for
          deployment progress.
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
    </>
  );
}
