package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type fileMeta struct {
	Title     string
	SourceURL string
}

func (a *App) startDownload(probeID, videoID, qualityID string) (string, error) {
	if _, ok := a.store.GetProbe(probeID); !ok {
		return "", fmt.Errorf("probe expired — re-probe the page and try again (sessions last ~30 minutes)")
	}
	q, videoLabel, ok := a.store.FindQuality(probeID, videoID, qualityID)
	if !ok {
		return "", fmt.Errorf("video or quality not found — re-probe if the stream list changed")
	}

	slug := "video"
	var meta fileMeta
	if probe, ok := a.store.GetProbe(probeID); ok {
		if probe.NameSlug != "" {
			slug = probe.NameSlug
		}
		meta.SourceURL = probe.PageURL
		meta.Title = probe.PageTitle
	}

	id := uuid.NewString()
	fileName := buildDownloadFileName(slug, q.Resolution, videoID, id[:8])
	label := slug
	if videoLabel != "" {
		label = videoLabel
	}
	if q.Label != "" {
		label = label + " · " + q.Label
	}
	if meta.Title == "" {
		meta.Title = label
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &DownloadJob{
		ID:        id,
		Status:    "queued",
		Message:   "Queued…",
		Label:     label,
		FileName:  fileName,
		ProbeID:   probeID,
		VideoID:   videoID,
		QualityID: qualityID,
		CreatedAt: time.Now(),
		cancel:    cancel,
	}
	a.store.PutDownload(job)

	go func() {
		select {
		case a.store.dlSem <- struct{}{}:
			defer func() { <-a.store.dlSem }()
		default:
			cancel()
			a.store.UpdateDownload(id, func(j *DownloadJob) {
				j.Status = "error"
				j.Message = fmt.Sprintf("Download limit reached (%d concurrent). Cancel one or wait.", a.store.MaxDownloads())
			})
			return
		}
		if job, ok := a.store.GetDownload(id); ok && job.Status == "cancelled" {
			return
		}
		a.runDownload(id, q, fileName, meta, ctx)
	}()

	return id, nil
}

func (a *App) runDownload(jobID string, q *Quality, fileName string, meta fileMeta, ctx context.Context) {
	if job, ok := a.store.GetDownload(jobID); ok && job.Status == "cancelled" {
		return
	}

	a.store.UpdateDownload(jobID, func(j *DownloadJob) {
		if j.Status == "cancelled" {
			return
		}
		j.Status = "running"
		j.Message = "Connecting to stream…"
		j.Progress = 0
	})
	if job, ok := a.store.GetDownload(jobID); ok && job.Status == "cancelled" {
		return
	}

	if err := os.MkdirAll(a.cfg.OutputDir, 0o755); err != nil {
		a.failDownload(jobID, fmt.Sprintf("output dir: %v", err))
		return
	}

	outPath := filepath.Join(a.cfg.OutputDir, fileName)

	// Quick duration probe (short timeout) so % can show immediately; don't block long.
	a.store.UpdateDownload(jobID, func(j *DownloadJob) {
		if j.Status == "cancelled" {
			return
		}
		j.Message = "Checking stream…"
	})
	durationMS := probeDurationMS(q.URL, q.Headers)

	if ctx.Err() != nil {
		_ = os.Remove(outPath)
		a.store.UpdateDownload(jobID, func(j *DownloadJob) {
			j.Status = "cancelled"
			j.Message = "Cancelled"
		})
		return
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
	}
	args = appendInputArgs(args, q.Headers, q.URL)
	args = append(args,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "+faststart",
	)
	args = appendOutputMetadata(args, meta.Title, meta.SourceURL)
	args = append(args,
		"-progress", "pipe:1",
		"-nostats",
		"-y",
		outPath,
	)

	cmd := ffmpegCommand(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.failDownload(jobID, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.failDownload(jobID, err.Error())
		return
	}

	if err := cmd.Start(); err != nil {
		a.failDownload(jobID, fmt.Sprintf("ffmpeg start: %v", err))
		return
	}

	a.store.UpdateDownload(jobID, func(j *DownloadJob) {
		if j.Status == "cancelled" {
			return
		}
		j.Message = "Downloading…"
	})

	var stderrBuf strings.Builder
	var stderrWG sync.WaitGroup
	stderrWG.Add(1)
	go func() {
		defer stderrWG.Done()
		_, _ = io.Copy(&stderrBuf, io.LimitReader(stderr, 64<<10))
	}()

	sc := bufio.NewScanner(stdout)
	started := time.Now()
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "out_time_ms=") {
			continue
		}
		v := strings.TrimPrefix(line, "out_time_ms=")
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < 0 {
			continue
		}

		pct := 0.0
		msg := "Downloading…"
		if durationMS > 0 {
			pct = (n / durationMS) * 100
			if pct > 99 {
				pct = 99
			}
			if pct < 1 && n > 0 {
				pct = 1
			}
			msg = fmt.Sprintf("Downloading… %.0f%%", pct)
		} else if n > 0 {
			// No duration: show elapsed media time and a slow-rising bar so it doesn't look stuck.
			sec := n / 1_000_000
			msg = fmt.Sprintf("Downloading… %s", formatDuration(sec))
			elapsed := time.Since(started).Seconds()
			pct = 5 + (elapsed/120)*80 // approaches ~85% over ~2 minutes
			if pct > 90 {
				pct = 90
			}
		}
		a.store.UpdateDownload(jobID, func(j *DownloadJob) {
			if j.Status == "cancelled" {
				return
			}
			j.Progress = pct
			j.Message = msg
		})
	}

	waitErr := cmd.Wait()
	stderrWG.Wait()

	if ctx.Err() != nil {
		_ = os.Remove(outPath)
		a.store.UpdateDownload(jobID, func(j *DownloadJob) {
			j.Status = "cancelled"
			j.Message = "Cancelled"
		})
		return
	}
	if waitErr != nil {
		_ = os.Remove(outPath)
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		if lines := strings.Split(msg, "\n"); len(lines) > 1 {
			msg = strings.TrimSpace(lines[len(lines)-1])
		}
		msg = friendlyFFmpegError(msg)
		if strings.Contains(msg, "aac_adtstoasc") || strings.Contains(strings.ToLower(msg), "failed to inject") {
			a.runDownloadNoBSF(jobID, q, fileName, outPath, meta, ctx)
			return
		}
		a.failDownload(jobID, msg)
		return
	}

	if job, ok := a.store.GetDownload(jobID); ok && job.Status == "cancelled" {
		_ = os.Remove(outPath)
		return
	}

	a.store.UpdateDownload(jobID, func(j *DownloadJob) {
		j.Status = "done"
		j.Message = "Download complete"
		j.Progress = 100
		j.FilePath = outPath
		j.FileName = fileName
	})
}

