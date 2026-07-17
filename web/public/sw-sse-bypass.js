// Do not intercept long-lived SSE — Workbox/fetch handlers can break streaming.
self.addEventListener("fetch", (event) => {
  try {
    const path = new URL(event.request.url).pathname;
    if (path === "/api/events") return;
  } catch {
    /* ignore */
  }
});
