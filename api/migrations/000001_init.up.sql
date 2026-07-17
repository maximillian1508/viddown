CREATE TABLE IF NOT EXISTS url_rules (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  match_pattern TEXT NOT NULL,
  replace_pattern TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS probes (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  page_url TEXT NOT NULL DEFAULT '',
  page_title TEXT NOT NULL DEFAULT '',
  name_slug TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_probes_created ON probes(created_at DESC);

CREATE TABLE IF NOT EXISTS download_jobs (
  id TEXT PRIMARY KEY,
  probe_id TEXT REFERENCES probes(id) ON DELETE SET NULL,
  video_id TEXT NOT NULL DEFAULT '',
  quality_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  progress REAL NOT NULL DEFAULT 0,
  file_name TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_download_jobs_probe ON download_jobs(probe_id);
CREATE INDEX IF NOT EXISTS idx_download_jobs_updated ON download_jobs(updated_at DESC);

CREATE TABLE IF NOT EXISTS downloads (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  download_job_id TEXT REFERENCES download_jobs(id) ON DELETE SET NULL,
  slug TEXT NOT NULL,
  video_id TEXT NOT NULL,
  quality TEXT NOT NULL,
  stream_key TEXT NOT NULL DEFAULT '',
  file_name TEXT NOT NULL UNIQUE,
  page_url TEXT NOT NULL DEFAULT '',
  saved_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_downloads_lookup ON downloads(page_url, video_id, quality);
