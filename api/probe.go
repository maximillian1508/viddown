package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"

type capturedStream struct {
	URL     string
	Headers map[string]string
}

func (a *App) startProbe(pageURL string) (string, error) {
	pageURL = strings.TrimSpace(pageURL)
	if pageURL == "" {
		return "", fmt.Errorf("url required")
	}

	id := uuid.NewString()
	job := &ProbeJob{
		ID:        id,
		Status:    "running",
		Message:   "Starting probe…",
		PageURL:   pageURL,
		NameSlug:  nameSlugFromURL(pageURL),
		CreatedAt: time.Now(),
	}
	a.store.PutProbe(job)

	go func() {
		select {
		case a.store.probeSem <- struct{}{}:
			defer func() { <-a.store.probeSem }()
		default:
			a.store.UpdateProbe(id, func(j *ProbeJob) {
				j.Status = "error"
				j.Message = "Another probe is already running"
			})
			return
		}

		videos, err := a.runProbe(pageURL, id)
		if err != nil {
			a.store.UpdateProbe(id, func(j *ProbeJob) {
				j.Status = "error"
				j.Message = err.Error()
			})
			return
		}
		if len(videos) == 0 {
			a.store.UpdateProbe(id, func(j *ProbeJob) {
				j.Status = "error"
				j.Message = "No HLS streams found"
			})
			return
		}
		a.store.UpdateProbe(id, func(j *ProbeJob) {
			j.Status = "ready"
			j.Message = fmt.Sprintf("Found %d video(s)", len(videos))
			j.Videos = videos
		})
	}()

	return id, nil
}

func (a *App) runProbe(pageURL, jobID string) ([]Video, error) {
	if strings.Contains(strings.ToLower(pageURL), ".m3u8") {
		a.store.UpdateProbe(jobID, func(j *ProbeJob) {
			j.Message = "Parsing direct playlist…"
		})
		return a.buildVideosFromCaptures([]capturedStream{{
			URL: pageURL,
			Headers: map[string]string{
				"User-Agent": defaultUA,
				"Referer":    pageURL,
			},
		}})
	}

	a.store.UpdateProbe(jobID, func(j *ProbeJob) {
		j.Message = "Launching browser…"
	})

	timeout := a.cfg.ProbeTimeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	userDataDir, err := os.MkdirTemp("/tmp", "chromium-user-*")
	if err != nil {
		return nil, fmt.Errorf("chrome profile dir: %w", err)
	}
	defer os.RemoveAll(userDataDir)
	crashDir := filepath.Join(userDataDir, "crash")
	_ = os.MkdirAll(crashDir, 0o755)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-crash-reporter", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-features", "Crashpad,TranslateUI"),
		chromedp.Flag("crash-dumps-dir", crashDir),
		chromedp.UserDataDir(userDataDir),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent(defaultUA),
		chromedp.Env("HOME=/home/viddown"),
		chromedp.Env("XDG_CONFIG_HOME=/home/viddown/.config"),
		chromedp.Env("XDG_CACHE_HOME=/home/viddown/.cache"),
	)
	if a.cfg.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(a.cfg.ChromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	var mu sync.Mutex
	captures := make(map[string]capturedStream)

	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			u := e.Request.URL
			if !isHLSURL(u) {
				return
			}
			headers := map[string]string{
				"User-Agent": defaultUA,
			}
			for k, v := range e.Request.Headers {
				ks := strings.ToLower(k)
				vs := fmt.Sprint(v)
				switch ks {
				case "referer", "origin", "user-agent", "cookie", "authorization":
					headers[http.CanonicalHeaderKey(k)] = vs
				}
			}
			if headers["Referer"] == "" {
				headers["Referer"] = pageURL
			}
			headers = sanitizeHeaders(headers)
			mu.Lock()
			if _, exists := captures[u]; !exists {
				captures[u] = capturedStream{URL: u, Headers: headers}
				a.store.UpdateProbe(jobID, func(j *ProbeJob) {
					j.Message = fmt.Sprintf("Found stream (%d)…", len(captures))
				})
			}
			mu.Unlock()
		}
	})

	err = chromedp.Run(browserCtx,
		network.Enable(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(`
				Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
			`).Do(ctx)
			return err
		}),
		chromedp.Navigate(pageURL),
	)
	if err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}

	a.store.UpdateProbe(jobID, func(j *ProbeJob) {
		j.Message = "Waiting for page…"
	})
	_ = chromedp.Run(browserCtx, chromedp.Sleep(2*time.Second))

	a.store.UpdateProbe(jobID, func(j *ProbeJob) {
		j.Message = "Triggering playback…"
	})
	_ = chromedp.Run(browserCtx, chromedp.Evaluate(`
		(() => {
			const playSelectors = [
				'button[aria-label*="play" i]', '.play-button', '.vjs-big-play-button',
				'.plyr__control--overlaid', '[class*="play-btn"]', '.jw-icon-playback',
				'[data-plyr="play"]', '.fp-play', 'video',
			];
			for (const sel of playSelectors) {
				document.querySelectorAll(sel).forEach(el => {
					try { el.click(); if (el.play) el.play(); } catch (e) {}
				});
			}
			document.querySelectorAll('video').forEach(v => { try { v.play(); } catch (e) {} });
			return true;
		})()
	`, nil))

	emptyWait := time.Now().Add(12 * time.Second)
	deadline := time.Now().Add(timeout - 10*time.Second)
	if deadline.Before(time.Now().Add(5 * time.Second)) {
		deadline = time.Now().Add(15 * time.Second)
	}
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(captures)
		mu.Unlock()
		if n > 0 {
			// give a short window for master + variants
			_ = chromedp.Run(browserCtx, chromedp.Sleep(3*time.Second))
			break
		}
		if time.Now().After(emptyWait) {
			break
		}
		_ = chromedp.Run(browserCtx, chromedp.Sleep(1*time.Second))
	}

	mu.Lock()
	list := make([]capturedStream, 0, len(captures))
	for _, c := range captures {
		list = append(list, c)
	}
	mu.Unlock()

	a.store.UpdateProbe(jobID, func(j *ProbeJob) {
		j.Message = "Parsing playlists…"
	})
	return a.buildVideosFromCaptures(list)
}

