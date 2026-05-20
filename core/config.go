package core

import (
	"fmt"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	LLM   LLMConfig   `toml:"llm"`
	Agent AgentConfig `toml:"agent"`
	Shell ShellConfig `toml:"shell"`
}

type LLMConfig struct {
	APIKey   string `toml:"api_key"`
	BaseURL  string `toml:"base_url"`
	Model    string `toml:"model"`
	MaxSteps int    `toml:"max_steps"`
}

type AgentConfig struct {
	SystemDir  string `toml:"system_dir"`
	HistoryDir string `toml:"history_dir"`
	WorkDir    string `toml:"work_dir"`
}

type ShellConfig struct {
	Commands     []string                `toml:"commands"`
	PathLocation map[string]PathLocation `toml:"path_location"`
}

type PathLocation struct {
	Position []uint   `toml:"position"`
	After    []string `toml:"after"`
	Prefix   []string `toml:"prefix"`
}

func LoadConfig(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Validate required fields
	if cfg.LLM.APIKey == "" || cfg.LLM.BaseURL == "" || cfg.LLM.Model == "" {
		return nil, fmt.Errorf("api_key, base_url, and model must be set in config [llm]")
	}
	// Resolve relative paths to absolute
	if !filepath.IsAbs(cfg.Agent.SystemDir) {
		absSystemDir, err := filepath.Abs(cfg.Agent.SystemDir)
		if err == nil {
			cfg.Agent.SystemDir = absSystemDir
		}
	}
	if !filepath.IsAbs(cfg.Agent.HistoryDir) {
		absHistoryDir, err := filepath.Abs(cfg.Agent.HistoryDir)
		if err == nil {
			cfg.Agent.HistoryDir = absHistoryDir
		}
	}
	if !filepath.IsAbs(cfg.Agent.WorkDir) {
		absWorkDir, err := filepath.Abs(cfg.Agent.WorkDir)
		if err == nil {
			cfg.Agent.WorkDir = absWorkDir
		}
	}
	return &cfg, nil
}
