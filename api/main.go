package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

//go:embed all:web
var embeddedWeb embed.FS

var webFS fs.FS

type Config struct {
	ListenAddr         string
	OutputDir          string
	DataDir            string
	OutputLabel        string // human path shown in UI, e.g. maxi1508/Downloads/videos
	FilebrowserURL     string // full folder URL for "open in Filebrowser" links
	LibreTranslateURL  string
	TranslateTo        string
	ProbeTimeout       time.Duration
	ChromePath      string
	MaxDownloads    int
}

func loadConfig() Config {
	timeout := 45 * time.Second
	if v := os.Getenv("PROBE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	maxDL := 10
	if v := os.Getenv("MAX_DOWNLOADS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxDL = n
		}
	}
	listen := envOr("LISTEN_ADDR", ":8091")
	return Config{
		ListenAddr:         listen,
		OutputDir:          envOr("OUTPUT_DIR", "/data/output"),
		DataDir:            envOr("DATA_DIR", "/data/viddown"),
		OutputLabel:        envOr("OUTPUT_LABEL", "Downloads/videos"),
		FilebrowserURL:    os.Getenv("FILEBROWSER_URL"),
		LibreTranslateURL: os.Getenv("LIBRETRANSLATE_URL"),
		TranslateTo:       envOr("TRANSLATE_TO", "en"),
		ProbeTimeout:   timeout,
		ChromePath:     os.Getenv("CHROME_PATH"),
		MaxDownloads:   maxDL,
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	sub, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		log.Fatal(err)
	}
	webFS = sub

	cfg := loadConfig()
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		log.Printf("warning: output dir: %v", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Printf("warning: data dir: %v", err)
	}

	app := &App{
		cfg:   cfg,
		store: NewStore(cfg.MaxDownloads),
		dlLog: NewDownloadLog(cfg.DataDir),
	}
	legacyLog := filepath.Join(cfg.OutputDir, ".viddown-downloads.json")
	if n, err := app.dlLog.migrateFromLegacy(legacyLog); err != nil {
		log.Printf("warning: migrate legacy download log: %v", err)
	} else if n > 0 {
		log.Printf("migrated %d download log entries from output dir", n)
	}
	if app.dlLog.isEmpty() {
		if n, err := app.dlLog.SeedFromOutputDir(cfg.OutputDir); err != nil {
			log.Printf("warning: seed download log: %v", err)
		} else if n > 0 {
			log.Printf("seeded download log with %d entries from existing files", n)
		}
	}
	log.Printf("viddown listening on %s (output %s, max downloads %d)", cfg.ListenAddr, cfg.OutputDir, cfg.MaxDownloads)
	if err := http.ListenAndServe(cfg.ListenAddr, app.routes()); err != nil {
		log.Fatal(err)
	}
}
