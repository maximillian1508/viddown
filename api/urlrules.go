package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const urlRulesFile = "url-rules.json"

type URLRule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Match   string `json:"match"`
	Replace string `json:"replace"`
}

type URLRulesConfig struct {
	Rules []URLRule `json:"rules"`
}

type URLRulesStore struct {
	path string
	mu   sync.RWMutex
}

func NewURLRulesStore(dataDir string) *URLRulesStore {
	return &URLRulesStore{path: filepath.Join(dataDir, urlRulesFile)}
}

func (s *URLRulesStore) Load() (URLRulesConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked()
}

func (s *URLRulesStore) loadLocked() (URLRulesConfig, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return URLRulesConfig{Rules: []URLRule{}}, nil
		}
		return URLRulesConfig{}, err
	}
	var cfg URLRulesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return URLRulesConfig{}, fmt.Errorf("parse url rules: %w", err)
	}
	if cfg.Rules == nil {
		cfg.Rules = []URLRule{}
	}
	return cfg, nil
}

func (s *URLRulesStore) Save(cfg URLRulesConfig) error {
	if err := validateURLRulesConfig(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.Rules == nil {
		cfg.Rules = []URLRule{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".url-rules-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func validateURLRulesConfig(cfg URLRulesConfig) error {
	for i, rule := range cfg.Rules {
		if rule.Match == "" {
			return fmt.Errorf("rule %q: match pattern is required", rule.Name)
		}
		if _, err := regexp.Compile(rule.Match); err != nil {
			return fmt.Errorf("rule %q: invalid match regex: %w", rule.Name, err)
		}
		if rule.Replace == "" {
			return fmt.Errorf("rule %q: replace pattern is required", rule.Name)
		}
		if rule.ID == "" {
			return fmt.Errorf("rule at index %d: id is required", i)
		}
	}
	return nil
}

type URLRewriteResult struct {
	Input    string  `json:"input"`
	Output   string  `json:"output"`
	RuleID   string  `json:"ruleId,omitempty"`
	RuleName string  `json:"ruleName,omitempty"`
	Changed  bool    `json:"changed"`
}

func ApplyURLRules(raw string, cfg URLRulesConfig) (URLRewriteResult, error) {
	input := trimURL(raw)
	out := URLRewriteResult{Input: input, Output: input}
	if input == "" {
		return out, nil
	}
	for _, rule := range cfg.Rules {
		if !rule.Enabled || rule.Match == "" {
			continue
		}
		re, err := regexp.Compile(rule.Match)
		if err != nil {
			return out, fmt.Errorf("rule %q: invalid regex: %w", rule.Name, err)
		}
		if !re.MatchString(input) {
			continue
		}
		rewritten := re.ReplaceAllString(input, rule.Replace)
		out.Output = trimURL(rewritten)
		out.RuleID = rule.ID
		out.RuleName = rule.Name
		out.Changed = out.Output != input
		return out, nil
	}
	return out, nil
}

func trimURL(s string) string {
	return strings.TrimSpace(s)
}

func (a *App) rewritePageURL(raw string) (string, error) {
	if a.urlRules == nil {
		return trimURL(raw), nil
	}
	cfg, err := a.urlRules.Load()
	if err != nil {
		return "", err
	}
	res, err := ApplyURLRules(raw, cfg)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}
