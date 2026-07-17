export type SSEHandlers = {
  onEvent: (event: string, data: string) => void;
  onOpen?: () => void;
};

const INITIAL_RETRY_MS = 3_000;
const MAX_RETRY_MS = 30_000;

function parseSSEChunk(buffer: string, onEvent: SSEHandlers["onEvent"]): string {
  const parts = buffer.split("\n\n");
  const rest = parts.pop() ?? "";
  for (const part of parts) {
    if (!part.trim() || part.startsWith(":")) continue;
    let event = "message";
    const dataLines: string[] = [];
    for (const line of part.split("\n")) {
      if (line.startsWith("event:")) event = line.slice(6).trim();
      else if (line.startsWith("data:")) dataLines.push(line.slice(5).trimStart());
    }
    if (dataLines.length > 0) onEvent(event, dataLines.join("\n"));
  }
  return rest;
}

/** Fetch-based SSE with backoff — avoids EventSource reconnect storms on ERR_NETWORK_CHANGED. */
export function connectEventStream(url: string, handlers: SSEHandlers): () => void {
  let cancelled = false;
  let retryMs = INITIAL_RETRY_MS;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let abort: AbortController | undefined;

  const clearRetry = () => {
    if (retryTimer !== undefined) {
      clearTimeout(retryTimer);
      retryTimer = undefined;
    }
  };

  const scheduleReconnect = () => {
    if (cancelled) return;
    clearRetry();
    if (typeof navigator !== "undefined" && !navigator.onLine) return;
    retryTimer = setTimeout(() => {
      void connect();
    }, retryMs);
    retryMs = Math.min(retryMs * 2, MAX_RETRY_MS);
  };

  const resetBackoff = () => {
    retryMs = INITIAL_RETRY_MS;
  };

  const connect = async () => {
    if (cancelled) return;
    clearRetry();
    abort?.abort();
    abort = new AbortController();

    try {
      const res = await fetch(url, {
        signal: abort.signal,
        headers: { Accept: "text/event-stream" },
        cache: "no-store",
      });
      if (!res.ok || !res.body) {
        scheduleReconnect();
        return;
      }

      handlers.onOpen?.();
      resetBackoff();

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

      while (!cancelled) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        buffer = parseSSEChunk(buffer, handlers.onEvent);
      }

      if (!cancelled) scheduleReconnect();
    } catch (err) {
      if (cancelled) return;
      if (err instanceof DOMException && err.name === "AbortError") return;
      scheduleReconnect();
    }
  };

  const onOnline = () => {
    resetBackoff();
    if (!cancelled) void connect();
  };

  window.addEventListener("online", onOnline);
  void connect();

  return () => {
    cancelled = true;
    clearRetry();
    abort?.abort();
    window.removeEventListener("online", onOnline);
  };
}
