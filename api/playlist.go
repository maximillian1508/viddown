package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type streamVariant struct {
	URL        string
	Name       string
	Bandwidth  int
	Resolution string
}

func isHLSURL(raw string) bool {
	lower := strings.ToLower(raw)
	excludes := []string{
		"google-analytics", "googletagmanager", "facebook.com", "doubleclick",
		".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".woff",
	}
	for _, ex := range excludes {
		if strings.Contains(lower, ex) {
			return false
		}
	}
	return strings.Contains(lower, ".m3u8") ||
		strings.Contains(lower, "/hls/") ||
		strings.Contains(lower, "m3u8")
}

func fetchPlaylist(rawURL string, headers map[string]string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUA)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("playlist HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parseMasterPlaylist(body, baseURL string) ([]streamVariant, bool) {
	if !strings.Contains(body, "#EXT-X-STREAM-INF") {
		return nil, false
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, false
	}

	var variants []streamVariant
	sc := bufio.NewScanner(strings.NewReader(body))
	var pending *streamVariant

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			attrs := line[len("#EXT-X-STREAM-INF:"):]
			pending = &streamVariant{}
			for _, part := range splitAttrs(attrs) {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) != 2 {
					continue
				}
				key := strings.ToUpper(strings.TrimSpace(kv[0]))
				val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
				switch key {
				case "BANDWIDTH", "AVERAGE-BANDWIDTH":
					if n, err := strconv.Atoi(val); err == nil && (pending.Bandwidth == 0 || key == "BANDWIDTH") {
						pending.Bandwidth = n
					}
				case "RESOLUTION":
					pending.Resolution = val
				case "NAME":
					pending.Name = val
				}
			}
			continue
		}
		if pending != nil && line != "" && !strings.HasPrefix(line, "#") {
			ref, err := url.Parse(line)
			if err == nil {
				pending.URL = base.ResolveReference(ref).String()
				variants = append(variants, *pending)
			}
			pending = nil
		}
	}
	return variants, len(variants) > 0
}

func splitAttrs(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case r == ',' && !inQuotes:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func labelFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "Video"
	}
	host := u.Hostname()
	pathStr := strings.Trim(u.Path, "/")
	if pathStr == "" {
		return host
	}
	segs := strings.Split(pathStr, "/")
	name := segs[len(segs)-1]
	if looksLikeHashName(name) && len(segs) > 1 {
		name = segs[len(segs)-2]
	}
	if strings.Contains(strings.ToLower(name), "m3u8") && len(segs) > 1 {
		name = segs[len(segs)-2]
	}
	if len(name) > 40 {
		name = name[:40] + "…"
	}
	return fmt.Sprintf("%s · %s", host, name)
}

