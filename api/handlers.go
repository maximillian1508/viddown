package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
)

type App struct {
	cfg      Config
	store    *Store
	dlLog    *DownloadLog
	urlRules *URLRulesStore
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("POST /api/probe", a.handleProbeCreate)
	mux.HandleFunc("GET /api/probe/{id}", a.handleProbeGet)
	mux.HandleFunc("GET /api/preview", a.handlePreview)
	mux.HandleFunc("POST /api/download", a.handleDownloadCreate)
	mux.HandleFunc("GET /api/downloads", a.handleDownloadList)
	mux.HandleFunc("GET /api/download/{id}", a.handleDownloadGet)
	mux.HandleFunc("POST /api/download/{id}/cancel", a.handleDownloadCancel)
	mux.HandleFunc("POST /api/download/{id}/retry", a.handleDownloadRetry)
	mux.HandleFunc("GET /api/url-rules", a.handleURLRulesGet)
	mux.HandleFunc("PUT /api/url-rules", a.handleURLRulesPut)
	mux.HandleFunc("POST /api/url-rules/test", a.handleURLRulesTest)
	mux.Handle("/", a.spaHandler())
	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, ffmpegErr := exec.LookPath("ffmpeg")
	_, ffprobeErr := exec.LookPath("ffprobe")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              ffmpegErr == nil && ffprobeErr == nil,
		"ffmpeg":          ffmpegErr == nil,
		"ffprobe":         ffprobeErr == nil,
		"output":          a.cfg.OutputDir,
		"outputLabel":     a.cfg.OutputLabel,
		"filebrowserUrl":     a.cfg.FilebrowserURL,
		"libreTranslateUrl":  a.cfg.LibreTranslateURL,
		"translateTo":        a.cfg.TranslateTo,
		"maxDownloads":    a.cfg.MaxDownloads,
		"activeDownloads": a.store.ActiveDownloadCount(),
	})
}

func (a *App) handleProbeCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id, err := a.startProbe(body.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (a *App) handleProbeGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := a.probeJobForAPI(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "probe not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *App) handleDownloadCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProbeID   string `json:"probeId"`
		VideoID   string `json:"videoId"`
		QualityID string `json:"qualityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.ProbeID == "" || body.VideoID == "" || body.QualityID == "" {
		writeErr(w, http.StatusBadRequest, "probeId, videoId, qualityId required")
		return
	}
	id, err := a.startDownload(body.ProbeID, body.VideoID, body.QualityID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (a *App) handleDownloadGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := a.store.GetDownload(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "download not found")
		return
	}
	writeJSON(w, http.StatusOK, publicDownload(job, a.cfg.OutputLabel, a.cfg.FilebrowserURL))
}

func (a *App) handleDownloadList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"downloads": a.store.ListDownloads(a.cfg.OutputLabel, a.cfg.FilebrowserURL),
	})
}

func (a *App) handleDownloadCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.store.CancelDownload(id) {
		job, ok := a.store.GetDownload(id)
		if !ok {
			writeErr(w, http.StatusNotFound, "download not found")
			return
		}
		writeJSON(w, http.StatusOK, publicDownload(job, a.cfg.OutputLabel, a.cfg.FilebrowserURL))
		return
	}
	job, _ := a.store.GetDownload(id)
	writeJSON(w, http.StatusOK, publicDownload(job, a.cfg.OutputLabel, a.cfg.FilebrowserURL))
}

func (a *App) handleDownloadRetry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := a.store.GetDownload(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "download not found")
		return
	}
	if job.Status != "error" && job.Status != "cancelled" {
		writeErr(w, http.StatusBadRequest, "only failed or cancelled downloads can be retried")
		return
	}
	if job.ProbeID == "" || job.VideoID == "" || job.QualityID == "" {
		writeErr(w, http.StatusBadRequest, "this download has no retry metadata — start a new download from the probe list")
		return
	}
	newID, err := a.startDownload(job.ProbeID, job.VideoID, job.QualityID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.store.RemoveDownload(id)
	writeJSON(w, http.StatusAccepted, map[string]string{"id": newID})
}

func (a *App) spaHandler() http.Handler {
	fs := http.FS(webFS)
	fileServer := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		switch {
		case path == "manifest.webmanifest" || strings.HasSuffix(path, ".webmanifest"):
			w.Header().Set("Content-Type", "application/manifest+json")
		case path == "sw.js" || strings.HasPrefix(path, "workbox-"):
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Type", "application/javascript")
		}
		if f, err := webFS.Open(path); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
