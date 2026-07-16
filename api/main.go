package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
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
}

func loadConfig() Config {
	timeout := 45 * time.Second
	if v := os.Getenv("PROBE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	listen := envOr("LISTEN_ADDR", ":8091")
	return Config{
		ListenAddr:   listen,
		OutputDir:    envOr("OUTPUT_DIR", "/data/output"),
		OutputLabel:  envOr("OUTPUT_LABEL", "Downloads/videos"),
		ProbeTimeout: timeout,
		ChromePath:   os.Getenv("CHROME_PATH"),
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

	app := &App{cfg: cfg, store: NewStore()}
	log.Printf("viddown listening on %s (output %s)", cfg.ListenAddr, cfg.OutputDir)
	if err := http.ListenAndServe(cfg.ListenAddr, app.routes()); err != nil {
		log.Fatal(err)
	}
}