func looksLikeHashName(name string) bool {
	base := strings.TrimSuffix(name, path.Ext(name))
	if base == "" {
		return true
	}
	// long hex / base64-ish tokens
	if len(base) >= 20 && regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`).MatchString(base) {
		hexish := regexp.MustCompile(`^[0-9a-fA-F]+$`).MatchString(base)
		if hexish || strings.Contains(base, "==") || len(base) >= 32 {
			return true
		}
	}
	return false
}

func qualityLabel(q Quality, index, total int) string {
	var parts []string
	if q.Name != "" && !looksLikeHashName(q.Name) {
		parts = append(parts, q.Name)
	}
	if q.Resolution != "" {
		parts = append(parts, prettyResolution(q.Resolution))
	}
	if q.Bandwidth > 0 {
		if q.Bandwidth >= 1_000_000 {
			parts = append(parts, fmt.Sprintf("%.1f Mbps", float64(q.Bandwidth)/1_000_000))
		} else {
			parts = append(parts, fmt.Sprintf("%d kbps", q.Bandwidth/1000))
		}
	}
	if q.Duration != "" {
		parts = append(parts, q.Duration)
	}
	if len(parts) == 0 {
		if total > 1 {
			return fmt.Sprintf("Stream %d", index+1)
		}
		return "Default"
	}
	return strings.Join(parts, " · ")
}

func prettyResolution(res string) string {
	parts := strings.Split(res, "x")
	if len(parts) != 2 {
		return res
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return res
	}
	switch {
	case h >= 2160:
		return "2160p"
	case h >= 1440:
		return "1440p"
	case h >= 1080:
		return "1080p"
	case h >= 720:
		return "720p"
	case h >= 480:
		return "480p"
	case h >= 360:
		return "360p"
	case h >= 240:
		return "240p"
	default:
		return res
	}
}

func enrichQuality(q *Quality) {
	if q.Resolution != "" && q.Duration != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	args := []string{"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams"}
	args = appendInputArgs(args, q.Headers, q.URL)

	cmd := ffprobeCommand(ctx, args...)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return
	}
	if q.Resolution == "" {
		for _, s := range result.Streams {
			if s.CodecType == "video" && s.Width > 0 && s.Height > 0 {
				q.Resolution = fmt.Sprintf("%dx%d", s.Width, s.Height)
				break
			}
		}
	}
	if q.Duration == "" && result.Format.Duration != "" {
		if sec, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil && sec > 0 {
			q.Duration = formatDuration(sec)
		}
	}
}

func formatDuration(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func sortQualities(qs []Quality) {
	sort.SliceStable(qs, func(i, j int) bool {
		if qs[i].Bandwidth != qs[j].Bandwidth {
			return qs[i].Bandwidth > qs[j].Bandwidth
		}
		hi := heightOf(qs[i].Resolution)
		hj := heightOf(qs[j].Resolution)
		return hi > hj
	})
}

func heightOf(res string) int {
	parts := strings.Split(res, "x")
	if len(parts) != 2 {
		return 0
	}
	h, _ := strconv.Atoi(parts[1])
	return h
}

func finalizeQualities(qs []Quality) []Quality {
	for i := range qs {
		enrichQuality(&qs[i])
	}
	sortQualities(qs)
	for i := range qs {
		qs[i].ID = fmt.Sprintf("q%d", i+1)
		qs[i].Label = qualityLabel(qs[i], i, len(qs))
	}
	return qs
}

// nameSlugFromURL builds a filesystem-safe stem from page or stream URL.
// Example: https://site.com/watch/abc-xyz → site-com_watch-abc-xyz
func nameSlugFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "video"
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	host = strings.ReplaceAll(host, ".", "-")

	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	var useful []string
	for _, s := range segs {
		if s == "" || s == "index.html" {
			continue
		}
		lower := strings.ToLower(s)
		if strings.HasSuffix(lower, ".m3u8") || strings.HasSuffix(lower, ".mpd") {
			continue
		}
		if looksLikeHashName(s) {
			continue
		}
		useful = append(useful, s)
	}
	pathPart := ""
	if len(useful) > 0 {
		// keep last 2 meaningful segments
		start := 0
		if len(useful) > 2 {
			start = len(useful) - 2
		}
		pathPart = strings.Join(useful[start:], "-")
	}

	slug := host
	if pathPart != "" {
		slug = host + "_" + pathPart
	}
	return sanitizeSlug(slug)
}

func sanitizeSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "video"
	}
	if len(out) > 80 {
		out = out[:80]
		out = strings.Trim(out, "-")
	}
	return out
}

func buildDownloadFileName(slug, resolution, videoID, uniq string) string {
	ts := time.Now().Format("20060102_150405")
	parts := []string{slug}
	if resolution != "" {
		parts = append(parts, prettyResolution(resolution))
	}
	if videoID != "" {
		parts = append(parts, videoID)
	}
	parts = append(parts, ts)
	if uniq != "" {
		parts = append(parts, uniq)
	}
	return strings.Join(parts, "_") + ".mp4"
}
