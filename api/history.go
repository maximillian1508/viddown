package main

import (
	"net/http"
	"sort"
	"strconv"
)

const defaultHistoryLimit = 15
const maxHistoryLimit = 100

func parseHistoryQuery(r *http.Request) (limit, offset int) {
	limit = defaultHistoryLimit
	offset = 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > maxHistoryLimit {
				limit = maxHistoryLimit
			}
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func isActiveJobStatus(status string) bool {
	return status == "queued" || status == "running"
}

func (a *App) listDownloadHistory(limit, offset int) (active, history []DownloadJob, total int, err error) {
	active = make([]DownloadJob, 0)
	history = make([]DownloadJob, 0)
	jobs, err := a.db.ListDownloadJobs(0)
	if err != nil {
		return nil, nil, 0, err
	}
	outLabel, fbURL := a.cfg.OutputLabel, a.cfg.FilebrowserURL
	for i := range jobs {
		jobs[i] = publicDownload(&jobs[i], outLabel, fbURL)
	}
	all := a.mergeSavedIntoHistory(jobs)

	for _, j := range all {
		if isActiveJobStatus(j.Status) {
			active = append(active, j)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].CreatedAt.After(active[j].CreatedAt)
	})

	finished := make([]DownloadJob, 0, len(all))
	for _, j := range all {
		if !isActiveJobStatus(j.Status) {
			finished = append(finished, j)
		}
	}
	total = len(finished)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	if offset < end {
		history = finished[offset:end]
	}
	return active, history, total, nil
}