func (a *App) buildVideosFromCaptures(captures []capturedStream) ([]Video, error) {
	type masterHit struct {
		URL      string
		Headers  map[string]string
		Variants []streamVariant
	}

	var masters []masterHit
	mediaOnly := make([]capturedStream, 0)
	seenMedia := make(map[string]bool)

	for _, c := range captures {
		body, err := fetchPlaylist(c.URL, c.Headers)
		if err != nil {
			mediaOnly = append(mediaOnly, c)
			continue
		}
		variants, isMaster := parseMasterPlaylist(body, c.URL)
		if isMaster {
			masters = append(masters, masterHit{URL: c.URL, Headers: c.Headers, Variants: variants})
			for _, v := range variants {
				seenMedia[v.URL] = true
			}
		} else {
			mediaOnly = append(mediaOnly, c)
		}
	}

	var videos []Video
	vidIdx := 0

	for _, m := range masters {
		vidIdx++
		v := Video{
			ID:        fmt.Sprintf("v%d", vidIdx),
			Label:     labelFromURL(m.URL),
			MasterURL: m.URL,
		}
		for i, variant := range m.Variants {
			headers := cloneHeaders(m.Headers)
			v.Qualities = append(v.Qualities, Quality{
				ID:         fmt.Sprintf("q%d", i+1),
				Name:       variant.Name,
				Resolution: variant.Resolution,
				Bandwidth:  variant.Bandwidth,
				URL:        variant.URL,
				Headers:    headers,
			})
		}
		if len(v.Qualities) == 0 {
			v.Qualities = append(v.Qualities, Quality{
				ID:      "q1",
				URL:     m.URL,
				Headers: cloneHeaders(m.Headers),
			})
		}
		v.Qualities = finalizeQualities(v.Qualities)
		videos = append(videos, v)
	}

	orphans := make([]capturedStream, 0)
	for _, c := range mediaOnly {
		if seenMedia[c.URL] {
			continue
		}
		// skip if this URL is itself a master we already handled
		skip := false
		for _, m := range masters {
			if m.URL == c.URL {
				skip = true
				break
			}
		}
		if !skip {
			orphans = append(orphans, c)
		}
	}

	// Each orphan media playlist is its own video (not quality variants).
	// Sites often expose many discrete .m3u8s (main + ads); bundling them as
	// "9 streams" under one quality dropdown made multiselect useless.
	for i, c := range orphans {
		vidIdx++
		label := labelFromURL(c.URL)
		if len(orphans) > 1 {
			label = fmt.Sprintf("Stream %d · %s", i+1, label)
		}
		v := Video{
			ID:    fmt.Sprintf("v%d", vidIdx),
			Label: label,
			Qualities: []Quality{{
				ID:      "q1",
				URL:     c.URL,
				Headers: cloneHeaders(c.Headers),
			}},
		}
		v.Qualities = finalizeQualities(v.Qualities)
		videos = append(videos, v)
	}

	return videos, nil
}

func cloneHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}
