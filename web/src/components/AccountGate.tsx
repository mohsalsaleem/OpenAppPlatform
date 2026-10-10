import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Layers3 } from "lucide-react";
import { api } from "../api";
import { ErrorBox } from "./Feedback";
export function AccountGate({
  setup,
  onConnected,
}: {
  setup: boolean;
  onConnected: () => void;
}) {
  const [inviteMode, setInviteMode] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [secret, setSecret] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const client = useQueryClient();
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      sessionStorage.removeItem("oap-token");
      const user = await api(
        `/auth/${setup ? "setup" : inviteMode ? "invitations/accept" : "login"}`,
        {
          method: "POST",
          headers: setup ? { Authorization: `Setup ${secret}` } : {},
          body: JSON.stringify({
            email,
            name,
            password,
            ...(inviteMode ? { invite: secret } : {}),
          }),
        },
      );
      setPassword("");
      setSecret("");
      client.setQueryData(["identity"], user);
      client.invalidateQueries({ queryKey: ["auth-status"] });
      onConnected();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="connect-page">
      <div className="connect-card">
        <div className="brand-icon">
          <Layers3 size={24} />
        </div>
        <p className="eyebrow">OPEN APP PLATFORM</p>
        <h1>
          {setup
            ? "Set up your workspace"
            : inviteMode
              ? "Join your workspace"
              : "Welcome back"}
        </h1>
        <p className="muted">
          {setup
            ? "Create the owner account for this self-hosted installation."
            : inviteMode
              ? "Use the invitation code shared by your workspace owner."
              : "Sign in to manage your applications."}
        </p>
        <form onSubmit={submit}>
          {(setup || inviteMode) && (
            <>
              <label htmlFor="account-name">Your name</label>
              <input
                id="account-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="name"
                maxLength={80}
                required
              />
            </>
          )}
          <label htmlFor="account-email">Email</label>
          <input
            id="account-email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="username"
            required
          />
          <label htmlFor="account-password">Password</label>
          <input
            id="account-password"
            type="password"
            minLength={setup || inviteMode ? 12 : undefined}
            maxLength={256}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete={
              setup || inviteMode ? "new-password" : "current-password"
            }
            required
          />
          {(setup || inviteMode) && (
            <>
              <label htmlFor="account-secret">
                {setup ? "Setup secret" : "Invitation code"}
              </label>
              <input
                id="account-secret"
                type="password"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                autoComplete="off"
                required
              />
              <small className="muted">
                {setup
                  ? "Use OAP_SETUP_TOKEN from your installation configuration. Passwords need at least 12 characters."
                  : "Invitation codes expire after 48 hours."}
              </small>
            </>
          )}
          <button className="primary" disabled={busy}>
            {busy
              ? "Please wait…"
              : setup
                ? "Create owner account"
                : inviteMode
                  ? "Accept invitation"
                  : "Sign in"}
          </button>
        </form>
        <ErrorBox error={error} />
        {!setup && (
          <button
            className="text-button account-switch"
            onClick={() => {
              setInviteMode(!inviteMode);
              setError("");
              setPassword("");
              setSecret("");
            }}
          >
            {inviteMode ? "Back to sign in" : "Have an invitation?"}
          </button>
        )}
      </div>
    </div>
  );
}
