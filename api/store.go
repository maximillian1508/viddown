package main

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type Quality struct {
	ID         string            `json:"id"`
	Label      string            `json:"label"`
	Name       string            `json:"name,omitempty"`
	Resolution string            `json:"resolution,omitempty"`
	Bandwidth  int               `json:"bandwidth,omitempty"`
	Duration   string            `json:"duration,omitempty"`
	URL        string            `json:"url"`
	Headers    map[string]string `json:"-"`
}

type Video struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	MasterURL string    `json:"masterUrl,omitempty"`
	Qualities []Quality `json:"qualities"`
}

type ProbeJob struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	Message    string    `json:"message,omitempty"`
	PageURL    string    `json:"pageUrl,omitempty"`
	NameSlug   string    `json:"nameSlug,omitempty"`
	Videos     []Video   `json:"videos,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type DownloadJob struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Label     string    `json:"label,omitempty"`
	Progress  float64   `json:"progress"`
	FilePath  string    `json:"filePath,omitempty"`
	FileName  string    `json:"fileName,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	cancel    context.CancelFunc
}

type Store struct {
	mu       sync.RWMutex
	probes   map[string]*ProbeJob
	downloads map[string]*DownloadJob
	probeSem chan struct{}
	dlSem    chan struct{}
}

func NewStore() *Store {
	return &Store{
		probes:    make(map[string]*ProbeJob),
		downloads: make(map[string]*DownloadJob),
		probeSem:  make(chan struct{}, 1),
		dlSem:     make(chan struct{}, 1),
	}
}

func (s *Store) PutProbe(j *ProbeJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes[j.ID] = j
	s.expireLocked()
}

func (s *Store) GetProbe(id string) (*ProbeJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.probes[id]
	if !ok {
		return nil, false
	}
	cp := *j
	cp.Videos = append([]Video(nil), j.Videos...)
	for i := range cp.Videos {
		cp.Videos[i].Qualities = append([]Quality(nil), j.Videos[i].Qualities...)
	}
	return &cp, true
}

func (s *Store) UpdateProbe(id string, fn func(*ProbeJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.probes[id]; ok {
		fn(j)
	}
}

func (s *Store) FindQuality(probeID, videoID, qualityID string) (*Quality, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.probes[probeID]
	if !ok {
		return nil, false
	}
	for _, v := range j.Videos {
		if v.ID != videoID {
			continue
		}
		for i := range v.Qualities {
			if v.Qualities[i].ID == qualityID {
				q := v.Qualities[i]
				return &q, true
			}
		}
	}
	return nil, false
}

func (s *Store) PutDownload(j *DownloadJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downloads[j.ID] = j
	s.expireLocked()
}

func (s *Store) GetDownload(id string) (*DownloadJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.downloads[id]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

func publicDownload(j *DownloadJob, outputLabel string) DownloadJob {
	cp := *j
	if cp.FileName != "" {
		label := strings.Trim(outputLabel, "/")
		if label == "" {
			label = "Downloads/videos"
		}
		cp.FilePath = label + "/" + cp.FileName
	}
	return cp
}

// ListDownloads returns active jobs plus recent finished ones (newest first).
func (s *Store) ListDownloads(outputLabel string) []DownloadJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DownloadJob, 0, len(s.downloads))
	for _, j := range s.downloads {
		out = append(out, publicDownload(j, outputLabel))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *Store) UpdateDownload(id string, fn func(*DownloadJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.downloads[id]; ok {
		fn(j)
	}
}

func (s *Store) SetDownloadCancel(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.downloads[id]; ok {
		j.cancel = cancel
	}
}

// CancelDownload stops a queued/running download. Returns false if missing or already finished.
func (s *Store) CancelDownload(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.downloads[id]
	if !ok {
		return false
	}
	switch j.Status {
	case "queued", "running":
		if j.cancel != nil {
			j.cancel()
		}
		j.Status = "cancelled"
		j.Message = "Cancelled"
		return true
	default:
		return false
	}
}

func (s *Store) expireLocked() {
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, j := range s.probes {
		if j.CreatedAt.Before(cutoff) {
			delete(s.probes, id)
		}
	}
	for id, j := range s.downloads {
		if j.CreatedAt.Before(cutoff) && (j.Status == "done" || j.Status == "error" || j.Status == "cancelled") {
			delete(s.downloads, id)
		}
	}
}
