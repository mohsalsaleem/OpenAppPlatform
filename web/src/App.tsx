import { useState, useEffect, type FormEvent } from "react";
import { NavLink, Link, Routes, Route, useLocation } from "react-router-dom";
import { useQueryClient, useQuery } from "@tanstack/react-query";
import {
  LogOut,
  Settings,
  ArrowUpRight,
  Box,
  CircleHelp,
  Layers3,
  Server,
} from "lucide-react";
import { OperationAccess, OwnerAccess } from "./access";
import { AccountGate } from "./components/AccountGate";
import { Access } from "./features/access/Access";
import { api, token } from "./api";
import { ErrorBox } from "./components/Feedback";
import { Applications } from "./features/applications/Applications";
import { NewApplication } from "./features/applications/NewApplication";
import { ApplicationDetail } from "./features/applications/ApplicationDetail";
import { AssembleApplication } from "./features/applications/AssembleApplication";
import { Targets } from "./features/targets/Targets";

export function App() {
  const [connected, setConnected] = useState(false);
  const [accessToken, setAccessToken] = useState("");
  const [error, setError] = useState("");
  const client = useQueryClient();
  const location = useLocation();
  const authStatus = useQuery({
    queryKey: ["auth-status"],
    queryFn: async () => {
      const r = await fetch("/api/v1/auth/status");
      if (!r.ok) throw new Error("Cannot connect to the platform");
      return r.json() as Promise<{ mode: string; setupRequired: boolean }>;
    },
  });
  const identity = useQuery({
    queryKey: ["identity"],
    queryFn: () =>
      api<{ name: string; role: string; kind: string }>("/auth/me"),
    enabled: authStatus.data?.mode === "owner",
    retry: false,
  });
  useEffect(() => {
    if (authStatus.data?.mode === "preview" && token())
      api("/meta")
        .then(() => setConnected(true))
        .catch(() => sessionStorage.removeItem("oap-token"));
  }, [authStatus.data?.mode]);

  useEffect(() => {
    const expired = () => {
      sessionStorage.removeItem("oap-token");
      setConnected(false);
      client.setQueryData(["identity"], null);
    };
    window.addEventListener("oap-session-expired", expired);
    return () => window.removeEventListener("oap-session-expired", expired);
  }, [client]);
  const pageName =
    location.pathname === "/targets"
      ? "Deployment Targets"
      : location.pathname.startsWith("/targets/")
        ? "Group Existing Services"
        : location.pathname === "/access"
          ? "Workspace Settings / Workspace Access"
          : location.pathname === "/applications/new"
            ? "New Application"
            : location.pathname.startsWith("/applications/")
              ? "Application"
              : "Applications";
  async function connect(e: FormEvent) {
    e.preventDefault();
    sessionStorage.setItem("oap-token", accessToken);
    try {
      await api("/meta");
      setConnected(true);
      setError("");
    } catch (e) {
      sessionStorage.removeItem("oap-token");
      setError((e as Error).message);
    }
  }
  if (authStatus.isPending) return <p className="content">Connecting…</p>;
  if (authStatus.error)
    return (
      <div className="content">
        <ErrorBox error={authStatus.error} />
      </div>
    );
  if (!connected && !identity.data && authStatus.data?.mode === "owner")
    return (
      <AccountGate
        setup={authStatus.data.setupRequired}
        onConnected={() => setConnected(true)}
      />
    );
  if (!connected && !identity.data)
    return (
      <div className="connect-page">
        <div className="connect-card">
          <div className="brand-icon">
            <Layers3 size={28} />
          </div>
          <p className="eyebrow">YOUR APPLICATION WORKSPACE</p>
          <h1>Open App Platform</h1>
          <p className="muted">
            One place for your applications.
            <br />
            Your infrastructure, your choice.
          </p>
          <form onSubmit={connect}>
            <label htmlFor="access-token">Platform Access Token</label>
            <input
              id="access-token"
              type="password"
              value={accessToken}
              onChange={(e) => setAccessToken(e.target.value)}
              required
              autoComplete="off"
              placeholder="Paste your local access token"
            />
            <button className="primary" type="submit">
              Connect to Workspace <ArrowUpRight size={16} />
            </button>
          </form>
          <ErrorBox error={error} />
          <p className="small muted">
            The token stays in this browser tab. Operator credentials stay on
            the server.
          </p>
        </div>
      </div>
    );
  return (
    <OperationAccess.Provider value={identity.data?.role !== "viewer"}>
      <OwnerAccess.Provider
        value={
          (identity.data?.role === "owner" &&
            identity.data?.kind === "session") ||
          authStatus.data?.mode === "preview"
        }
      >
        <div className="shell">
          <aside className="sidebar">
            <Link to="/" className="brand">
              <span className="brand-icon">
                <Layers3 size={21} />
              </span>
              <span>
                Open App<span className="brand-sub">Platform</span>
              </span>
            </Link>
            <div className="workspace">
              <span className="workspace-avatar">W</span>
              <div>
                My Workspace
                <small>
                  {identity.data
                    ? `${identity.data.name} · ${identity.data.role}`
                    : "Self-hosted"}
                </small>
              </div>
              {identity.data?.role === "owner" && (
                <NavLink
                  to="/access"
                  className="workspace-settings"
                  title="Workspace Settings"
                  aria-label="Workspace Settings"
                >
                  <Settings size={18} />
                </NavLink>
              )}
            </div>
            <p className="nav-label">WORKSPACE</p>
            <nav>
              <NavLink to="/" end>
                <Box size={18} /> Applications
              </NavLink>
            </nav>
            <p className="nav-label">INFRASTRUCTURE</p>
            <nav aria-label="Infrastructure">
              <NavLink to="/targets">
                <Server size={18} /> Deployment Targets
              </NavLink>
            </nav>
            <div className="sidebar-bottom">
              <span className="small">
                <span className="dot" /> Core preview · v0.2
              </span>
              <button
                className="text-button"
                title={
                  authStatus.data?.mode === "owner" ? "Sign Out" : "Disconnect"
                }
                onClick={async () => {
                  if (authStatus.data?.mode === "owner") {
                    try {
                      await api("/auth/logout", { method: "POST" });
                    } catch {
                      return;
                    }
                    client.setQueryData(["identity"], null);
                  }
                  sessionStorage.removeItem("oap-token");
                  client.clear();
                  setConnected(false);
                }}
              >
                <LogOut size={15} />{" "}
                {authStatus.data?.mode === "owner" ? "Sign Out" : "Disconnect"}
              </button>
            </div>
          </aside>
          <main className="main">
            <header className="topbar">
              <span>
                Workspace <span className="slash">/</span> {pageName}
              </span>
              <a
                href="https://github.com/mohsalsaleem/OpenAppPlatform"
                target="_blank"
                rel="noreferrer"
              >
                <CircleHelp size={16} /> Project Docs
              </a>
            </header>
            <div className="content">
              <Routes>
                <Route path="/" element={<Applications />} />
                <Route
                  path="/access"
                  element={
                    identity.data?.role === "owner" ? (
                      <Access />
                    ) : (
                      <p>Owner access is required.</p>
                    )
                  }
                />
                <Route path="/applications/new" element={<NewApplication />} />
                <Route
                  path="/applications/:id"
                  element={<ApplicationDetail key={location.pathname} />}
                />
                <Route
                  path="/targets/:id/assemble"
                  element={<AssembleApplication />}
                />
                <Route path="/targets" element={<Targets />} />
                <Route
                  path="*"
                  element={
                    <section className="empty">
                      <CircleHelp size={32} />
                      <h1>Page Not Found</h1>
                      <p>
                        This page is unavailable. Return to your applications.
                      </p>
                      <Link className="button primary" to="/">
                        Go to Applications
                      </Link>
                    </section>
                  }
                />
              </Routes>
            </div>
          </main>
        </div>
      </OwnerAccess.Provider>
    </OperationAccess.Provider>
  );
}
