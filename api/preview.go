package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	probeID := r.URL.Query().Get("probeId")
	videoID := r.URL.Query().Get("videoId")
	qualityID := r.URL.Query().Get("qualityId")
	if probeID == "" || videoID == "" || qualityID == "" {
		writeErr(w, http.StatusBadRequest, "probeId, videoId, qualityId required")
		return
	}

	q, _, ok := a.store.FindQuality(probeID, videoID, qualityID)
	if !ok {
		writeErr(w, http.StatusNotFound, "quality not found")
		return
	}

	format := r.URL.Query().Get("format")
	if format == "mp4" {
		a.writeClipPreview(w, *q)
		return
	}
	a.writeThumbPreview(w, *q)
}

func (a *App) writeThumbPreview(w http.ResponseWriter, q Quality) {
	tmp, err := os.MkdirTemp("", "viddown-preview-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp dir failed")
		return
	}
	defer os.RemoveAll(tmp)

	outJPG := filepath.Join(tmp, "thumb.jpg")

	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()

	attempts := [][]string{
		{"-frames:v", "1", "-update", "1", "-q:v", "4", outJPG},
		{"-ss", "1", "-frames:v", "1", "-update", "1", "-q:v", "4", outJPG},
	}

	var lastErr error
	var lastOut []byte
	for _, extra := range attempts {
		if ctx.Err() != nil {
			break
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-y"}
		args = appendInputArgs(args, q.Headers, q.URL)
		args = append(args, extra...)

		cmd := ffmpegCommand(ctx, args...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			if data, readErr := os.ReadFile(outJPG); readErr == nil && len(data) > 0 {
				w.Header().Set("Content-Type", "image/jpeg")
				w.Header().Set("Cache-Control", "private, max-age=120")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(data)
				return
			}
			lastErr = fmt.Errorf("empty output")
		} else {
			lastErr = err
			lastOut = out
		}
		_ = os.Remove(outJPG)
	}

	writeErr(w, http.StatusBadGateway, fmt.Sprintf("preview failed: %v %s", lastErr, string(lastOut)))
}

func (a *App) writeClipPreview(w http.ResponseWriter, q Quality) {
	tmp, err := os.MkdirTemp("", "viddown-clip-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp dir failed")
		return
	}
	defer os.RemoveAll(tmp)

	outMP4 := filepath.Join(tmp, "clip.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	args = appendInputArgs(args, q.Headers, q.URL)
	args = append(args,
		"-t", "3",
		"-an",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "30",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		outMP4,
	)

	cmd := ffmpegCommand(ctx, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("clip failed: %v %s", err, string(out)))
		return
	}

	data, err := os.ReadFile(outMP4)
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadGateway, "clip empty")
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=120")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
