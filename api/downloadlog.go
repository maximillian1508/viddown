package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const downloadLogName = "downloads.json"

type DownloadRecord struct {
	Slug      string    `json:"slug"`
	VideoID   string    `json:"videoId"`
	Quality   string    `json:"quality"`
	StreamKey string    `json:"streamKey,omitempty"`
	FileName  string    `json:"fileName"`
	PageURL   string    `json:"pageUrl,omitempty"`
	SavedAt   time.Time `json:"savedAt"`
}

type DownloadLog struct {
	path string
	mu   sync.Mutex
}

func NewDownloadLog(dataDir string) *DownloadLog {
	return &DownloadLog{path: filepath.Join(dataDir, downloadLogName)}
}

func (l *DownloadLog) load() ([]DownloadRecord, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []DownloadRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse download log: %w", err)
	}
	return records, nil
}

func (l *DownloadLog) save(records []DownloadRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".viddown-downloads-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, l.path)
}

// Upsert replaces an existing entry with the same pageUrl+videoId+quality (or slug fallback).
func (l *DownloadLog) Upsert(rec DownloadRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	records, err := l.load()
	if err != nil {
		return err
	}

	urlKey := urlRecordKey(rec.PageURL, rec.VideoID, rec.Quality)
	slugKey := slugRecordKey(rec.Slug, rec.VideoID, rec.Quality)
	skKey := streamRecordKey(rec.PageURL, rec.Quality, rec.StreamKey)
	replaced := false
	if skKey != "" {
		for i := range records {
			if streamRecordKey(records[i].PageURL, records[i].Quality, records[i].StreamKey) == skKey {
				records[i] = rec
				replaced = true
				break
			}
		}
	}
	if !replaced {
		for i := range records {
			if urlKey != "" && urlRecordKey(records[i].PageURL, records[i].VideoID, records[i].Quality) == urlKey {
				records[i] = rec
				replaced = true
				break
			}
		}
	}
	if !replaced && urlKey != "" {
		for i := range records {
			if records[i].PageURL == "" && slugRecordKey(records[i].Slug, records[i].VideoID, records[i].Quality) == slugKey {
				records[i] = rec
				replaced = true
				break
			}
		}
	}
	if !replaced && urlKey == "" {
		for i := range records {
			if slugRecordKey(records[i].Slug, records[i].VideoID, records[i].Quality) == slugKey {
				records[i] = rec
				replaced = true
				break
			}
		}
	}
	if !replaced {
		records = append(records, rec)
	}
	return l.save(records)
}

// Find returns the best log record for a stream (page URL + stream identity + quality).
func (l *DownloadLog) Find(pageURL, slug, videoID, quality, streamK string) (*DownloadRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	records, err := l.load()
	if err != nil {
		return nil, err
	}

	skKey := streamRecordKey(pageURL, quality, streamK)
	urlKey := urlRecordKey(pageURL, videoID, quality)
	slugKey := slugRecordKey(slug, videoID, quality)

	var best *DownloadRecord
	pick := func(rec *DownloadRecord) {
		if best == nil || rec.SavedAt.After(best.SavedAt) {
			best = rec
		}
	}

	if skKey != "" {
		for i := range records {
			if streamRecordKey(records[i].PageURL, records[i].Quality, records[i].StreamKey) != skKey {
				continue
			}
			pick(&records[i])
		}
	}
	if best != nil {
		return best, nil
	}

	if urlKey != "" {
		for i := range records {
			if urlRecordKey(records[i].PageURL, records[i].VideoID, records[i].Quality) != urlKey {
				continue
			}
			pick(&records[i])
		}
	}
	if best != nil {
		return best, nil
	}

	if slug != "" {
		for i := range records {
			if slugRecordKey(records[i].Slug, records[i].VideoID, records[i].Quality) != slugKey {
				continue
			}
			pick(&records[i])
		}
	}
	if best != nil {
		return best, nil
	}

	// One saved file for this page+quality → label any matching stream (legacy logs without streamKey).
	if normalizePageURL(pageURL) != "" && quality != "" {
		var sole *DownloadRecord
		for i := range records {
			if records[i].Quality != quality {
				continue
			}
			if normalizePageURL(records[i].PageURL) != normalizePageURL(pageURL) {
				continue
			}
			if sole != nil {
				sole = nil
				break
			}
			sole = &records[i]
		}
		if sole != nil {
			return sole, nil
		}
	}

	return nil, nil
}

func normalizePageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}
	return u.Scheme + "://" + u.Host + u.Path + u.RawQuery
}

func urlRecordKey(pageURL, videoID, quality string) string {
	if norm := normalizePageURL(pageURL); norm != "" {
		return norm + "\x00" + videoID + "\x00" + quality
	}
	return ""
}

func slugRecordKey(slug, videoID, quality string) string {
	return slug + "\x00" + videoID + "\x00" + quality
}

func streamRecordKey(pageURL, quality, streamK string) string {
	if streamK == "" || quality == "" {
		return ""
	}
	if norm := normalizePageURL(pageURL); norm != "" {
		return norm + "\x00" + quality + "\x00" + streamK
	}
	return ""
}

// streamKey normalizes an HLS URL so the same stream matches across probes (ignores auth_key etc.).
func streamKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	q := u.Query()
	for _, k := range []string{"auth_key", "token", "expires", "t", "sign", "e", "time", "rand"} {
		q.Del(k)
	}
	u.RawQuery = q.Encode()
	return u.Host + u.Path + "?" + u.RawQuery
}

var knownQualities = map[string]bool{
	"2160p": true, "1440p": true, "1080p": true, "720p": true,
	"480p": true, "360p": true, "240p": true,
}

