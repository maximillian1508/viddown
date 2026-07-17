CREATE INDEX IF NOT EXISTS idx_downloads_saved_at ON downloads(saved_at DESC);
CREATE INDEX IF NOT EXISTS idx_downloads_stream ON downloads(page_url, quality, stream_key);
CREATE INDEX IF NOT EXISTS idx_downloads_slug ON downloads(slug, video_id, quality);
CREATE INDEX IF NOT EXISTS idx_download_jobs_status ON download_jobs(status);
