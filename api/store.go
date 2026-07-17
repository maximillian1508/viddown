package main

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type Quality struct {
	ID             string            `json:"id"`
	Label          string            `json:"label"`
	Name           string            `json:"name,omitempty"`
	Resolution     string            `json:"resolution,omitempty"`
	Bandwidth      int               `json:"bandwidth,omitempty"`
	Duration       string            `json:"duration,omitempty"`
	EstimatedBytes int64             `json:"estimatedBytes,omitempty"`
	OnDisk         bool              `json:"onDisk,omitempty"`
	OnDiskFile     string            `json:"onDiskFile,omitempty"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"-"`
}

type Video struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Duration  string    `json:"duration,omitempty"`
	LikelyAd  bool      `json:"likelyAd,omitempty"`
	MasterURL string    `json:"masterUrl,omitempty"`
	Qualities []Quality `json:"qualities"`
}

type ProbeJob struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	PageURL   string    `json:"pageUrl,omitempty"`
	PageTitle string    `json:"pageTitle,omitempty"`
	NameSlug  string    `json:"nameSlug,omitempty"`
	Videos    []Video   `json:"videos,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type DownloadJob struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Label     string    `json:"label,omitempty"`
	Progress  float64   `json:"progress"`
	FilePath  string    `json:"filePath,omitempty"`
	FileName  string    `json:"fileName,omitempty"`
	OpenURL   string    `json:"openUrl,omitempty"`
	ProbeID   string    `json:"probeId,omitempty"`
	VideoID   string    `json:"videoId,omitempty"`
	QualityID string    `json:"qualityId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	cancel    context.CancelFunc
}

type Store struct {
	mu        sync.RWMutex
	db        *Database
	probes    map[string]*ProbeJob
	downloads map[string]*DownloadJob
	probeSem  chan struct{}
	dlSem     chan struct{}
	maxDL     int
}

func NewStore(maxDownloads int, db *Database) *Store {
	if maxDownloads < 1 {
		maxDownloads = 10
	}
	return &Store{
		db:        db,
		probes:    make(map[string]*ProbeJob),
		downloads: make(map[string]*DownloadJob),
		probeSem:  make(chan struct{}, 1),
		dlSem:     make(chan struct{}, maxDownloads),
		maxDL:     maxDownloads,
	}
}

func (s *Store) LoadFromDB() error {
	if s.db == nil {
		return nil
	}
	probes, err := s.db.ListRecentProbes(50)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for i := range probes {
		p := probes[i]
		s.probes[p.ID] = &p
	}
	s.mu.Unlock()

	jobs, err := s.db.ListDownloadJobs(500)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for i := range jobs {
		j := jobs[i]
		s.downloads[j.ID] = &j
	}
	s.mu.Unlock()
	return nil
}

func (s *Store) MaxDownloads() int {
	return s.maxDL
}

func (s *Store) ActiveDownloadCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, j := range s.downloads {
		if j.Status == "queued" || j.Status == "running" {
			n++
		}
	}
	return n
}

func (s *Store) PutProbe(j *ProbeJob, persist bool) {
	s.mu.Lock()
	s.probes[j.ID] = j
	s.mu.Unlock()
	if persist && s.db != nil {
		_ = s.db.SaveProbe(j)
	}
}

