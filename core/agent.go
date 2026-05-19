package core

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var ToolAgentName = "agent"
var ToolShellName = "shell"

// Fixed tool definitions used by every agent.
var Tools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        ToolAgentName,
			Description: "Spawn a sub-agent to complete a task. Sub-agents are independent agents with their own system prompt and capabilities.",
			Parameters: Parameters{
				Type: "object",
				Properties: map[string]Property{
					"name": {
						Type:        "string",
						Description: "Name of the sub-agent, corresponding to a subdirectory under agents/",
					},
					"task": {
						Type:        "string",
						Description: "Task description. The sub-agent will complete this independently.",
					},
				},
				Required: []string{"name", "task"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        ToolShellName,
			Description: "Execute shell command.",
			Parameters: Parameters{
				Type: "object",
				Properties: map[string]Property{
					"commands": {
						Type:        "array",
						Description: "The shell command pipeline to execute, the previous command's output will be piped to the next command as input.",
						Items: &Parameters{
							Type: "object",
							Properties: map[string]Property{
								"command": {
									Type:        "string",
									Description: "The single shell command to execute",
								},
								"arguments": {
									Type:        "array",
									Description: "Arguments for the shell command",
									Items: &Parameters{
										Type: "string",
									},
								},
								"stdout_to_file": {
									Type:        "string",
									Description: "If set, redirect stdout to a file. Keep empty to disable redirection. Only the last command in the pipeline can redirect output to a file.",
								},
								"stderr_to_file": {
									Type:        "string",
									Description: "If set, redirect stderr to a file. Keep empty to disable redirection. Conflicts with stderr_to_stdout if both are set.",
								},
								"stderr_to_stdout": {
									Type:        "boolean",
									Description: "Whether to redirect stderr to stdout. Conflicts with stderr_to_file if both are set.",
								},
							},
							Required: []string{"command"},
						},
					},
				},
				Required: []string{"commands"},
			},
		},
	},
}

type AgentArguments struct {
	Name string `json:"name"`
	Task string `json:"task"`
}

type ShellCommand struct {
	Command        string   `json:"command"`
	Arguments      []string `json:"arguments,omitempty"`
	StdoutToFile   string   `json:"stdout_to_file,omitempty"`
	StderrToFile   string   `json:"stderr_to_file,omitempty"`
	StderrToStdout bool     `json:"stderr_to_stdout,omitempty"`
}

type ShellArguments struct {
	Commands []ShellCommand `json:"commands"`
}

var subAgentsDir = "agents" // Relative path to sub-agents from each agent directory
var rulesPromptFileName = "rules.md"
var agentPromptFileName = "agent.md"
var apiPromptFileName = "api.md"
var subAgentDescriptionTemplate = `
================================================================================
Agent Name: %s
Agent Description:
--------------------------------------------------------------------------------
%s
================================================================================
`

type Agent struct {
	RulesPrompt string
	AgentPrompt string
	Config      *Config
	SessionID   string
	AgentDir    string // directory containing system.md and agents/
	Workspace   string
}

// LoadAgent reads agents.md, scans sub-agents, replaces {{AGENTS}}, and returns an Agent.
// It works identically for root and sub-agents.
func LoadAgent(cfg *Config, dir string, rulesPrompt string, sessionID string, workspace string) (*Agent, error) {
	if rulesPrompt == "" {
		// Read rule prompt
		rulePromptFilePath := filepath.Join(dir, rulesPromptFileName)
		rulePromptBytes, err := os.ReadFile(rulePromptFilePath)
		if err != nil {
			return nil, fmt.Errorf("read %s from %s: %w", rulesPromptFileName, dir, err)
		}
		rulesPrompt = string(rulePromptBytes)
	}
	// Read agent prompt
	agentPromptFilePath := filepath.Join(dir, agentPromptFileName)
	agentPromptBytes, err := os.ReadFile(agentPromptFilePath)
	if err != nil {
		return nil, fmt.Errorf("read %s from %s: %w", agentPromptFileName, dir, err)
	}
	agentPrompt := string(agentPromptBytes)

	// Look for sub-agents
	agentsDir := filepath.Join(dir, subAgentsDir)
	entries, err := os.ReadDir(agentsDir)
	var agentDescriptions string
	if err == nil && len(entries) > 0 {
		var subAgents []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			apiPromptPath := filepath.Join(agentsDir, e.Name(), apiPromptFileName)
			apiPromptBytes, err := os.ReadFile(apiPromptPath)
			if err != nil {
				// Skip sub-agents without api.md
				continue
			}
			subAgents = append(subAgents, fmt.Sprintf(subAgentDescriptionTemplate, e.Name(), string(apiPromptBytes)))
		}
		if len(subAgents) > 0 {
			agentDescriptions = strings.Join(subAgents, "\n")
		}
	}
	if agentDescriptions == "" {
		agentDescriptions = "No sub-agents available."
	}

	// Replace placeholder and append protocol
	agentPrompt = strings.Replace(agentPrompt, "{{AGENTS}}", agentDescriptions, 1)

	return &Agent{
		RulesPrompt: rulesPrompt,
		AgentPrompt: agentPrompt,
		Config:      cfg,
		SessionID:   sessionID,
		AgentDir:    dir,
		Workspace:   workspace,
	}, nil
}

