import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { Application, Deployment } from "../api";
import { consumeSnapshots } from "./streams";
export function useApplicationEvents(id: string | undefined) {
  const client = useQueryClient(),
    [connected, setConnected] = useState(false);
  useEffect(() => {
    setConnected(false);
    if (!id) return;
    const stop = new AbortController();
    void consumeSnapshots(
      `/applications/${encodeURIComponent(id)}/events`,
      stop.signal,
      (value) => {
        const snapshot = value as {
          application: Application;
          deployments: Deployment[];
        };
        client.setQueryData(["application", id], snapshot.application);
        client.setQueryData(["deployments", id], snapshot.deployments);
      },
      setConnected,
    );
    return () => stop.abort();
  }, [id, client]);
  return connected;
}
