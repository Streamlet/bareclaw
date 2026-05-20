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
			Parameters: JsonSchema{
				Type: "object",
				Properties: map[string]JsonSchema{
					"name": {Type: "string", Description: "Name of the sub-agent, corresponding to a subdirectory under agents/"},
					"task": {Type: "string", Description: "Task description. The sub-agent will complete this independently."},
				},
				Required: []string{"name", "task"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        ToolShellName,
			Description: "Execute shell commands.",
			Parameters: JsonSchema{
				Type: "object",
				Properties: map[string]JsonSchema{
					"commands": {
						Type:        "array",
						Description: "The shell command pipeline to execute, the previous command's output will be piped to the next command as input. Equal to `cmd1 | cmd2 | ...`. Must not use '|' in individual commands.",
						Items: &JsonSchema{
							Type:        "object",
							Description: "A single shell command with optional arguments and redirections. Redirections will only work for the last command in the pipeline.",
							Properties: map[string]JsonSchema{
								"command": {Type: "string", Description: "The single shell command to execute. Must not contain spaces or redirection operators ('>', '>>', '2>', '2>>', '2>&1'). Put arguments in the 'arguments' field, and put redirections in 'redirection' field."},
								"arguments": {
									Type:        "array",
									Description: "Arguments for the shell command. Must not contain redirection operators ('>', '>>', '2>', '2>>', '2>&1'). Put redirections in the 'redirection' field.",
									Items: &JsonSchema{
										Type: "string",
									},
								},
								"redirection": {
									Type:        "object",
									Description: "If set, redirect stdout and/or stderr of the command. Only the last command in the pipeline can have redirection.",
									Properties: map[string]JsonSchema{
										"stdout": {
											Type: "object",
											Properties: map[string]JsonSchema{
												"file":      {Type: "string", Description: "Redirect stdout to this file."},
												"append":    {Type: "boolean", Description: "Use append mode for stdout redirection. Default is false (overwrite)."},
												"to_stderr": {Type: "boolean", Description: "Redirect stdout to stderr. Default is false."},
											},
										},
										"stderr": {
											Type: "object",
											Properties: map[string]JsonSchema{
												"file":      {Type: "string", Description: "Redirect stderr to this file."},
												"append":    {Type: "boolean", Description: "Use append mode for stderr redirection. Default is false (overwrite)."},
												"to_stdout": {Type: "boolean", Description: "Redirect stderr to stdout. Default is false."},
											},
										},
									},
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

type ShellArguments struct {
	Commands []ShellCommand `json:"commands"`
}

type ShellCommand struct {
	Command     string           `json:"command"`
	Arguments   []string         `json:"arguments,omitempty"`
	Redirection ShellRedirection `json:"redirection,omitempty"`
}

type ShellRedirection struct {
	StdOut ShellStdoutRedirection `json:"stdout,omitempty"`
	StdErr ShellStderrRedirection `json:"stderr,omitempty"`
}

type ShellStdoutRedirection struct {
	File     string `json:"file,omitempty"`
	Append   bool   `json:"append,omitempty"`
	ToStdErr bool   `json:"to_stderr,omitempty"`
}

type ShellStderrRedirection struct {
	File     string `json:"file,omitempty"`
	Append   bool   `json:"append,omitempty"`
	ToStdOut bool   `json:"to_stdout,omitempty"`
}

var subAgentsDir = "agents" // Relative path to sub-agents from each agent directory
var rulesPromptFileName = "rules.md"
var agentPromptFileName = "agent.md"
var apiPromptFileName = "api.md"
var subAgentsDescriptionPlaceholder = "{{AGENTS_DESCRIPTION}}"
var availableSubAgentsPlaceholder = "{{AVAILABLE_AGENTS}}"
var availableCommandsPlaceholder = "{{AVAILABLE_COMMANDS}}"

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

	var availableCommands string
	if len(cfg.Shell.Commands) > 0 {
		availableCommands = strings.Join(cfg.Shell.Commands, ", ")
	} else {
		availableCommands = "(None)"
	}

	// Look for sub-agents
	agentsDir := filepath.Join(dir, subAgentsDir)
	entries, err := os.ReadDir(agentsDir)
	var agentNames []string
	var agentDescriptions []string
	if err == nil && len(entries) > 0 {
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
			agentNames = append(agentNames, e.Name())
			agentDescriptions = append(agentDescriptions, string(apiPromptBytes))
		}
	}
	var availableSubAgents string
	if len(agentNames) > 0 {
		availableSubAgents = strings.Join(agentNames, ", ")
	} else {
		availableSubAgents = "(None)"
	}
	var subAgentDescription string
	if len(agentDescriptions) > 0 {
		subAgentDescription = strings.Join(agentDescriptions, "\n")
	} else {
		subAgentDescription = "No sub-agents available."
	}

	// Replace placeholders
	rulesPrompt = strings.Replace(rulesPrompt, availableSubAgentsPlaceholder, availableSubAgents, -1)
	rulesPrompt = strings.Replace(rulesPrompt, availableCommandsPlaceholder, availableCommands, -1)
	rulesPrompt = strings.Replace(rulesPrompt, subAgentsDescriptionPlaceholder, subAgentDescription, -1)
	agentPrompt = strings.Replace(agentPrompt, availableSubAgentsPlaceholder, availableSubAgents, -1)
	agentPrompt = strings.Replace(agentPrompt, availableCommandsPlaceholder, availableCommands, -1)
	agentPrompt = strings.Replace(agentPrompt, subAgentsDescriptionPlaceholder, subAgentDescription, -1)

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
		//log.Printf("[session=%s step=%d] LLM request:\n%s", a.SessionID, step, request)
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
						output, exitCodes, err := ExecuteShell(args.Commands, a.SessionID, a.Config.Shell, a.Workspace)
						if err != nil {
							result = fmt.Sprintf("Execution failed: %s", err.Error())
						} else {
							result = "Exit Codes: " + fmt.Sprint(exitCodes)
							if output != "" {
								result += "\nOutput:\n" + output
							} else {
								result += "\n(No output)"
							}
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
