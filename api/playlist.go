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
	if q.Bandwidth >= minBandwidthForEstimate {
		if q.Bandwidth >= 1_000_000 {
			parts = append(parts, fmt.Sprintf("%.1f Mbps", float64(q.Bandwidth)/1_000_000))
		} else {
			parts = append(parts, fmt.Sprintf("%d kbps", (q.Bandwidth+500)/1000))
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

// minBandwidthForEstimate rejects ffprobe bit_rate on .m3u8 manifests (often ~hundreds of bps).
const minBandwidthForEstimate = 50_000

func enrichQuality(q *Quality) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	needProbe := q.Resolution == "" || q.Duration == "" || q.Bandwidth < minBandwidthForEstimate
	if needProbe {
		args := []string{"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams"}
		args = appendInputArgs(args, q.Headers, q.URL)

		cmd := ffprobeCommand(ctx, args...)
		out, err := cmd.Output()
		if err == nil {
			var result struct {
				Format struct {
					Duration string `json:"duration"`
					BitRate  string `json:"bit_rate"`
				} `json:"format"`
				Streams []struct {
					CodecType string `json:"codec_type"`
					Width     int    `json:"width"`
					Height    int    `json:"height"`
					BitRate   string `json:"bit_rate"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(out, &result); err == nil {
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
				if q.Bandwidth < minBandwidthForEstimate {
					q.Bandwidth = maxProbeBitrate(result.Format.BitRate, result.Streams)
				}
			}
		}
	}
	if q.Bandwidth < minBandwidthForEstimate {
		if seg := firstMediaSegmentURL(q.URL, q.Headers); seg != "" {
			if br := probeStreamBitrate(ctx, q.Headers, seg); br >= minBandwidthForEstimate {
				q.Bandwidth = br
			}
		}
	}
	applyQualityEstimate(q)
}

func maxProbeBitrate(formatBR string, streams []struct {
	CodecType string `json:"codec_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	BitRate   string `json:"bit_rate"`
}) int {
	best := parsePositiveInt(formatBR)
	for _, s := range streams {
		if br := parsePositiveInt(s.BitRate); br > best {
			best = br
		}
	}
	return best
}

func parsePositiveInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func firstMediaSegmentURL(playlistURL string, headers map[string]string) string {
	body, err := fetchPlaylist(playlistURL, headers)
	if err != nil {
		return ""
	}
	base, err := url.Parse(playlistURL)
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ref, err := url.Parse(line)
		if err != nil {
			return ""
		}
		return base.ResolveReference(ref).String()
	}
	return ""
}

func probeStreamBitrate(ctx context.Context, headers map[string]string, streamURL string) int {
	args := []string{"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams"}
	args = appendInputArgs(args, headers, streamURL)
	out, err := ffprobeCommand(ctx, args...).Output()
	if err != nil {
		return 0
	}
	var result struct {
		Format struct {
			BitRate string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			BitRate   string `json:"bit_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0
	}
	best := parsePositiveInt(result.Format.BitRate)
	for _, s := range result.Streams {
		if br := parsePositiveInt(s.BitRate); br > best {
			best = br
		}
	}
	return best
}

func parseDurationLabel(label string) float64 {
	parts := strings.Split(label, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	vals := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		vals[i] = n
	}
	if len(vals) == 2 {
		return float64(vals[0]*60 + vals[1])
	}
	return float64(vals[0]*3600 + vals[1]*60 + vals[2])
}

func applyQualityEstimate(q *Quality) {
	if q.Duration == "" || q.Bandwidth < minBandwidthForEstimate {
		return
	}
	sec := parseDurationLabel(q.Duration)
	if sec <= 0 {
		return
	}
	// HLS BANDWIDTH / ffprobe bit_rate are bits/s; ~5% overhead for container.
	q.EstimatedBytes = int64(float64(q.Bandwidth) * sec / 8 * 1.05)
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
	parts := []string{slug}
	if resolution != "" {
		parts = append(parts, prettyResolution(resolution))
	}
	if videoID != "" {
		parts = append(parts, videoID)
	}
	if uniq != "" {
		parts = append(parts, uniq)
	}
	return strings.Join(parts, "_") + ".mp4"
}
