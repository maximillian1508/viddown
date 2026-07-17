import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SettingsDrawer } from "./components/SettingsDrawer";
import "./App.css";
import { applyUrlRules, type URLRulesConfig } from "./urlRules";
import { connectEventStream } from "./sse";

type Quality = {
  id: string;
  label: string;
  name?: string;
  resolution?: string;
  bandwidth?: number;
  duration?: string;
  estimatedBytes?: number;
  onDisk?: boolean;
  onDiskFile?: string;
  url: string;
};

function formatBytes(n?: number): string {
  if (!n || n <= 0) return "";
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)} GB`;
  if (n >= 1_000_000) return `${Math.round(n / 1_000_000)} MB`;
  if (n >= 1_000) return `${Math.round(n / 1_000)} KB`;
  return `${n} B`;
}

type Video = {
  id: string;
  label: string;
  duration?: string;
  likelyAd?: boolean;
  masterUrl?: string;
  qualities: Quality[];
};

type ProbeJob = {
  id: string;
  status: string;
  message?: string;
  pageUrl?: string;
  pageTitle?: string;
  nameSlug?: string;
  videos?: Video[];
};

type DownloadJob = {
  id: string;
  status: string;
  message?: string;
  label?: string;
  progress: number;
  filePath?: string;
  fileName?: string;
  openUrl?: string;
  probeId?: string;
  videoId?: string;
  qualityId?: string;
  createdAt?: string;
};

function formatJobTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** videoId → qualityId (kept for all videos, including unchecked) */
type QualityMap = Record<string, string>;

const HISTORY_PAGE_SIZE = 15;

type DownloadsResponse = {
  active?: DownloadJob[];
  history?: DownloadJob[];
  total?: number;
  limit?: number;
  offset?: number;
  downloads?: DownloadJob[];
};

function isActiveDownload(d: DownloadJob): boolean {
  return d.status === "queued" || d.status === "running";
}

function statusLabel(status: string): string {
  switch (status) {
    case "error":
      return "Failed";
    case "cancelled":
      return "Cancelled";
    case "done":
      return "Done";
    default:
      return status;
  }
}

async function readError(res: Response): Promise<string> {
  try {
    const data = await res.json();
    return data.error || res.statusText;
  } catch {
    return res.statusText;
  }
}

function suggestedFileName(
  slug: string | undefined,
  q: Quality | undefined,
  videoId?: string,
): string {
  const base = slug || "video";
  const res = q?.resolution
    ? q.resolution.includes("x")
      ? (() => {
          const h = Number(q.resolution!.split("x")[1]);
          if (h >= 2160) return "2160p";
          if (h >= 1440) return "1440p";
          if (h >= 1080) return "1080p";
          if (h >= 720) return "720p";
          if (h >= 480) return "480p";
          if (h >= 360) return "360p";
          return q.resolution!;
        })()
      : q.resolution
    : "";
  return [base, res, videoId].filter(Boolean).join("_") + ".mp4";
}

function defaultQualityId(v: Video): string {
  return v.qualities[0]?.id ?? "";
}

function cleanClipboardText(raw: string): string {
  return raw.trim().split(/\r?\n/)[0]?.trim() ?? "";
}

export default function App() {
  const [url, setUrl] = useState("");
  const [probeId, setProbeId] = useState<string | null>(null);
  const [probe, setProbe] = useState<ProbeJob | null>(null);
  const [qualityByVideo, setQualityByVideo] = useState<QualityMap>({});
  const [checkedIds, setCheckedIds] = useState<Record<string, boolean>>({});
  const [focusVideoId, setFocusVideoId] = useState("");
  const [downloadId, setDownloadId] = useState<string | null>(null);
  const [downloads, setDownloads] = useState<DownloadJob[]>([]);
  const [activeDownloads, setActiveDownloads] = useState<DownloadJob[]>([]);
  const [historyDownloads, setHistoryDownloads] = useState<DownloadJob[]>([]);
  const [historyTotal, setHistoryTotal] = useState(0);
  const [historyOffset, setHistoryOffset] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [startingDownloads, setStartingDownloads] = useState(false);
  const [cancellingId, setCancellingId] = useState<string | null>(null);
  const [retryingId, setRetryingId] = useState<string | null>(null);
  const [previewURL, setPreviewURL] = useState<string | null>(null);
  const [previewKind, setPreviewKind] = useState<"video" | "image" | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [clipboardError, setClipboardError] = useState<string | null>(null);
  const [clipboardPasting, setClipboardPasting] = useState(false);
  const [urlRules, setUrlRules] = useState<URLRulesConfig>({ rules: [] });
  const [settingsOpen, setSettingsOpen] = useState(false);
  const historyPanelRef = useRef<HTMLElement>(null);
  const skipHistoryScrollRef = useRef(true);
  const probeIdRef = useRef<string | null>(null);
  const downloadIdRef = useRef<string | null>(null);
  const historyOffsetRef = useRef(0);
  const refetchProbeAnnotatedRef = useRef<() => Promise<void>>(async () => {});
  const applyTerminalDownloadRef = useRef<(job: DownloadJob) => void>(() => {});
  probeIdRef.current = probeId;
  downloadIdRef.current = downloadId;
  historyOffsetRef.current = historyOffset;

  const fetchDownloads = useCallback(async () => {
    const qs = new URLSearchParams({
      limit: String(HISTORY_PAGE_SIZE),
      offset: String(historyOffsetRef.current),
    });
    try {
      const res = await fetch(`/api/downloads?${qs}`);
      if (!res.ok) return;
      const data: DownloadsResponse = await res.json();
      const active = data.active ?? [];
      const history =
        data.history ?? data.downloads?.filter((d) => !isActiveDownload(d)) ?? [];
      setActiveDownloads(active);
      setHistoryDownloads(history);
      setHistoryTotal(data.total ?? history.length);
      setDownloads([...active, ...history]);
      const tracked = downloadIdRef.current;
      if (tracked) {
        const mine = [...active, ...history].find((d) => d.id === tracked);
        if (mine && !isActiveDownload(mine) && mine.status === "error") {
          setError(mine.message || "Download failed");
        }
      }
    } catch {
      /* optional */
    }
  }, []);

  const refetchProbeAnnotated = useCallback(async () => {
    const id = probeIdRef.current;
    if (!id) return;
    try {
      const res = await fetch(`/api/probe/${id}`);
      if (res.ok) setProbe(await res.json());
    } catch {
      /* optional */
    }
  }, []);

  const applyTerminalDownload = useCallback((job: DownloadJob) => {
    setActiveDownloads((prev) => prev.filter((d) => d.id !== job.id));
    if (downloadIdRef.current === job.id && job.status === "error") {
      setError(job.message || "Download failed");
    }
    setHistoryDownloads((prev) => {
      const existed = prev.some((d) => d.id === job.id);
      if (!existed) {
        setHistoryTotal((t) => t + 1);
      }
      if (historyOffsetRef.current !== 0) {
        return prev;
      }
      const without = prev.filter((d) => d.id !== job.id);
      return [job, ...without].slice(0, HISTORY_PAGE_SIZE);
    });
    setDownloads((prev) => {
      const rest = prev.filter((d) => d.id !== job.id);
      return [job, ...rest];
    });
    void refetchProbeAnnotated();
  }, [refetchProbeAnnotated]);

  refetchProbeAnnotatedRef.current = refetchProbeAnnotated;
  applyTerminalDownloadRef.current = applyTerminalDownload;

  useEffect(() => {
    void fetchDownloads();
  }, [fetchDownloads, historyOffset]);

  useEffect(() => {
    return connectEventStream("/api/events", {
      onEvent: (event, data) => {
        if (event === "connected" || event === "history") return;

        if (event === "probe") {
          let probeData: ProbeJob;
          try {
            probeData = JSON.parse(data) as ProbeJob;
          } catch {
            return;
          }
          if (probeData.id !== probeIdRef.current) return;

          if (probeData.status === "ready") {
            void refetchProbeAnnotatedRef.current().then(() => setBusy(false));
            return;
          }

          setProbe(probeData);
          if (probeData.status === "error") {
            setBusy(false);
            setError(probeData.message || "Probe failed");
          }
          return;
        }

        if (event === "download") {
          let job: DownloadJob;
          try {
            job = JSON.parse(data) as DownloadJob;
          } catch {
            return;
          }

          if (isActiveDownload(job)) {
            setActiveDownloads((prev) => {
              const idx = prev.findIndex((d) => d.id === job.id);
              if (idx >= 0) {
                const next = [...prev];
                next[idx] = job;
                return next;
              }
              return [job, ...prev];
            });
            setDownloads((prev) => {
              const rest = prev.filter((d) => d.id !== job.id);
              return [job, ...rest];
            });
            return;
          }

          applyTerminalDownloadRef.current(job);
        }
      },
    });
  }, []);

  useEffect(() => {
    if (skipHistoryScrollRef.current) {
      skipHistoryScrollRef.current = false;
      return;
    }
    historyPanelRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [historyOffset]);

  const videos = probe?.videos ?? [];
  const urlRewrite = useMemo(
    () => applyUrlRules(url, urlRules),
    [url, urlRules],
  );
  const selectedIds = useMemo(
    () => videos.map((v) => v.id).filter((id) => checkedIds[id]),
    [videos, checkedIds],
  );
  const selectedCount = selectedIds.length;

  const focusVideo = useMemo(() => {
    if (!focusVideoId) return videos[0] ?? null;
    return videos.find((v) => v.id === focusVideoId) ?? videos[0] ?? null;
  }, [videos, focusVideoId]);

  const focusQuality = useMemo(() => {
    if (!focusVideo) return undefined;
    const qid = qualityByVideo[focusVideo.id] ?? defaultQualityId(focusVideo);
    return focusVideo.qualities.find((q) => q.id === qid);
  }, [focusVideo, qualityByVideo]);

  // When probe results arrive, select all videos (best quality = first).
  useEffect(() => {
    if (probe?.status !== "ready" || videos.length === 0) return;
    const nextQ: QualityMap = {};
    const nextChecked: Record<string, boolean> = {};
    for (const v of videos) {
      const qid = defaultQualityId(v);
      if (qid) nextQ[v.id] = qid;
      nextChecked[v.id] = true;
    }
    setQualityByVideo(nextQ);
    setCheckedIds(nextChecked);
    setFocusVideoId(videos[0]?.id ?? "");
  }, [probe?.status, probe?.id]); // eslint-disable-line react-hooks/exhaustive-deps -- init once per probe

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await fetch("/api/url-rules");
        if (!res.ok || cancelled) return;
        setUrlRules(await res.json());
      } catch {
        /* optional */
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function pasteFromClipboard() {
    setClipboardError(null);
    setClipboardPasting(true);
    try {
      if (!navigator.clipboard?.readText) {
        setClipboardError("Clipboard paste is not supported in this browser.");
        return;
      }
      const text = cleanClipboardText(await navigator.clipboard.readText());
      if (!text) {
        setClipboardError("Clipboard is empty.");
        return;
      }
      setUrl(applyUrlRules(text, urlRules).output);
      setError(null);
    } catch {
      setClipboardError("Could not read clipboard — allow access when prompted.");
    } finally {
      setClipboardPasting(false);
    }
  }

  const probeReady = probe?.status === "ready";
  const previewVideoId = focusVideo?.id ?? "";
  const previewQualityId = focusVideo
    ? qualityByVideo[focusVideo.id] ?? defaultQualityId(focusVideo)
    : "";

  useEffect(() => {
    if (!probeId || !previewVideoId || !previewQualityId || !probeReady) {
      setPreviewURL(null);
      setPreviewKind(null);
      setPreviewError(null);
      return;
    }
    let cancelled = false;
    let objectURL: string | null = null;
    setPreviewLoading(true);
    setPreviewError(null);
    setPreviewURL(null);
    setPreviewKind(null);

    const qs = new URLSearchParams({
      probeId,
      videoId: previewVideoId,
      qualityId: previewQualityId,
    });

    fetch(`/api/preview?${qs}`)
      .then(async (res) => {
        if (!res.ok) throw new Error(await readError(res));
        return res.blob();
      })
      .then((blob) => {
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setPreviewURL(objectURL);
        setPreviewKind("image");
        setPreviewLoading(false);
      })
      .catch((err) => {
        if (cancelled) return;
        setPreviewLoading(false);
        setPreviewError(err instanceof Error ? err.message : "Preview failed");
      });

    return () => {
      cancelled = true;
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [probeId, previewVideoId, previewQualityId, probeReady]);

  function toggleVideo(v: Video, checked: boolean) {
    setCheckedIds((prev) => ({ ...prev, [v.id]: checked }));
    if (checked) setFocusVideoId(v.id);
  }

  function setVideoQuality(videoId: string, qualityId: string) {
    setQualityByVideo((prev) => ({ ...prev, [videoId]: qualityId }));
    setFocusVideoId(videoId);
  }

  function selectAll() {
    const next: Record<string, boolean> = {};
    for (const v of videos) next[v.id] = true;
    setCheckedIds(next);
    if (videos[0]) setFocusVideoId(videos[0].id);
  }

  function selectNone() {
    setCheckedIds({});
  }

  function promoteActiveDownload(job: DownloadJob) {
    setActiveDownloads((prev) => {
      const idx = prev.findIndex((d) => d.id === job.id);
      if (idx >= 0) {
        const next = [...prev];
        next[idx] = job;
        return next;
      }
      return [job, ...prev];
    });
  }

  function removeFromHistory(id: string) {
    setHistoryDownloads((prev) => {
      if (!prev.some((d) => d.id === id)) return prev;
      setHistoryTotal((t) => Math.max(0, t - 1));
      return prev.filter((d) => d.id !== id);
    });
  }

  async function onProbe(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setDownloadId(null);
    setProbe(null);
    setQualityByVideo({});
    setCheckedIds({});
    setFocusVideoId("");
    setPreviewURL(null);
    setBusy(true);
    const probeUrl = applyUrlRules(url, urlRules).output;
    if (probeUrl !== url.trim()) setUrl(probeUrl);
    try {
      const res = await fetch("/api/probe", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: probeUrl }),
      });
      if (!res.ok) throw new Error(await readError(res));
      const data = await res.json();
      setProbeId(data.id);
    } catch (err) {
      setBusy(false);
      setError(err instanceof Error ? err.message : "Probe failed");
    }
  }

  async function onDownloadSelected() {
    if (!probeId || selectedCount === 0) return;
    setError(null);
    setStartingDownloads(true);
    const entries = selectedIds.map((videoId) => {
      const v = videos.find((x) => x.id === videoId);
      return {
        videoId,
        qualityId: qualityByVideo[videoId] || (v ? defaultQualityId(v) : ""),
      };
    });
    const errors: string[] = [];
    let lastId: string | null = null;
    try {
      for (const { videoId, qualityId } of entries) {
        if (!qualityId) {
          errors.push(`${videoId}: no quality`);
          continue;
        }
        const res = await fetch("/api/download", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ probeId, videoId, qualityId }),
        });
        if (!res.ok) {
          errors.push(`${videoId}: ${await readError(res)}`);
          continue;
        }
        const data: DownloadJob = await res.json();
        lastId = data.id;
        promoteActiveDownload(data);
      }
      if (lastId) setDownloadId(lastId);
      if (errors.length) {
        setError(
          errors.length === entries.length
            ? errors.join("; ")
            : `Started ${entries.length - errors.length}; failed: ${errors.join("; ")}`,
        );
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Download failed");
    } finally {
      setStartingDownloads(false);
    }
  }

  async function onCancel(id: string) {
    setCancellingId(id);
    setError(null);
    try {
      const res = await fetch(`/api/download/${id}/cancel`, { method: "POST" });
      if (!res.ok) throw new Error(await readError(res));
      const data: DownloadJob = await res.json();
      applyTerminalDownload(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Cancel failed");
    } finally {
      setCancellingId(null);
    }
  }

  async function onRetry(id: string) {
    setRetryingId(id);
    setError(null);
    try {
      const res = await fetch(`/api/download/${id}/retry`, { method: "POST" });
      if (!res.ok) throw new Error(await readError(res));
      const job: DownloadJob = await res.json();
      removeFromHistory(id);
      setDownloadId(job.id);
      promoteActiveDownload(job);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Retry failed");
    } finally {
      setRetryingId(null);
    }
  }

  const canRetry = (d: DownloadJob) =>
    !d.id.startsWith("saved:") &&
    (d.status === "error" || d.status === "cancelled") &&
    !!(d.probeId && d.videoId && d.qualityId);

  const probing = probe?.status === "running";
  const ready = probe?.status === "ready" && videos.length > 0;
  const historyPage = Math.floor(historyOffset / HISTORY_PAGE_SIZE) + 1;
  const historyPageCount = Math.max(1, Math.ceil(historyTotal / HISTORY_PAGE_SIZE));
  const historyRangeStart = historyTotal === 0 ? 0 : historyOffset + 1;
  const historyRangeEnd = Math.min(historyOffset + HISTORY_PAGE_SIZE, historyTotal);

  const historySection = (
    <section ref={historyPanelRef} className="panel history-panel">
      <div className="jobs">
        {activeDownloads.length > 0 && (
          <>
            <h2 className="jobs-title">
              Running now ({activeDownloads.length})
            </h2>
            <p className="status">
              Up to 10 downloads at once. They keep going if you close this tab
              — use Cancel to stop them.
            </p>
            {activeDownloads.map((d) => (
              <div key={d.id} className="progress-block">
                <div className="progress-meta">
                  <span className="job-label">
                    {d.label || d.fileName || d.id.slice(0, 8)}
                  </span>
                  <span className="mono">{Math.round(d.progress)}%</span>
                </div>
                <p className="status">{d.message || d.status}</p>
                <div className="bar">
                  <div style={{ width: `${Math.min(100, d.progress)}%` }} />
                </div>
                <div className="job-actions">
                  <button
                    type="button"
                    className="ghost"
                    onClick={() => onCancel(d.id)}
                    disabled={cancellingId === d.id}
                  >
                    {cancellingId === d.id ? "Cancelling…" : "Cancel"}
                  </button>
                </div>
              </div>
            ))}
          </>
        )}

        <h2 className="jobs-title">Download history</h2>
        <p className="status history-lede">
          Saved videos, failed attempts, and cancelled downloads. Probe sessions
          are not listed — only download activity.
        </p>
        {historyDownloads.length === 0 ? (
          <p className="status history-empty">
            No downloads yet. Probe a URL and save a stream — it will show up
            here.
          </p>
        ) : (
          historyDownloads.map((d) => (
            <div key={d.id} className="progress-block recent">
              <div className="progress-meta">
                <span className="job-label">
                  {d.label || d.fileName || d.id.slice(0, 8)}
                </span>
                <span className={`mono job-status status-${d.status}`}>
                  {statusLabel(d.status)}
                </span>
              </div>
              {formatJobTime(d.createdAt) && (
                <p className="status job-time">{formatJobTime(d.createdAt)}</p>
              )}
              {d.status === "done" && (d.fileName || d.filePath) && (
                <p className="ok">
                  Saved → <code>{d.fileName || d.filePath}</code>
                </p>
              )}
              {d.status === "done" && d.openUrl && (
                <div className="job-actions">
                  <a
                    className="ghost link-btn"
                    href={d.openUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    Open in Filebrowser
                  </a>
                </div>
              )}
              {(d.status === "error" || d.status === "cancelled") && (
                <p className="status">{d.message || d.status}</p>
              )}
              {canRetry(d) && (
                <div className="job-actions">
                  <button
                    type="button"
                    className="ghost"
                    onClick={() => onRetry(d.id)}
                    disabled={retryingId === d.id}
                  >
                    {retryingId === d.id ? "Retrying…" : "Retry"}
                  </button>
                </div>
              )}
            </div>
          ))
        )}
        {historyTotal > HISTORY_PAGE_SIZE && (
          <div className="history-pagination">
            <button
              type="button"
              className="ghost"
              disabled={historyOffset <= 0}
              onClick={() =>
                setHistoryOffset((o) => Math.max(0, o - HISTORY_PAGE_SIZE))
              }
            >
              Previous
            </button>
            <span className="status history-page-meta">
              {historyRangeStart}–{historyRangeEnd} of {historyTotal} · page{" "}
              {historyPage} / {historyPageCount}
            </span>
            <button
              type="button"
              className="ghost"
              disabled={historyOffset + HISTORY_PAGE_SIZE >= historyTotal}
              onClick={() => setHistoryOffset((o) => o + HISTORY_PAGE_SIZE)}
            >
              Next
            </button>
          </div>
        )}
      </div>
    </section>
  );

  return (
    <div className={`page${ready ? " has-download-bar" : ""}`}>
      <button
        type="button"
        className="icon-btn page-settings-btn"
        onClick={() => setSettingsOpen(true)}
        aria-label="Open settings"
      >
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
          <circle cx="12" cy="12" r="3" />
        </svg>
      </button>

      <SettingsDrawer
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        urlRules={urlRules}
        onUrlRulesChange={setUrlRules}
      />

      <header className="hero">
        <p className="brand">Viddown</p>
        <h1>Pull a stream into Filebrowser</h1>
        <p className="lede">
          Paste a page or m3u8 URL. Select one or more videos, pick quality, then
          download.
        </p>
      </header>

      <main className="panel">
        <form className="probe-row" onSubmit={onProbe}>
          <div className="probe-url-block">
            <span className="probe-url-label">URL</span>
            <div className="probe-url-main">
              <div className="url-input-row">
                <div className="url-input-wrap">
                  <input
                    value={url}
                    onChange={(e) => {
                      setUrl(e.target.value);
                      setClipboardError(null);
                    }}
                    placeholder="https://…"
                    required
                    disabled={busy && probing}
                    inputMode="url"
                    autoCapitalize="off"
                    autoCorrect="off"
                    spellCheck={false}
                    className={url ? "has-clear" : undefined}
                  />
                  {url && !(busy && probing) && (
                    <button
                      type="button"
                      className="url-clear-btn"
                      aria-label="Clear URL"
                      onClick={() => {
                        setUrl("");
                        setClipboardError(null);
                      }}
                    >
                      ×
                    </button>
                  )}
                </div>
                <button
                  type="button"
                  className="ghost paste-btn"
                  disabled={(busy && probing) || clipboardPasting}
                  onClick={() => void pasteFromClipboard()}
                >
                  {clipboardPasting ? "…" : "Paste"}
                </button>
              </div>
              <button type="submit" disabled={busy || !url.trim()}>
                {probing ? "Probing…" : "Probe"}
              </button>
            </div>
            {clipboardError && (
              <p className="clipboard-error">{clipboardError}</p>
            )}
            {urlRewrite.changed && (
              <p className="url-rewrite-hint">
                Using{" "}
                <code>{urlRewrite.output}</code>
                {urlRewrite.ruleName ? ` · ${urlRewrite.ruleName}` : ""}
              </p>
            )}
          </div>
        </form>

        {(probe?.message || probing) && (
          <p className="status">{probe?.message || "Working…"}</p>
        )}
        {probe?.pageTitle && probe.status === "ready" && (
          <p className="status">Page: {probe.pageTitle}</p>
        )}

        {ready && (
          <>
            <div className="video-list-header">
              <span className="field-label">Videos</span>
              <div className="video-list-actions">
                <button type="button" className="ghost" onClick={selectAll} disabled={probing}>
                  Select all
                </button>
                <button type="button" className="ghost" onClick={selectNone} disabled={probing}>
                  None
                </button>
              </div>
            </div>

            <ul className="video-list">
              {videos.map((v) => {
                const checked = !!checkedIds[v.id];
                const qid = qualityByVideo[v.id] ?? defaultQualityId(v);
                const selectedQuality = v.qualities.find((q) => q.id === qid);
                const focused = focusVideo?.id === v.id;
                return (
                  <li
                    key={v.id}
                    className={`video-row${checked ? " is-checked" : ""}${focused ? " is-focus" : ""}`}
                  >
                    <label className="video-check">
                      <input
                        type="checkbox"
                        checked={checked}
                        disabled={probing}
                        onChange={(e) => toggleVideo(v, e.target.checked)}
                      />
                      <span className="video-check-label">
                        <span className="video-check-title">
                          {v.label}
                          {v.duration && (
                            <span className="video-duration"> · {v.duration}</span>
                          )}
                        </span>
                        <span className="video-badges">
                          {v.likelyAd && (
                            <span className="video-badge video-badge-ad">Likely ad</span>
                          )}
                          {selectedQuality?.onDisk && (
                            <span className="video-badge video-badge-saved">Already saved</span>
                          )}
                        </span>
                      </span>
                    </label>
                    <div className="video-row-actions">
                      <select
                        value={qid}
                        disabled={probing}
                        onChange={(e) => setVideoQuality(v.id, e.target.value)}
                        onClick={() => setFocusVideoId(v.id)}
                        aria-label={`Quality for ${v.label}`}
                      >
                        {v.qualities.map((q) => (
                          <option key={q.id} value={q.id}>
                            {[q.label || "Stream", q.onDisk ? "· saved" : ""]
                              .filter(Boolean)
                              .join(" ")}
                          </option>
                        ))}
                      </select>
                      <button
                        type="button"
                        className="ghost preview-pick"
                        disabled={probing}
                        onClick={() => setFocusVideoId(v.id)}
                      >
                        Preview
                      </button>
                    </div>

                    {focused && (
                      <div className="preview-row preview-row-inline">
                        <div className="preview-frame">
                          {previewLoading && <p className="status">Loading preview…</p>}
                          {!previewLoading && previewURL && previewKind === "video" && (
                            <video
                              src={previewURL}
                              muted
                              autoPlay
                              loop
                              playsInline
                              controls={false}
                            />
                          )}
                          {!previewLoading && previewURL && previewKind === "image" && (
                            <img src={previewURL} alt="Stream preview" />
                          )}
                          {!previewLoading && !previewURL && (
                            <p className="status">
                              {previewError ? "Preview unavailable" : "No preview"}
                            </p>
                          )}
                        </div>
                        <div className="preview-meta">
                          <p className="meta-label">Preview · {v.label}</p>
                          {focusQuality && (
                            <code className="filename">
                              {suggestedFileName(probe?.nameSlug, focusQuality, v.id)}
                            </code>
                          )}
                          {(focusQuality?.duration || focusQuality?.estimatedBytes) && (
                            <p className="status">
                              {[
                                focusQuality?.duration && `Duration ~ ${focusQuality.duration}`,
                                focusQuality?.estimatedBytes &&
                                  `Size ~ ${formatBytes(focusQuality.estimatedBytes)}`,
                              ]
                                .filter(Boolean)
                                .join(" · ")}
                            </p>
                          )}
                          {focusQuality?.onDiskFile && (
                            <p className="status saved-hint">
                              Already on disk: {focusQuality.onDiskFile}
                            </p>
                          )}
                        </div>
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>

            <div className="download-bar">
              <p className="status">
                {selectedCount === 0
                  ? "Select at least one video"
                  : `${selectedCount} selected`}
              </p>
              <button
                type="button"
                className="primary"
                onClick={onDownloadSelected}
                disabled={probing || startingDownloads || selectedCount === 0}
              >
                {startingDownloads
                  ? "Starting…"
                  : selectedCount <= 1
                    ? "Download"
                    : `Download ${selectedCount}`}
              </button>
            </div>

          </>
        )}

        {error && <p className="error">{error}</p>}
      </main>

      {historySection}
    </div>
  );
}
