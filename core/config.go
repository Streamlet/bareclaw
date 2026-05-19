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
	Root      string `toml:"root"`
	Workspace string `toml:"workspace"`
}

type ShellConfig struct {
	AllowedCmd []string `toml:"allowed_cmd"`
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
	if !filepath.IsAbs(cfg.Agent.Root) {
		absRoot, err := filepath.Abs(cfg.Agent.Root)
		if err == nil {
			cfg.Agent.Root = absRoot
		}
	}
	if !filepath.IsAbs(cfg.Agent.Workspace) {
		absWs, err := filepath.Abs(cfg.Agent.Workspace)
		if err == nil {
			cfg.Agent.Workspace = absWs
		}
	}
	return &cfg, nil
}
