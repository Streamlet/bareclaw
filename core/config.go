package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	LLM   LLMConfig
	Agent AgentConfig
	Shell ShellConfig
}

type LLMConfig struct {
	APIKey   string
	BaseURL  string
	Model    string
	MaxSteps int
}

type AgentConfig struct {
	Root      string
	Workspace string
}

type ShellConfig struct {
	AllowedCmd []string
}

// Minimal TOML parser that handles the required sections.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		LLM: LLMConfig{
			MaxSteps: 10,
		},
		Agent: AgentConfig{
			Root:      ".",
			Workspace: "./workspace",
		},
	}

	currentSection := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = line[1 : len(line)-1]
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// remove optional quotes from value
		val = strings.Trim(val, "\"'")
		switch currentSection {
		case "llm":
			switch key {
			case "api_key":
				cfg.LLM.APIKey = val
			case "base_url":
				cfg.LLM.BaseURL = val
			case "model":
				cfg.LLM.Model = val
			case "max_steps":
				if n, err := strconv.Atoi(val); err == nil {
					cfg.LLM.MaxSteps = n
				}
			}
		case "agent":
			switch key {
			case "root":
				cfg.Agent.Root = val
			case "workspace":
				cfg.Agent.Workspace = val
			}
		case "shell":
			if key == "allowed_cmd" {
				// parse array: ["cmd1","cmd2"]
				val = strings.TrimPrefix(val, "[")
				val = strings.TrimSuffix(val, "]")
				items := strings.Split(val, ",")
				for _, item := range items {
					item = strings.TrimSpace(item)
					item = strings.Trim(item, "\"'")
					if item != "" {
						cfg.Shell.AllowedCmd = append(cfg.Shell.AllowedCmd, item)
					}
				}
			}
		}
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
	return cfg, nil
}