// Run executes the main agent loop with the given user input.
func (a *Agent) Run(userInput string) (string, error) {
	if err := os.MkdirAll(a.Workspace, 0755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	messages := []Message{
		{Role: RoleSystem, Content: a.RulesPrompt + "\n" + a.AgentPrompt},
		{Role: RoleUser, Content: userInput},
	}
	log.Printf("[session=%s step=%d] User input: %s", a.SessionID, 0, truncate(userInput, 200))

	for step := 0; step < a.Config.LLM.MaxSteps; step++ {
		log.Printf("[session=%s step=%d] Calling LLM...", a.SessionID, step)
		request := ChatCompletionRequest{
			Messages: messages,
			Tools:    Tools,
		}
		response, err := Chat(&a.Config.LLM, request)
		if err != nil {
			return "", fmt.Errorf("LLM call failed: %w", err)
		}
		if len(response.Choices) == 0 {
			return "", fmt.Errorf("empty LLM response")
		}
		msg := response.Choices[0].Message
		log.Printf("[session=%s step=%d] LLM respond: %s", a.SessionID, step, truncate(msg.Content, 200))

		if len(msg.ToolCalls) > 0 {
			messages = append(messages, Message{
				Role:      RoleAssistant,
				Content:   msg.Content,
				ToolCalls: msg.ToolCalls,
			})
			for _, tc := range msg.ToolCalls {
				log.Printf("[session=%s step=%d] Tool call: %s(%s)", a.SessionID, step, tc.Function.Name, truncate(tc.Function.Arguments, 200))
				var result string
				switch tc.Function.Name {
				case ToolAgentName:
					var args AgentArguments
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						result = fmt.Sprintf("Invalid agent arguments: %v", err)
					} else {
						subAgentDir := filepath.Join(a.AgentDir, subAgentsDir, args.Name)
						if _, err := os.Stat(subAgentDir); os.IsNotExist(err) {
							return fmt.Sprintf("Agent not found: %s", args.Name), nil
						}
						subWorkspace := filepath.Join(a.Workspace, args.Name)
						if _, err := os.Stat(subWorkspace); os.IsNotExist(err) {
							os.Mkdir(subWorkspace, 0755)
						}
						subAgent, err := LoadAgent(a.Config, subAgentDir, a.RulesPrompt, a.SessionID, subWorkspace)
						if err != nil {
							return "", fmt.Errorf("load sub-agent %s: %w", args.Name, err)
						}
						res, err := subAgent.Run(args.Task)
						if err != nil {
							result = fmt.Sprintf("Agent execution failed: %s", err.Error())
						} else {
							result = res
						}
					}
				case ToolShellName:
					var args ShellArguments
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						result = fmt.Sprintf("Invalid shell arguments: %v", err)
					} else {
						res, err := ExecuteShell(args.Commands, a.SessionID, a.Config.Shell.Command, a.Workspace)
						if err != nil {
							result = fmt.Sprintf("Execution failed: %s", err.Error())
						} else {
							result = res
						}
					}
				default:
					result = fmt.Sprintf("Unknown tool: %s", tc.Function.Name)
				}
				log.Printf("[session=%s step=%d] Tool result: %s", a.SessionID, step, truncate(result, 200))
				messages = append(messages, Message{
					Role:       RoleTool,
					ToolCallID: tc.ID,
					Content:    result,
				})
			}
		} else {
			if msg.Content != "" {
				log.Printf("[session=%s step=%d] Task complete", a.SessionID, step)
				messages = append(messages, Message{
					Role:    RoleAssistant,
					Content: msg.Content,
				})
				return msg.Content, nil
			} else {
				return "", fmt.Errorf("empty response from model")
			}
		}
	}
	return "", fmt.Errorf("reached max steps %d without completion", a.Config.LLM.MaxSteps)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
