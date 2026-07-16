import { useEffect, useMemo, useState } from "react";
import "./App.css";

type Quality = {
  id: string;
  label: string;
  name?: string;
  resolution?: string;
  bandwidth?: number;
  duration?: string;
  url: string;
};

type Video = {
  id: string;
  label: string;
  masterUrl?: string;
  qualities: Quality[];
};

type ProbeJob = {
  id: string;
  status: string;
  message?: string;
  pageUrl?: string;
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
};

function isActiveDownload(d: DownloadJob): boolean {
  return d.status === "queued" || d.status === "running";
}

async function readError(res: Response): Promise<string> {
  try {
    const data = await res.json();
    return data.error || res.statusText;
  } catch {
    return res.statusText;
  }
}

function suggestedFileName(slug: string | undefined, q: Quality | undefined): string {
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
  const ts = new Date()
    .toISOString()
    .replace(/[-:TZ.]/g, "")
    .slice(0, 15);
  return [base, res, ts].filter(Boolean).join("_") + ".mp4";
}

export default function App() {
  const [url, setUrl] = useState("");
  const [probeId, setProbeId] = useState<string | null>(null);
  const [probe, setProbe] = useState<ProbeJob | null>(null);
  const [videoId, setVideoId] = useState("");
  const [qualityId, setQualityId] = useState("");
  const [downloadId, setDownloadId] = useState<string | null>(null);
  const [downloads, setDownloads] = useState<DownloadJob[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [cancellingId, setCancellingId] = useState<string | null>(null);
  const [previewURL, setPreviewURL] = useState<string | null>(null);
  const [previewKind, setPreviewKind] = useState<"video" | "image" | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);

  const videos = probe?.videos ?? [];
  const selectedVideo = useMemo(
    () => videos.find((v) => v.id === videoId) ?? videos[0],
    [videos, videoId],
  );
  const selectedQuality = useMemo(
    () => selectedVideo?.qualities.find((q) => q.id === qualityId),
    [selectedVideo, qualityId],
  );

  useEffect(() => {
    if (!selectedVideo) {
      setVideoId("");
      setQualityId("");
      return;
    }
    setVideoId(selectedVideo.id);
    const firstQ = selectedVideo.qualities[0]?.id ?? "";
    setQualityId((prev) =>
      selectedVideo.qualities.some((q) => q.id === prev) ? prev : firstQ,
    );
  }, [selectedVideo]);

  useEffect(() => {
    if (!probeId) return;
    let cancelled = false;
    let intervalId = 0;
    const stop = () => {
      if (intervalId) {
        window.clearInterval(intervalId);
        intervalId = 0;
      }
    };
    const tick = async () => {
      const res = await fetch(`/api/probe/${probeId}`);
      if (!res.ok || cancelled) return;
      const data: ProbeJob = await res.json();
      if (cancelled) return;
      setProbe(data);
      if (data.status === "ready" || data.status === "error") {
        stop();
        setBusy(false);
        if (data.status === "error") setError(data.message || "Probe failed");
      }
    };
    tick();
    intervalId = window.setInterval(tick, 1500);
    return () => {
      cancelled = true;
      stop();
    };
  }, [probeId]);

  useEffect(() => {
    let cancelled = false;
    let timeoutId = 0;

    const schedule = (ms: number) => {
      timeoutId = window.setTimeout(tick, ms);
    };

    const tick = async () => {
      try {
        const res = await fetch("/api/downloads");
        if (!res.ok || cancelled) return;
        const data = await res.json();
        if (cancelled) return;
        const list: DownloadJob[] = data.downloads ?? [];
        setDownloads(list);
        const active = list.some(isActiveDownload);
        if (downloadId) {
          const mine = list.find((d) => d.id === downloadId);
          if (mine && !isActiveDownload(mine) && mine.status === "error") {
            setError(mine.message || "Download failed");
          }
        }
        // Poll fast while something is running; otherwise check rarely.
        schedule(active || downloadId ? 1500 : 12000);
      } catch {
        if (!cancelled) schedule(5000);
      }
    };

    tick();
    return () => {
      cancelled = true;
      window.clearTimeout(timeoutId);
    };
  }, [downloadId]);

  const probeReady = probe?.status === "ready";
  const previewVideoId = selectedVideo?.id ?? "";

  useEffect(() => {
    if (!probeId || !previewVideoId || !qualityId || !probeReady) {
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
      qualityId,
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
  }, [probeId, previewVideoId, qualityId, probeReady]);

  async function onProbe(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setDownloadId(null);
    setProbe(null);
    setPreviewURL(null);
    setBusy(true);
    try {
      const res = await fetch("/api/probe", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url }),
      });
      if (!res.ok) throw new Error(await readError(res));
      const data = await res.json();
      setProbeId(data.id);
    } catch (err) {
      setBusy(false);
      setError(err instanceof Error ? err.message : "Probe failed");
    }
  }

  async function onDownload() {
    if (!probeId || !selectedVideo || !qualityId) return;
    setError(null);
    try {
      const res = await fetch("/api/download", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          probeId,
          videoId: selectedVideo.id,
          qualityId,
        }),
      });
      if (!res.ok) throw new Error(await readError(res));
      const data = await res.json();
      setDownloadId(data.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Download failed");
    }
  }

  async function onCancel(id: string) {
    setCancellingId(id);
    setError(null);
    try {
      const res = await fetch(`/api/download/${id}/cancel`, { method: "POST" });
      if (!res.ok) throw new Error(await readError(res));
      const data: DownloadJob = await res.json();
      setDownloads((prev) => {
        const rest = prev.filter((d) => d.id !== data.id);
        return [data, ...rest];
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Cancel failed");
    } finally {
      setCancellingId(null);
    }
  }

  const probing = probe?.status === "running";
  const ready = probe?.status === "ready" && videos.length > 0;
  const activeDownloads = downloads.filter(isActiveDownload);
  const recentDownloads = downloads.filter((d) => !isActiveDownload(d)).slice(0, 5);

  return (
    <div className="page">
      <header className="hero">
        <p className="brand">Viddown</p>
        <h1>Pull a stream into Filebrowser</h1>
        <p className="lede">
          Paste a page or m3u8 URL. Pick the video and quality, then download.
        </p>
      </header>

      <main className="panel">
        <form className="row" onSubmit={onProbe}>
          <label className="field grow">
            <span>URL</span>
            <input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://…"
              required
              disabled={busy && probing}
            />
          </label>
          <button type="submit" disabled={busy || !url.trim()}>
            {probing ? "Probing…" : "Probe"}
          </button>
        </form>

        {(probe?.message || probing) && (
          <p className="status">{probe?.message || "Working…"}</p>
        )}

        {ready && (
          <>
            <div className="selectors">
              <label className="field">
                <span>Video</span>
                <select
                  value={selectedVideo?.id ?? ""}
                  onChange={(e) => setVideoId(e.target.value)}
                  disabled={probing}
                >
                  {videos.map((v) => (
                    <option key={v.id} value={v.id}>
                      {v.label} ({v.qualities.length})
                    </option>
                  ))}
                </select>
              </label>

              <label className="field">
                <span>Quality</span>
                <select
                  value={qualityId}
                  onChange={(e) => setQualityId(e.target.value)}
                  disabled={probing || !selectedVideo}
                >
                  {(selectedVideo?.qualities ?? []).map((q) => (
                    <option key={q.id} value={q.id}>
                      {q.label || "Stream"}
                    </option>
                  ))}
                </select>
              </label>

              <button
                type="button"
                className="primary"
                onClick={onDownload}
                disabled={probing || !qualityId}
              >
                Download
              </button>
            </div>

            <div className="preview-row">
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
                <p className="meta-label">Will save as</p>
                <code className="filename">
                  {suggestedFileName(probe?.nameSlug, selectedQuality)}
                </code>
                {selectedQuality?.duration && (
                  <p className="status">Duration ~ {selectedQuality.duration}</p>
                )}
              </div>
            </div>
          </>
        )}

        {(activeDownloads.length > 0 || recentDownloads.length > 0) && (
          <section className="jobs">
            {activeDownloads.length > 0 && (
              <>
                <h2 className="jobs-title">
                  Running now
                  {activeDownloads.length > 0
                    ? ` (${activeDownloads.length})`
                    : ""}
                </h2>
                <p className="status">
                  Up to 10 downloads at once. They keep going if you close this
                  tab — use Cancel to stop them.
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
            {recentDownloads.length > 0 && (
              <>
                <h2 className="jobs-title">Recent</h2>
                {recentDownloads.map((d) => (
                  <div key={d.id} className="progress-block recent">
                    <div className="progress-meta">
                      <span className="job-label">
                        {d.label || d.fileName || d.id.slice(0, 8)}
                      </span>
                      <span className="mono">{d.status}</span>
                    </div>
                    {d.status === "done" && (d.fileName || d.filePath) && (
                      <p className="ok">
                        Saved → <code>{d.fileName || d.filePath}</code>
                      </p>
                    )}
                    {(d.status === "error" || d.status === "cancelled") && (
                      <p className="status">{d.message || d.status}</p>
                    )}
                  </div>
                ))}
              </>
            )}
          </section>
        )}

        {error && <p className="error">{error}</p>}
      </main>
    </div>
  );
}
