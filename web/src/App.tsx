import { useState, type FormEvent } from "react";
import { NavLink, Link, Routes, Route, useLocation } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowUpRight,
  Box,
  CircleHelp,
  Layers3,
  Server,
} from "lucide-react";
import { api, token } from "./api";
import { ErrorBox } from "./components/Feedback";
import { Applications } from "./features/applications/Applications";
import { NewApplication } from "./features/applications/NewApplication";
import { ApplicationDetail } from "./features/applications/ApplicationDetail";
import { AssembleApplication } from "./features/applications/AssembleApplication";
import { Targets } from "./features/targets/Targets";

export function App() {
  const [connected, setConnected] = useState(!!token());
  const [accessToken, setAccessToken] = useState("");
  const [error, setError] = useState("");
  const client = useQueryClient();
  const location = useLocation();
  const pageName =
    location.pathname === "/targets"
      ? "Deployment targets"
      : location.pathname === "/applications/new"
        ? "New application"
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
  if (!connected)
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
            <label htmlFor="access-token">Platform access token</label>
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
              Connect to workspace <ArrowUpRight size={16} />
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
            My workspace<small>Self-hosted</small>
          </div>
        </div>
        <p className="nav-label">WORKSPACE</p>
        <nav>
          <NavLink to="/" end>
            <Box size={18} /> Applications
          </NavLink>
          <NavLink to="/targets">
            <Server size={18} /> Deployment targets
          </NavLink>
        </nav>
        <div className="sidebar-bottom">
          <span className="small">
            <span className="dot" /> Core preview · v0.2
          </span>
          <button
            className="text-button"
            onClick={() => {
              sessionStorage.removeItem("oap-token");
              client.clear();
              setConnected(false);
            }}
          >
            <ArrowLeft size={15} /> Disconnect
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
            <CircleHelp size={16} /> Project docs
          </a>
        </header>
        <div className="content">
          <Routes>
            <Route path="/" element={<Applications />} />
            <Route path="/applications/new" element={<NewApplication />} />
            <Route path="/applications/:id" element={<ApplicationDetail />} />
            <Route
              path="/targets/:id/assemble"
              element={<AssembleApplication />}
            />
            <Route path="/targets" element={<Targets />} />
            <Route
              path="*"
              element={
                <p>
                  Page not found. <Link to="/">Go to applications</Link>
                </p>
              }
            />
          </Routes>
        </div>
      </main>
    </div>
  );
}
