package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

//go:embed all:web
var embeddedWeb embed.FS

var webFS fs.FS

type Config struct {
	ListenAddr   string
	OutputDir    string
	OutputLabel  string // human path shown in UI, e.g. maxi1508/Downloads/videos
	ProbeTimeout time.Duration
	ChromePath   string
	MaxDownloads int
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
		ListenAddr:   listen,
		OutputDir:    envOr("OUTPUT_DIR", "/data/output"),
		OutputLabel:  envOr("OUTPUT_LABEL", "Downloads/videos"),
		ProbeTimeout: timeout,
		ChromePath:   os.Getenv("CHROME_PATH"),
		MaxDownloads: maxDL,
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

	app := &App{cfg: cfg, store: NewStore(cfg.MaxDownloads)}
	log.Printf("viddown listening on %s (output %s, max downloads %d)", cfg.ListenAddr, cfg.OutputDir, cfg.MaxDownloads)
	if err := http.ListenAndServe(cfg.ListenAddr, app.routes()); err != nil {
		log.Fatal(err)
	}
}
