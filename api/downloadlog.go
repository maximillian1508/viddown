package main

import (
	"net/url"
	"regexp"
	"strings"
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

func findDownloadRecord(records []DownloadRecord, pageURL, slug, videoID, quality, streamK string) *DownloadRecord {
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
		return best
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
		return best
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
		return best
	}

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
			return sole
		}
	}

	return nil
}

func (db *Database) FindDownload(pageURL, slug, videoID, quality, streamK string) (*DownloadRecord, error) {
	records, err := db.loadAllDownloads()
	if err != nil {
		return nil, err
	}
	if rec := findDownloadRecord(records, pageURL, slug, videoID, quality, streamK); rec != nil {
		cp := *rec
		return &cp, nil
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

func (a *App) annotateExistingFromLog(videos []Video, pageURL, slug string) {
	if a.db == nil || (pageURL == "" && slug == "") {
		return
	}
	for i := range videos {
		for j := range videos[i].Qualities {
			q := &videos[i].Qualities[j]
			quality := prettyResolution(q.Resolution)
			rec, err := a.db.FindDownload(pageURL, slug, videos[i].ID, quality, streamKey(q.URL))
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
	if !ok && a.db != nil {
		loaded, err := a.db.GetProbe(id)
		if err != nil || loaded == nil {
			return nil, false
		}
		a.store.PutProbe(loaded, false)
		job, ok = loaded, true
	}
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
	if a.db == nil {
		return
	}
	job, ok := a.store.GetDownload(jobID)
	if !ok {
		loaded, err := a.db.GetDownloadJob(jobID)
		if err != nil || loaded == nil {
			return
		}
		job = loaded
	}
	if job.FileName == "" {
		return
	}

	slug := "video"
	pageURL := ""
	if probe, ok := a.store.GetProbe(job.ProbeID); ok {
		if probe.NameSlug != "" {
			slug = probe.NameSlug
		}
		pageURL = probe.PageURL
	} else if a.db != nil {
		if probe, err := a.db.GetProbe(job.ProbeID); err == nil && probe != nil {
			if probe.NameSlug != "" {
				slug = probe.NameSlug
			}
			pageURL = probe.PageURL
		}
	}

	quality := ""
	streamK := ""
	if q, _, ok := a.store.FindQuality(job.ProbeID, job.VideoID, job.QualityID); ok {
		quality = prettyResolution(q.Resolution)
		streamK = streamKey(q.URL)
	}

	_ = a.db.UpsertDownload(DownloadRecord{
		Slug:      slug,
		VideoID:   job.VideoID,
		Quality:   quality,
		StreamKey: streamK,
		FileName:  job.FileName,
		PageURL:   pageURL,
		SavedAt:   time.Now(),
	}, jobID)
}
