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
export function Access() {
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
      setIssued(`Invitation code: ${v.invite}`);
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
          <h1>Workspace access</h1>
          <p className="muted">
            Invite members, scope agent access, and review activity.
          </p>
        </div>
      </div>
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
          <h2>Save this code</h2>
          <p className="muted">
            Shown once here. Share invitation codes privately; store agent
            tokens in your secret store.
          </p>
          <pre className="one-time-secret">{issued}</pre>
          <button className="secondary" onClick={() => setIssued("")}>
            Dismiss code
          </button>
        </section>
      )}
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
            <label htmlFor="invite-email">Invite email</label>
            <input
              id="invite-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </div>
          <div>
            <label htmlFor="invite-role">Member role</label>
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
              Create invitation
            </button>
          </div>
        </form>
      </section>
      <section className="panel">
        <h2>Agent credentials</h2>
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
            <label htmlFor="credential-name">Credential name</label>
            <input
              id="credential-name"
              value={credentialName}
              onChange={(e) => setCredentialName(e.target.value)}
              maxLength={80}
              required
            />
          </div>
          <div>
            <label htmlFor="credential-app">Application access</label>
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
            <label htmlFor="credential-scope">Agent scope</label>
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
              Create agent token
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
      <section className="panel">
        <h2>Audit trail</h2>
        <p className="muted">
          Latest 100 mutation attempts. Request bodies and secrets are excluded.
        </p>
        {audit.data?.map((event) => (
          <div className="step" key={event.id}>
            <div className="grow">
              <strong>{event.action}</strong>
              <p className="small muted">
                {event.subject}
                {event.kind === "agent"
                  ? ` · Agent: ${event.credentialName}`
                  : ""}{" "}
                · {new Date(event.createdAt).toLocaleString()}
              </p>
            </div>
            <span className="environment">{event.status || "Pending"}</span>
          </div>
        ))}
      </section>
    </>
  );
}
