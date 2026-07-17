DROP INDEX IF EXISTS idx_downloads_lookup;
DROP TABLE IF EXISTS downloads;

DROP INDEX IF EXISTS idx_download_jobs_updated;
DROP INDEX IF EXISTS idx_download_jobs_probe;
DROP TABLE IF EXISTS download_jobs;

DROP INDEX IF EXISTS idx_probes_created;
DROP TABLE IF EXISTS probes;

DROP TABLE IF EXISTS url_rules;