func (a *App) runDownloadNoBSF(jobID string, q *Quality, fileName, outPath string, meta fileMeta, ctx context.Context) {
	if ctx.Err() != nil {
		a.store.UpdateDownload(jobID, func(j *DownloadJob) {
			j.Status = "cancelled"
			j.Message = "Cancelled"
		})
		return
	}
	a.store.UpdateDownload(jobID, func(j *DownloadJob) {
		if j.Status == "cancelled" {
			return
		}
		j.Message = "Retrying without audio filter…"
		j.Progress = 0
	})

	args := []string{
		"-hide_banner", "-loglevel", "error",
	}
	args = appendInputArgs(args, q.Headers, q.URL)
	args = append(args, "-c", "copy", "-movflags", "+faststart")
	args = appendOutputMetadata(args, meta.Title, meta.SourceURL)
	args = append(args, "-progress", "pipe:1", "-nostats", "-y", outPath)

	cmd := ffmpegCommand(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.failDownload(jobID, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.failDownload(jobID, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		a.failDownload(jobID, err.Error())
		return
	}

	var stderrBuf strings.Builder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrBuf, io.LimitReader(stderr, 64<<10))
	}()

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "out_time_ms=") {
			continue
		}
		a.store.UpdateDownload(jobID, func(j *DownloadJob) {
			if j.Status == "cancelled" {
				return
			}
			j.Message = "Downloading…"
			if j.Progress < 5 {
				j.Progress = 5
			}
		})
	}
	waitErr := cmd.Wait()
	wg.Wait()
	if ctx.Err() != nil {
		_ = os.Remove(outPath)
		a.store.UpdateDownload(jobID, func(j *DownloadJob) {
			j.Status = "cancelled"
			j.Message = "Cancelled"
		})
		return
	}
	if waitErr != nil {
		_ = os.Remove(outPath)
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		if lines := strings.Split(msg, "\n"); len(lines) > 1 {
			msg = strings.TrimSpace(lines[len(lines)-1])
		}
		a.failDownload(jobID, friendlyFFmpegError(msg))
		return
	}
	a.store.UpdateDownload(jobID, func(j *DownloadJob) {
		j.Status = "done"
		j.Message = "Download complete"
		j.Progress = 100
		j.FilePath = outPath
		j.FileName = fileName
	})
}

func (a *App) failDownload(jobID, msg string) {
	a.store.UpdateDownload(jobID, func(j *DownloadJob) {
		if j.Status == "cancelled" {
			return
		}
		j.Status = "error"
		j.Message = msg
	})
}

func probeDurationMS(mediaURL string, headers map[string]string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
	}
	args = appendInputArgs(args, headers, mediaURL)

	cmd := ffprobeCommand(ctx, args...)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil {
		return 0
	}
	return sec * 1_000_000 // ffmpeg out_time_ms is microseconds despite the name
}

func friendlyFFmpegError(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "argument list too long"):
		return "Request headers too large for ffmpeg (cookie jar). Re-probe and try again."
	case strings.Contains(lower, "name or service not known"),
		strings.Contains(lower, "nodename nor servname"),
		strings.Contains(lower, "temporary failure in name resolution"),
		strings.Contains(lower, "could not resolve host"):
		return "CDN host for segments/key could not be resolved (DNS). Check network/DNS and re-probe."
	case strings.Contains(lower, "unable to open resource"),
		strings.Contains(lower, "crypto"),
		strings.Contains(lower, "failed to read encryption key"):
		return "Stream is encrypted or the segment/key URL expired. Re-probe the page and try again."
	case strings.Contains(lower, "invalid data found"):
		return "ffmpeg could not read the stream (bad playlist, expired auth, or segment CDN unreachable). Re-probe and retry."
	case strings.Contains(lower, "403"), strings.Contains(lower, "401"):
		return "CDN rejected the request (auth/referer). Re-probe and download soon after."
	case strings.Contains(lower, "404"):
		return "Segment URL not found (expired playlist). Re-probe the page."
	default:
		return msg
	}
}
