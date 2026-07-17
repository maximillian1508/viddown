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
	ListenAddr        string
	OutputDir         string
	DataDir           string
	OutputLabel       string
	FilebrowserURL    string
	LibreTranslateURL string
	TranslateTo       string
	ProbeTimeout      time.Duration
	ChromePath        string
	MaxDownloads      int
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
		ListenAddr:        listen,
		OutputDir:         envOr("OUTPUT_DIR", "/data/output"),
		DataDir:           envOr("DATA_DIR", "/data/viddown"),
		OutputLabel:       envOr("OUTPUT_LABEL", "Downloads/videos"),
		FilebrowserURL:    os.Getenv("FILEBROWSER_URL"),
		LibreTranslateURL: os.Getenv("LIBRETRANSLATE_URL"),
		TranslateTo:       envOr("TRANSLATE_TO", "en"),
		ProbeTimeout:      timeout,
		ChromePath:        os.Getenv("CHROME_PATH"),
		MaxDownloads:      maxDL,
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

	db, err := OpenDatabase(cfg.DataDir)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	if err := db.ImportJSONIfEmpty(cfg.DataDir, cfg.OutputDir); err != nil {
		log.Printf("warning: import legacy json: %v", err)
	}
	if n, err := db.MarkInterruptedJobs(); err != nil {
		log.Printf("warning: mark interrupted jobs: %v", err)
	} else if n > 0 {
		log.Printf("marked %d download job(s) interrupted after restart", n)
	}
	if err := db.PruneOldProbes(50); err != nil {
		log.Printf("warning: prune probes: %v", err)
	}

	store := NewStore(cfg.MaxDownloads, db, nil)
	if err := store.LoadFromDB(); err != nil {
		log.Printf("warning: load store from db: %v", err)
	}

	events := newEventHub(cfg.OutputLabel, cfg.FilebrowserURL)
	store.events = events

	app := &App{
		cfg:    cfg,
		store:  store,
		db:     db,
		events: events,
	}

	log.Printf("viddown listening on %s (output %s, data %s, max downloads %d)",
		cfg.ListenAddr, cfg.OutputDir, cfg.DataDir, cfg.MaxDownloads)
	if err := http.ListenAndServe(cfg.ListenAddr, app.routes()); err != nil {
		log.Fatal(err)
	}
}