var (
	reJobID     = regexp.MustCompile(`^[a-f0-9]{8}$`)
	reVideoID   = regexp.MustCompile(`^v\d+$`)
	reDate      = regexp.MustCompile(`^\d{8}$`)
	reTimeOfDay = regexp.MustCompile(`^\d{6}$`)
)

// parseDownloadFileName extracts slug, videoId, and quality from a viddown-style filename.
func parseDownloadFileName(name string) (slug, videoID, quality string, ok bool) {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".mp4") {
		return
	}
	base := name[:len(name)-4]
	parts := strings.Split(base, "_")
	if len(parts) < 2 {
		return
	}

	if reJobID.MatchString(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	if len(parts) >= 2 && reDate.MatchString(parts[len(parts)-2]) && reTimeOfDay.MatchString(parts[len(parts)-1]) {
		parts = parts[:len(parts)-2]
	} else if len(parts) >= 1 && reDate.MatchString(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return
	}

	if reVideoID.MatchString(parts[len(parts)-1]) {
		videoID = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return
	}

	quality = parts[len(parts)-1]
	if !knownQualities[quality] {
		return
	}
	slug = strings.Join(parts[:len(parts)-1], "_")
	if slug == "" {
		return
	}
	if videoID == "" {
		videoID = "v1"
	}
	return slug, videoID, quality, true
}

// SeedFromOutputDir imports existing *.mp4 filenames into the log (one-time / testing).
func (l *DownloadLog) SeedFromOutputDir(outputDir string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	matches, err := filepath.Glob(filepath.Join(outputDir, "*.mp4"))
	if err != nil {
		return 0, err
	}

	records, err := l.load()
	if err != nil {
		return 0, err
	}
	byKey := make(map[string]DownloadRecord, len(records))
	for _, rec := range records {
		key := urlRecordKey(rec.PageURL, rec.VideoID, rec.Quality)
		if key == "" {
			key = "slug:" + slugRecordKey(rec.Slug, rec.VideoID, rec.Quality)
		}
		byKey[key] = rec
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
		key := "slug:" + slugRecordKey(slug, videoID, quality)
		rec := DownloadRecord{
			Slug:     slug,
			VideoID:  videoID,
			Quality:  quality,
			FileName: name,
			SavedAt:  fi.ModTime(),
		}
		prev, exists := byKey[key]
		if exists && !rec.SavedAt.After(prev.SavedAt) {
			continue
		}
		if !exists {
			added++
		}
		byKey[key] = rec
	}

	if len(byKey) == 0 {
		return 0, nil
	}
	out := make([]DownloadRecord, 0, len(byKey))
	for _, rec := range byKey {
		out = append(out, rec)
	}
	if err := l.save(out); err != nil {
		return 0, err
	}
	return added, nil
}

func (l *DownloadLog) isEmpty() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	records, err := l.load()
	return err != nil || len(records) == 0
}

// migrateFromLegacy imports records from the old output-dir log if the new log is empty.
func (l *DownloadLog) migrateFromLegacy(legacyPath string) (int, error) {
	if !l.isEmpty() {
		return 0, nil
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var records []DownloadRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return 0, fmt.Errorf("parse legacy download log: %w", err)
	}
	if len(records) == 0 {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.save(records); err != nil {
		return 0, err
	}
	return len(records), nil
}

func (a *App) annotateExistingFromLog(videos []Video, pageURL, slug string) {
	if a.dlLog == nil || (pageURL == "" && slug == "") {
		return
	}
	for i := range videos {
		for j := range videos[i].Qualities {
			q := &videos[i].Qualities[j]
			quality := prettyResolution(q.Resolution)
			rec, err := a.dlLog.Find(pageURL, slug, videos[i].ID, quality, streamKey(q.URL))
			if err != nil || rec == nil {
				continue
			}
			q.OnDisk = true
			q.OnDiskFile = rec.FileName
		}
	}
}

func (a *App) probeJobForAPI(id string) (*ProbeJob, bool) {
	job, ok := a.store.GetProbe(id)
	if !ok {
		return nil, false
	}
	if job.Status != "ready" || len(job.Videos) == 0 {
		return job, true
	}
	resp := *job
	videos := cloneVideosForAnnotate(job.Videos)
	a.annotateExistingFromLog(videos, job.PageURL, job.NameSlug)
	resp.Videos = videos
	return &resp, true
}

func cloneVideosForAnnotate(src []Video) []Video {
	out := make([]Video, len(src))
	for i, v := range src {
		out[i] = v
		out[i].Qualities = append([]Quality(nil), v.Qualities...)
	}
	return out
}

func (a *App) recordDownload(jobID string) {
	if a.dlLog == nil {
		return
	}
	job, ok := a.store.GetDownload(jobID)
	if !ok || job.FileName == "" {
		return
	}

	slug := "video"
	pageURL := ""
	if probe, ok := a.store.GetProbe(job.ProbeID); ok {
		if probe.NameSlug != "" {
			slug = probe.NameSlug
		}
		pageURL = probe.PageURL
	}

	quality := ""
	streamK := ""
	if q, _, ok := a.store.FindQuality(job.ProbeID, job.VideoID, job.QualityID); ok {
		quality = prettyResolution(q.Resolution)
		streamK = streamKey(q.URL)
	}

	_ = a.dlLog.Upsert(DownloadRecord{
		Slug:      slug,
		VideoID:   job.VideoID,
		Quality:   quality,
		StreamKey: streamK,
		FileName:  job.FileName,
		PageURL:   pageURL,
		SavedAt:   time.Now(),
	})
}
