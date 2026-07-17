package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const dbFileName = "viddown.db"

type Database struct {
	sql *sql.DB
}

func OpenDatabase(dataDir string) (*Database, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, dbFileName)
	if err := runMigrations(dbPath); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	return &Database{sql: sqlDB}, nil
}

func (db *Database) Close() error {
	if db == nil || db.sql == nil {
		return nil
	}
	return db.sql.Close()
}

func fmtTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

// --- URL rules ---

func (db *Database) LoadURLRules() (URLRulesConfig, error) {
	rows, err := db.sql.Query(`SELECT id, name, enabled, match_pattern, replace_pattern
		FROM url_rules ORDER BY sort_order, rowid`)
	if err != nil {
		return URLRulesConfig{}, err
	}
	defer rows.Close()
	var rules []URLRule
	for rows.Next() {
		var r URLRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &enabled, &r.Match, &r.Replace); err != nil {
			return URLRulesConfig{}, err
		}
		r.Enabled = enabled != 0
		rules = append(rules, r)
	}
	if rules == nil {
		rules = []URLRule{}
	}
	return URLRulesConfig{Rules: rules}, rows.Err()
}

func (db *Database) SaveURLRules(cfg URLRulesConfig) error {
	if err := validateURLRulesConfig(cfg); err != nil {
		return err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM url_rules`); err != nil {
		return err
	}
	for i, r := range cfg.Rules {
		enabled := 0
		if r.Enabled {
			enabled = 1
		}
		if _, err := tx.Exec(`INSERT INTO url_rules(id, name, enabled, match_pattern, replace_pattern, sort_order)
			VALUES (?, ?, ?, ?, ?, ?)`,
			r.ID, r.Name, enabled, r.Match, r.Replace, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Probes ---

type probePayload struct {
	Videos []Video `json:"videos"`
}

func (db *Database) SaveProbe(job *ProbeJob) error {
	if job == nil {
		return nil
	}
	payload, err := json.Marshal(probePayload{Videos: job.Videos})
	if err != nil {
		return err
	}
	now := fmtTime(time.Now())
	created := fmtTime(job.CreatedAt)
	if job.CreatedAt.IsZero() {
		created = now
	}
	_, err = db.sql.Exec(`INSERT INTO probes(id, status, message, page_url, page_title, name_slug, payload_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			message=excluded.message,
			page_url=excluded.page_url,
			page_title=excluded.page_title,
			name_slug=excluded.name_slug,
			payload_json=excluded.payload_json,
			updated_at=excluded.updated_at`,
		job.ID, job.Status, job.Message, job.PageURL, job.PageTitle, job.NameSlug, string(payload), created, now)
	return err
}

func (db *Database) GetProbe(id string) (*ProbeJob, error) {
	row := db.sql.QueryRow(`SELECT id, status, message, page_url, page_title, name_slug, payload_json, created_at
		FROM probes WHERE id = ?`, id)
	var job ProbeJob
	var payloadRaw string
	var created string
	if err := row.Scan(&job.ID, &job.Status, &job.Message, &job.PageURL, &job.PageTitle, &job.NameSlug, &payloadRaw, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	job.CreatedAt = parseTime(created)
	var payload probePayload
	if payloadRaw != "" && payloadRaw != "[]" {
		_ = json.Unmarshal([]byte(payloadRaw), &payload)
	}
	job.Videos = payload.Videos
	return &job, nil
}

func (db *Database) ListRecentProbes(limit int) ([]ProbeJob, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.sql.Query(`SELECT id, status, message, page_url, page_title, name_slug, payload_json, created_at
		FROM probes ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeJob
	for rows.Next() {
		var job ProbeJob
		var payloadRaw, created string
		if err := rows.Scan(&job.ID, &job.Status, &job.Message, &job.PageURL, &job.PageTitle, &job.NameSlug, &payloadRaw, &created); err != nil {
			return nil, err
		}
		job.CreatedAt = parseTime(created)
		var payload probePayload
		if payloadRaw != "" {
			_ = json.Unmarshal([]byte(payloadRaw), &payload)
		}
		job.Videos = payload.Videos
		out = append(out, job)
	}
	return out, rows.Err()
}

func (db *Database) PruneOldProbes(keep int) error {
	if keep <= 0 {
		keep = 50
	}
	_, err := db.sql.Exec(`DELETE FROM probes WHERE id NOT IN (
		SELECT id FROM probes ORDER BY created_at DESC LIMIT ?
	)`, keep)
	return err
}

// --- Download jobs ---

func (db *Database) SaveDownloadJob(job *DownloadJob) error {
	if job == nil {
		return nil
	}
	now := fmtTime(time.Now())
	created := fmtTime(job.CreatedAt)
	if job.CreatedAt.IsZero() {
		created = now
	}
	_, err := db.sql.Exec(`INSERT INTO download_jobs(id, probe_id, video_id, quality_id, status, message, label, progress, file_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			message=excluded.message,
			label=excluded.label,
			progress=excluded.progress,
			file_name=excluded.file_name,
			updated_at=excluded.updated_at`,
		job.ID, nullIfEmpty(job.ProbeID), job.VideoID, job.QualityID, job.Status, job.Message, job.Label, job.Progress, job.FileName, created, now)
	return err
}

func (db *Database) GetDownloadJob(id string) (*DownloadJob, error) {
	row := db.sql.QueryRow(`SELECT id, probe_id, video_id, quality_id, status, message, label, progress, file_name, created_at
		FROM download_jobs WHERE id = ?`, id)
	var job DownloadJob
	var probeID sql.NullString
	var created string
	if err := row.Scan(&job.ID, &probeID, &job.VideoID, &job.QualityID, &job.Status, &job.Message, &job.Label, &job.Progress, &job.FileName, &created); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if probeID.Valid {
		job.ProbeID = probeID.String
	}
	job.CreatedAt = parseTime(created)
	return &job, nil
}

func (db *Database) ListDownloadJobs(limit int) ([]DownloadJob, error) {
	query := `SELECT id, probe_id, video_id, quality_id, status, message, label, progress, file_name, created_at
		FROM download_jobs ORDER BY updated_at DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = db.sql.Query(query+` LIMIT ?`, limit)
	} else {
		rows, err = db.sql.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DownloadJob
	for rows.Next() {
		var job DownloadJob
		var probeID sql.NullString
		var created string
		if err := rows.Scan(&job.ID, &probeID, &job.VideoID, &job.QualityID, &job.Status, &job.Message, &job.Label, &job.Progress, &job.FileName, &created); err != nil {
			return nil, err
		}
		if probeID.Valid {
			job.ProbeID = probeID.String
		}
		job.CreatedAt = parseTime(created)
		out = append(out, job)
	}
	return out, rows.Err()
}

func (db *Database) DeleteDownloadJob(id string) error {
	_, err := db.sql.Exec(`DELETE FROM download_jobs WHERE id = ?`, id)
	return err
}

func (db *Database) MarkInterruptedJobs() (int64, error) {
	res, err := db.sql.Exec(`UPDATE download_jobs SET status = 'error',
		message = 'Interrupted — server restarted',
		updated_at = ?
		WHERE status IN ('queued', 'running')`, fmtTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Saved downloads (completed videos) ---

func (db *Database) UpsertDownload(rec DownloadRecord, jobID string) error {
	urlKey := urlRecordKey(rec.PageURL, rec.VideoID, rec.Quality)
	slugKey := slugRecordKey(rec.Slug, rec.VideoID, rec.Quality)
	skKey := streamRecordKey(rec.PageURL, rec.Quality, rec.StreamKey)

	var existingID int64
	var existingJobID sql.NullString

	// Same dedup logic as the old JSON log.
	if skKey != "" {
		row := db.sql.QueryRow(`SELECT id, download_job_id FROM downloads WHERE page_url = ? AND quality = ? AND stream_key = ? LIMIT 1`,
			normalizePageURL(rec.PageURL), rec.Quality, rec.StreamKey)
		_ = row.Scan(&existingID, &existingJobID)
	}
	if existingID == 0 && urlKey != "" {
		row := db.sql.QueryRow(`SELECT id, download_job_id FROM downloads WHERE page_url = ? AND video_id = ? AND quality = ? LIMIT 1`,
			normalizePageURL(rec.PageURL), rec.VideoID, rec.Quality)
		_ = row.Scan(&existingID, &existingJobID)
	}
	if existingID == 0 && urlKey == "" {
		row := db.sql.QueryRow(`SELECT id, download_job_id FROM downloads WHERE slug = ? AND video_id = ? AND quality = ? LIMIT 1`,
			rec.Slug, rec.VideoID, rec.Quality)
		_ = row.Scan(&existingID, &existingJobID)
	}
	if existingID == 0 && slugKey != "" {
		row := db.sql.QueryRow(`SELECT id, download_job_id FROM downloads WHERE slug = ? AND video_id = ? AND quality = ? AND page_url = '' LIMIT 1`,
			rec.Slug, rec.VideoID, rec.Quality)
		_ = row.Scan(&existingID, &existingJobID)
	}

	jobRef := nullIfEmpty(jobID)
	savedAt := fmtTime(rec.SavedAt)
	if rec.SavedAt.IsZero() {
		savedAt = fmtTime(time.Now())
	}

	if existingID > 0 {
		if !jobRef.Valid && existingJobID.Valid {
			jobRef = existingJobID
		}
		_, err := db.sql.Exec(`UPDATE downloads SET slug=?, video_id=?, quality=?, stream_key=?, file_name=?, page_url=?, saved_at=?, download_job_id=COALESCE(?, download_job_id)
			WHERE id=?`,
			rec.Slug, rec.VideoID, rec.Quality, rec.StreamKey, rec.FileName, rec.PageURL, savedAt, jobRef, existingID)
		return err
	}
	_, err := db.sql.Exec(`INSERT INTO downloads(download_job_id, slug, video_id, quality, stream_key, file_name, page_url, saved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		jobRef, rec.Slug, rec.VideoID, rec.Quality, rec.StreamKey, rec.FileName, rec.PageURL, savedAt)
	return err
}

func (db *Database) ListSavedDownloads(limit int) ([]DownloadRecord, error) {
	query := `SELECT slug, video_id, quality, stream_key, file_name, page_url, saved_at
		FROM downloads ORDER BY saved_at DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = db.sql.Query(query+` LIMIT ?`, limit)
	} else {
		rows, err = db.sql.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DownloadRecord
	for rows.Next() {
		var rec DownloadRecord
		var saved string
		if err := rows.Scan(&rec.Slug, &rec.VideoID, &rec.Quality, &rec.StreamKey, &rec.FileName, &rec.PageURL, &saved); err != nil {
			return nil, err
		}
		rec.SavedAt = parseTime(saved)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (db *Database) loadAllDownloads() ([]DownloadRecord, error) {
	rows, err := db.sql.Query(`SELECT slug, video_id, quality, stream_key, file_name, page_url, saved_at FROM downloads`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DownloadRecord
	for rows.Next() {
		var rec DownloadRecord
		var saved string
		if err := rows.Scan(&rec.Slug, &rec.VideoID, &rec.Quality, &rec.StreamKey, &rec.FileName, &rec.PageURL, &saved); err != nil {
			return nil, err
		}
		rec.SavedAt = parseTime(saved)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (db *Database) downloadsEmpty() (bool, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM downloads`).Scan(&n)
	return n == 0, err
}

func (db *Database) urlRulesEmpty() (bool, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM url_rules`).Scan(&n)
	return n == 0, err
}

func nullIfEmpty(s string) sql.NullString {
	if strings.TrimSpace(s) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// ImportJSONIfEmpty migrates legacy JSON files into SQLite on first run.
func (db *Database) ImportJSONIfEmpty(dataDir, outputDir string) error {
	if empty, err := db.downloadsEmpty(); err != nil {
		return err
	} else if empty {
		if n, err := db.importDownloadsJSON(filepath.Join(dataDir, downloadLogName)); err != nil {
			return err
		} else if n > 0 {
			log.Printf("imported %d downloads from %s", n, downloadLogName)
		}
		legacy := filepath.Join(outputDir, ".viddown-downloads.json")
		if n, err := db.importDownloadsJSON(legacy); err != nil {
			return err
		} else if n > 0 {
			log.Printf("imported %d downloads from legacy log", n)
		}
		if empty, _ := db.downloadsEmpty(); empty {
			if n, err := db.seedDownloadsFromOutput(outputDir); err != nil {
				return err
			} else if n > 0 {
				log.Printf("seeded %d downloads from output dir", n)
			}
		}
	}

	if empty, err := db.urlRulesEmpty(); err != nil {
		return err
	} else if empty {
		if n, err := db.importURLRulesJSON(filepath.Join(dataDir, urlRulesFile)); err != nil {
			return err
		} else if n > 0 {
			log.Printf("imported %d url rules", n)
		}
	}
	return nil
}

func (db *Database) importDownloadsJSON(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var records []DownloadRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return 0, err
	}
	for _, rec := range records {
		if err := db.UpsertDownload(rec, ""); err != nil {
			return 0, err
		}
	}
	return len(records), nil
}

func (db *Database) importURLRulesJSON(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var cfg URLRulesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, err
	}
	for i := range cfg.Rules {
		if cfg.Rules[i].ID == "" {
			cfg.Rules[i].ID = uuid.NewString()
		}
	}
	if len(cfg.Rules) == 0 {
		return 0, nil
	}
	if err := db.SaveURLRules(cfg); err != nil {
		return 0, err
	}
	return len(cfg.Rules), nil
}

func (db *Database) seedDownloadsFromOutput(outputDir string) (int, error) {
	matches, err := filepath.Glob(filepath.Join(outputDir, "*.mp4"))
	if err != nil {
		return 0, err
	}
	added := 0
	for _, path := range matches {
		name := filepath.Base(path)
		slug, videoID, quality, ok := parseDownloadFileName(name)
		if !ok {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if err := db.UpsertDownload(DownloadRecord{
			Slug:     slug,
			VideoID:  videoID,
			Quality:  quality,
			FileName: name,
			SavedAt:  fi.ModTime(),
		}, ""); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}
