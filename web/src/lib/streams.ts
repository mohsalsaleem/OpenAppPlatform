import { token } from "../api";

/** Bounded SSE parser shared by cookie sessions and bearer-authenticated previews. */
export async function consumeSnapshots(
  path: string,
  signal: AbortSignal,
  onSnapshot: (value: unknown) => void,
  onConnection: (connected: boolean) => void,
) {
  let delay = 1000;
  while (!signal.aborted) {
    try {
      const response = await fetch("/api/v1" + path, {
        signal,
        headers: token() ? { Authorization: `Bearer ${token()}` } : {},
        credentials: "same-origin",
      });
      if (response.status === 401 || response.status === 403) {
        window.dispatchEvent(new Event("oap-session-expired"));
        return;
      }
      if (
        !response.ok ||
        !response.headers
          .get("content-type")
          ?.startsWith("text/event-stream") ||
        !response.body
      )
        throw new Error("Streaming unavailable");
      const reader = response.body.getReader(),
        decoder = new TextDecoder();
      let buffer = "";
      try {
        while (!signal.aborted) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder
            .decode(value, { stream: true })
            .replace(/\r\n/g, "\n");
          if (buffer.length > 4 * 1024 * 1024)
            throw new Error("Stream frame too large");
          let boundary;
          while ((boundary = buffer.indexOf("\n\n")) >= 0) {
            const block = buffer.slice(0, boundary);
            buffer = buffer.slice(boundary + 2);
            const lines = block.split("\n"),
              event = lines
                .find((line) => line.startsWith("event:"))
                ?.slice(6)
                .trim();
            const data = lines
              .filter((line) => line.startsWith("data:"))
              .map((line) => line.slice(5).trimStart())
              .join("\n");
            if (event === "unauthorized") {
              window.dispatchEvent(new Event("oap-session-expired"));
              return;
            }
            if (event === "unavailable")
              throw new Error("Live updates unavailable");
            if (event === "snapshot") {
              onSnapshot(JSON.parse(data));
              onConnection(true);
              delay = 1000;
            }
          }
        }
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
    } catch (error) {
      if (signal.aborted) return;
    }
    onConnection(false);
    await new Promise<void>((resolve) => {
      const abort = () => {
        clearTimeout(timer);
        resolve();
      };
      const timer = setTimeout(() => {
        signal.removeEventListener("abort", abort);
        resolve();
      }, delay);
      signal.addEventListener("abort", abort, { once: true });
    });
    delay = Math.min(delay * 2, 10000);
  }
}
