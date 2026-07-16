package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var (
	reURLInTitle  = regexp.MustCompile(`(?i)https?://\S+|www\.\S+`)
	reDateInTitle = regexp.MustCompile(`\b20\d{2}[-/.年]\d{1,2}[-/.月]\d{1,2}日?\b|\b20\d{6}\b`)
)

func cleanPageTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	title = reURLInTitle.ReplaceAllString(title, "")
	title = reDateInTitle.ReplaceAllString(title, "")
	title = strings.Join(strings.Fields(title), " ")
	// Strip common site suffixes: "Episode 3 - mrds66", "Title | Site"
	for _, sep := range []string{" | ", " - ", " – ", " — ", " _ "} {
		if i := strings.LastIndex(title, sep); i > 0 {
			suffix := strings.ToLower(strings.TrimSpace(title[i+len(sep):]))
			if len(suffix) < 40 && (strings.Contains(suffix, ".") || len(strings.Fields(suffix)) <= 3) {
				title = strings.TrimSpace(title[:i])
			}
		}
	}
	return strings.TrimSpace(title)
}

func needsTranslation(s string) bool {
	letters := 0
	nonLatin := 0
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if r > unicode.MaxASCII {
			nonLatin++
		}
	}
	if letters == 0 {
		return false
	}
	return nonLatin*2 >= letters // mostly non-ASCII
}

func (a *App) translateText(text, source, target string) (string, error) {
	base := strings.TrimRight(a.cfg.LibreTranslateURL, "/")
	if base == "" {
		return "", fmt.Errorf("translation disabled")
	}
	body, _ := json.Marshal(map[string]string{
		"q":      text,
		"source": source,
		"target": target,
		"format": "text",
	})
	req, err := http.NewRequest(http.MethodPost, base+"/translate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("translate http %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		TranslatedText string `json:"translatedText"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	out.TranslatedText = strings.TrimSpace(out.TranslatedText)
	if out.TranslatedText == "" {
		return "", fmt.Errorf("empty translation")
	}
	return out.TranslatedText, nil
}

func (a *App) nameSlugForPage(pageURL, pageTitle string) (slug, displayTitle string) {
	displayTitle = cleanPageTitle(pageTitle)
	if displayTitle == "" {
		return nameSlugFromURL(pageURL), ""
	}

	label := displayTitle
	if needsTranslation(displayTitle) && a.cfg.LibreTranslateURL != "" {
		target := a.cfg.TranslateTo
		if target == "" {
			target = "en"
		}
		if en, err := a.translateText(displayTitle, "zh", target); err == nil {
			label = en
		} else if en, err := a.translateText(displayTitle, "auto", target); err == nil {
			label = en
		}
	}

	titleSlug := sanitizeSlug(label)
	if titleSlug != "" && titleSlug != "video" {
		return titleSlug, displayTitle
	}
	return nameSlugFromURL(pageURL), displayTitle
}
