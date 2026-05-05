package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	BBox       []float64   `json:"bbox"`
	StartDate  string      `json:"start_date"`
	EndDate    string      `json:"end_date"`
	MaxCloud   float64     `json:"max_cloud"`
	Bands      []string    `json:"bands"`
	Limit      int         `json:"limit"`
	MaxWorkers int         `json:"max_workers"`
	MaxRetries int         `json:"max_retries"`
	STACURL    string      `json:"stac_url,omitempty"`
	Collection string      `json:"collection,omitempty"`
	Auth       *AuthConfig `json:"auth,omitempty"`
}

type SearchOptions struct {
	Bbox       []float64
	StartDate  string
	EndDate    string
	Limit      int
	MaxCloud   float64
	STACURL    string
	Collection string
}

type AuthConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func resolveEnv(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		return os.Getenv(s[2 : len(s)-1])
	}
	return s
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	if cfg.Limit == 0 {
		cfg.Limit = 20
	}
	if cfg.MaxWorkers == 0 {
		cfg.MaxWorkers = 4
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.STACURL == "" {
		cfg.STACURL = EarthSearchURL
	}
	if cfg.Collection == "" {
		cfg.Collection = Collection
	}
	return &cfg, nil
}

func mergeSettings(cfg *Config) {
	s, err := loadSettings()
	if err != nil || s == nil {
		return
	}
	if cfg.STACURL == "" || cfg.STACURL == EarthSearchURL {
		if s.STACURL != "" {
			cfg.STACURL = s.STACURL
		}
	}
	if cfg.Collection == "" || cfg.Collection == Collection {
		if s.Collection != "" {
			cfg.Collection = s.Collection
		}
	}
	if cfg.Auth == nil || cfg.Auth.Username == "" {
		if s.Auth != nil && s.Auth.Username != "" {
			cfg.Auth = s.Auth
		}
	}
}