func (s *Store) GetProbe(id string) (*ProbeJob, bool) {
	s.mu.RLock()
	j, ok := s.probes[id]
	s.mu.RUnlock()
	if !ok {
		if s.db != nil {
			loaded, err := s.db.GetProbe(id)
			if err != nil || loaded == nil {
				return nil, false
			}
			s.PutProbe(loaded, false)
			return loaded, true
		}
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
	j, ok := s.probes[id]
	if ok {
		fn(j)
	}
	s.mu.Unlock()
	if ok && s.db != nil {
		s.mu.RLock()
		cp := s.probes[id]
		s.mu.RUnlock()
		if cp != nil {
			_ = s.db.SaveProbe(cp)
		}
	}
}

func (s *Store) FindQuality(probeID, videoID, qualityID string) (*Quality, string, bool) {
	s.mu.RLock()
	j, ok := s.probes[probeID]
	s.mu.RUnlock()
	if !ok && s.db != nil {
		loaded, err := s.db.GetProbe(probeID)
		if err != nil || loaded == nil {
			return nil, "", false
		}
		s.PutProbe(loaded, false)
		j = loaded
		ok = true
	}
	if !ok {
		return nil, "", false
	}
	for _, v := range j.Videos {
		if v.ID != videoID {
			continue
		}
		for i := range v.Qualities {
			if v.Qualities[i].ID == qualityID {
				q := v.Qualities[i]
				return &q, v.Label, true
			}
		}
	}
	return nil, "", false
}

func (s *Store) PutDownload(j *DownloadJob) {
	s.mu.Lock()
	s.downloads[j.ID] = j
	s.mu.Unlock()
	if s.db != nil {
		_ = s.db.SaveDownloadJob(j)
	}
}

func (s *Store) GetDownload(id string) (*DownloadJob, bool) {
	s.mu.RLock()
	j, ok := s.downloads[id]
	s.mu.RUnlock()
	if !ok && s.db != nil {
		loaded, err := s.db.GetDownloadJob(id)
		if err != nil || loaded == nil {
			return nil, false
		}
		s.mu.Lock()
		s.downloads[id] = loaded
		s.mu.Unlock()
		return loaded, true
	}
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

func publicDownload(j *DownloadJob, outputLabel, filebrowserURL string) DownloadJob {
	cp := *j
	if cp.FileName != "" {
		label := strings.Trim(outputLabel, "/")
		if label == "" {
			label = "Downloads/videos"
		}
		cp.FilePath = label + "/" + cp.FileName
		if filebrowserURL != "" {
			cp.OpenURL = strings.TrimRight(filebrowserURL, "/") + "/" + cp.FileName
		}
	}
	return cp
}

func (s *Store) ListDownloads(outputLabel, filebrowserURL string) []DownloadJob {
	if s.db != nil {
		jobs, err := s.db.ListDownloadJobs(500)
		if err == nil {
			out := make([]DownloadJob, 0, len(jobs))
			for i := range jobs {
				out = append(out, publicDownload(&jobs[i], outputLabel, filebrowserURL))
			}
			return out
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DownloadJob, 0, len(s.downloads))
	for _, j := range s.downloads {
		out = append(out, publicDownload(j, outputLabel, filebrowserURL))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *Store) UpdateDownload(id string, fn func(*DownloadJob)) {
	s.mu.Lock()
	j, ok := s.downloads[id]
	if ok {
		fn(j)
	}
	s.mu.Unlock()
	if ok && s.db != nil {
		s.mu.RLock()
		cp := s.downloads[id]
		s.mu.RUnlock()
		if cp != nil {
			_ = s.db.SaveDownloadJob(cp)
		}
	}
}

func (s *Store) SetDownloadCancel(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.downloads[id]; ok {
		j.cancel = cancel
	}
}

func (s *Store) CancelDownload(id string) bool {
	s.mu.Lock()
	j, ok := s.downloads[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	switch j.Status {
	case "queued", "running":
		if j.cancel != nil {
			j.cancel()
		}
		j.Status = "cancelled"
		j.Message = "Cancelled"
		s.mu.Unlock()
		if s.db != nil {
			_ = s.db.SaveDownloadJob(j)
		}
		return true
	default:
		s.mu.Unlock()
		return false
	}
}

func (s *Store) RemoveDownload(id string) {
	s.mu.Lock()
	delete(s.downloads, id)
	s.mu.Unlock()
	if s.db != nil {
		_ = s.db.DeleteDownloadJob(id)
	}
}
