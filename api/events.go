package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type eventHub struct {
	mu              sync.RWMutex
	clients         map[chan ssePayload]struct{}
	outputLabel     string
	filebrowserURL  string
}

type ssePayload struct {
	event string
	data  []byte
}

func newEventHub(outputLabel, filebrowserURL string) *eventHub {
	return &eventHub{
		clients:        make(map[chan ssePayload]struct{}),
		outputLabel:    outputLabel,
		filebrowserURL: filebrowserURL,
	}
}

func (h *eventHub) subscribe() chan ssePayload {
	ch := make(chan ssePayload, 16)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) unsubscribe(ch chan ssePayload) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *eventHub) publish(event string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	payload := ssePayload{event: event, data: data}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- payload:
		default:
			// slow client: drop rather than block producers
		}
	}
}

func (h *eventHub) publishProbe(job *ProbeJob) {
	if job == nil {
		return
	}
	cp := *job
	cp.Videos = cloneVideosForAnnotate(job.Videos)
	h.publish("probe", &cp)
}

func (h *eventHub) publishDownload(job *DownloadJob) {
	if job == nil {
		return
	}
	pub := publicDownload(job, h.outputLabel, h.filebrowserURL)
	h.publish("download", pub)
	if isTerminalDownloadStatus(pub.Status) {
		h.publish("history", map[string]string{"reason": pub.Status})
	}
}

func isTerminalDownloadStatus(status string) bool {
	switch status {
	case "done", "error", "cancelled":
		return true
	default:
		return false
	}
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	if a.events == nil {
		http.Error(w, "events unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := a.events.subscribe()
	defer a.events.unsubscribe(ch)

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	fmt.Fprintf(w, "retry: 10000\n")
	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case payload := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", payload.event, payload.data)
			flusher.Flush()
		}
	}
}
