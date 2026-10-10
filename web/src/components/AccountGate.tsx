import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Layers3 } from "lucide-react";
import { unwrap, api } from "../api";
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
      const user = await api
        .POST(
          setup
            ? "/api/v1/auth/setup"
            : inviteMode
              ? "/api/v1/auth/invitations/accept"
              : "/api/v1/auth/login",
          {
            body: {
              email,
              name,
              password,
              ...(inviteMode ? { invite: secret } : {}),
            },
            headers: setup ? { Authorization: `Setup ${secret}` } : {},
          },
        )
        .then(unwrap);
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
            ? "Set Up Your Workspace"
            : inviteMode
              ? "Join Your Workspace"
              : "Welcome Back"}
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
              <Label htmlFor="account-name">Your Name</Label>
              <Input
                id="account-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="name"
                maxLength={80}
                required
              />
            </>
          )}
          <Label htmlFor="account-email">Email</Label>
          <Input
            id="account-email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="username"
            required
          />
          <Label htmlFor="account-password">Password</Label>
          <Input
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
              <Label htmlFor="account-secret">
                {setup ? "Setup Secret" : "Invitation Code"}
              </Label>
              <Input
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
          <Button variant="default" disabled={busy}>
            {busy
              ? "Please wait…"
              : setup
                ? "Create Owner Account"
                : inviteMode
                  ? "Accept Invitation"
                  : "Sign In"}
          </Button>
        </form>
        <ErrorBox error={error} />
        {!setup && (
          <Button
            variant="outline"
            className="text-button account-switch"
            onClick={() => {
              setInviteMode(!inviteMode);
              setError("");
              setPassword("");
              setSecret("");
            }}
          >
            {inviteMode ? "Back to sign in" : "Have an invitation?"}
          </Button>
        )}
      </div>
    </div>
  );
}
