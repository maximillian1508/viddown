package main

import (
	"fmt"
	"regexp"
	"strings"
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

func validateURLRulesConfig(cfg URLRulesConfig) error {
	for _, r := range cfg.Rules {
		if strings.TrimSpace(r.Match) == "" {
			return fmt.Errorf("rule %q: match pattern required", r.Name)
		}
		if _, err := regexp.Compile(r.Match); err != nil {
			return fmt.Errorf("rule %q: invalid match regex: %w", r.Name, err)
		}
	}
	return nil
}

type URLRewriteResult struct {
	Output   string `json:"output"`
	Changed  bool   `json:"changed"`
	RuleName string `json:"ruleName,omitempty"`
}

func ApplyURLRules(raw string, cfg URLRulesConfig) (URLRewriteResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return URLRewriteResult{Output: raw}, nil
	}
	for _, r := range cfg.Rules {
		if !r.Enabled {
			continue
		}
		re, err := regexp.Compile(r.Match)
		if err != nil {
			return URLRewriteResult{}, fmt.Errorf("rule %q: %w", r.Name, err)
		}
		out := re.ReplaceAllString(raw, r.Replace)
		if out != raw {
			return URLRewriteResult{Output: out, Changed: true, RuleName: r.Name}, nil
		}
	}
	return URLRewriteResult{Output: raw}, nil
}

func (a *App) rewritePageURL(pageURL string) (string, error) {
	if a.db == nil {
		return pageURL, nil
	}
	cfg, err := a.db.LoadURLRules()
	if err != nil {
		return "", err
	}
	res, err := ApplyURLRules(pageURL, cfg)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}
