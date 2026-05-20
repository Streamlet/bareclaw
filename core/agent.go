package core

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	SUB_AGENTS_DIR       = "agents" // Relative path to sub-agents from each agent directory
	RULES_MD             = "rules.md"
	AGENT_MD             = "agent.md"
	API_MD               = "api.md"
	AGENTS_PLACEHOLDER   = "{{AGENTS}}"
	COMMANDS_PLACEHOLDER = "{{COMMANDS}}"
	TOOL_AGENT           = "agent"
	TOOL_SHELL           = "shell"
)

// Fixed tool definitions used by every agent.
var Tools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        TOOL_AGENT,
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
			Name:        TOOL_SHELL,
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

type Agent struct {
	Name        string
	Config      *Config
	RulesPrompt string
	AgentPrompt string
	SessionID   string
	ToolCallID  string
	Messages    []Message
	SystemDir   string
	HistoryDir  string
	WorkDir     string
}

// LoadAgent reads agents.md, scans sub-agents, replaces {{AGENTS}}, and returns an Agent.
// It works identically for root and sub-agents.
func LoadAgent(config *Config, parent *Agent, dir string, toolCallID string) (*Agent, error) {
	var agent Agent
	agent.Name = filepath.Base(dir)
	agent.ToolCallID = toolCallID
	agent.SystemDir = dir
	if parent != nil {
		agent.Config = parent.Config
		agent.SessionID = parent.SessionID
		agent.HistoryDir = parent.HistoryDir
		agent.WorkDir = parent.WorkDir
	} else {
		agent.Config = config
		agent.SessionID = GenerateSessionID()
		agent.HistoryDir = filepath.Join(config.Agent.HistoryDir, agent.SessionID)
		_ = os.MkdirAll(agent.HistoryDir, 0755)
		agent.WorkDir = filepath.Join(config.Agent.WorkDir, agent.SessionID)
		_ = os.MkdirAll(agent.WorkDir, 0755)
	}
	if agent.Config == nil {
		return nil, fmt.Errorf("config must be provided for root agent")
	}

	if parent != nil {
		agent.RulesPrompt = parent.RulesPrompt
	} else {
		// Read rule prompt
		rulePromptFilePath := filepath.Join(dir, RULES_MD)
		rulePromptBytes, err := os.ReadFile(rulePromptFilePath)
		if err != nil {
			return nil, fmt.Errorf("read %s from %s: %w", RULES_MD, dir, err)
		}
		agent.RulesPrompt = string(rulePromptBytes)
	}

	// Read agent prompt
	agentPromptFilePath := filepath.Join(dir, AGENT_MD)
	agentPromptBytes, err := os.ReadFile(agentPromptFilePath)
	if err != nil {
		return nil, fmt.Errorf("read %s from %s: %w", AGENT_MD, dir, err)
	}
	agent.AgentPrompt = string(agentPromptBytes)

	var availableCommands string
	if len(agent.Config.Shell.Commands) > 0 {
		availableCommands = strings.Join(agent.Config.Shell.Commands, ", ")
	} else {
		availableCommands = "(None)"
	}

	// Look for sub-agents
	agentsDir := filepath.Join(dir, SUB_AGENTS_DIR)
	entries, err := os.ReadDir(agentsDir)
	var agentNames []string
	var agentDescriptions []string
	if err == nil && len(entries) > 0 {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			apiPromptPath := filepath.Join(agentsDir, e.Name(), API_MD)
			apiPromptBytes, err := os.ReadFile(apiPromptPath)
			if err != nil {
				// Skip sub-agents without api.md
				continue
			}
			agentNames = append(agentNames, e.Name())
			agentDescriptions = append(agentDescriptions, string(apiPromptBytes))
		}
	}

	var subAgentDescription string
	if len(agentDescriptions) > 0 {
		subAgentDescription = strings.Join(agentDescriptions, "\n")
	} else {
		subAgentDescription = "No sub-agents available."
	}

	// Replace placeholders
	agent.RulesPrompt = strings.Replace(agent.RulesPrompt, COMMANDS_PLACEHOLDER, availableCommands, -1)
	agent.AgentPrompt = strings.Replace(agent.AgentPrompt, AGENTS_PLACEHOLDER, subAgentDescription, -1)

	return &agent, nil
}

func (a *Agent) appendHistory(message Message) {
	a.Messages = append(a.Messages, message)
	// Save history to file after each update
	if a.HistoryDir != "" {
		var historyFilePath string
		if a.ToolCallID == "" {
			historyFilePath = filepath.Join(a.HistoryDir, "main.json")
		} else {
			historyFilePath = filepath.Join(a.HistoryDir, fmt.Sprintf("%s.json", a.ToolCallID))
		}
		messageData, err := json.Marshal(message)
		if err != nil {
			log.Printf("Failed to marshal history: %v", err)
			return
		}
		f, err := os.OpenFile(historyFilePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Printf("Failed to open history file: %v", err)
			return
		}
		defer f.Close()
		_, err = f.Write(messageData)
		if err != nil {
			log.Printf("Failed to write history: %v", err)
			return
		}
		_, err = f.Write([]byte("\n"))
		if err != nil {
			log.Printf("Failed to write history newline: %v", err)
			return
		}
	}
}

// Run executes the main agent loop with the given user input.
func (a *Agent) Run(userInput string) (string, error) {
	if err := os.MkdirAll(a.WorkDir, 0755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	if a.Messages == nil {
		a.appendHistory(Message{Role: RoleSystem, Content: a.RulesPrompt + "\n" + a.AgentPrompt})
	}
	a.appendHistory(Message{Role: RoleUser, Content: userInput})

	for step := 0; step < a.Config.LLM.MaxSteps; step++ {
		request := ChatCompletionRequest{
			Messages: a.Messages,
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
		log.Printf("[session=%s toolcall=%s step=%d] LLM respond: %s [%s]", a.SessionID, a.ToolCallID, step, truncate(msg.Content, 200), msg.ToolCalls)

		if len(msg.ToolCalls) > 0 {
			a.appendHistory(Message{
				Role:      RoleAssistant,
				Content:   msg.Content,
				ToolCalls: msg.ToolCalls,
			})
			for _, tc := range msg.ToolCalls {
				var result string
				switch tc.Function.Name {
				case TOOL_AGENT:
					var args AgentArguments
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						result = fmt.Sprintf("Invalid agent arguments: %v", err)
					} else {
						log.Print(a.SystemDir, SUB_AGENTS_DIR, args.Name)
						subAgentDir := filepath.Join(a.SystemDir, SUB_AGENTS_DIR, args.Name)
						log.Print(subAgentDir)
						if _, err := os.Stat(subAgentDir); os.IsNotExist(err) {
							return fmt.Sprintf("Agent %s not found: %s not exists", args.Name, subAgentDir), nil
						}
						subWorkspace := filepath.Join(a.WorkDir, args.Name)
						if _, err := os.Stat(subWorkspace); os.IsNotExist(err) {
							os.Mkdir(subWorkspace, 0755)
						}
						subAgent, err := LoadAgent(nil, a, subAgentDir, tc.ID)
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
				case TOOL_SHELL:
					var args ShellArguments
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						result = fmt.Sprintf("Invalid shell arguments: %v", err)
					} else {
						output, exitCodes, err := ExecuteShell(args.Commands, a.SessionID, a.Config.Shell, a.WorkDir)
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
				a.appendHistory(Message{
					Role:       RoleTool,
					ToolCallID: tc.ID,
					Content:    result,
				})
			}
		} else {
			if msg.Content != "" {
				a.appendHistory(Message{
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
