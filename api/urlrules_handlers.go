package main

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

func (a *App) handleURLRulesGet(w http.ResponseWriter, r *http.Request) {
	if a.db == nil {
		writeJSON(w, http.StatusOK, URLRulesConfig{Rules: []URLRule{}})
		return
	}
	cfg, err := a.db.LoadURLRules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleURLRulesPut(w http.ResponseWriter, r *http.Request) {
	if a.db == nil {
		writeErr(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	var cfg URLRulesConfig
	if err := jsonDecode(r, &cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	for i := range cfg.Rules {
		if cfg.Rules[i].ID == "" {
			cfg.Rules[i].ID = uuid.NewString()
		}
	}
	if err := a.db.SaveURLRules(cfg); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (a *App) handleURLRulesTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL   string    `json:"url"`
		Rules []URLRule `json:"rules,omitempty"`
	}
	if err := jsonDecode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	cfg := URLRulesConfig{Rules: body.Rules}
	if len(body.Rules) == 0 && a.db != nil {
		loaded, err := a.db.LoadURLRules()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		cfg = loaded
	}
	res, err := ApplyURLRules(body.URL, cfg)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func jsonDecode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
