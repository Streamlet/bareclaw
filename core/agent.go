package core

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	mainAgentName       = "main"
	toolAgent           = "agent"
	toolShell           = "shell"
	agentsPlaceHolder   = "{{AGENTS}}"
	commandsPlaceHolder = "{{COMMANDS}}"
)

var toolsDefine = []tool{
	{
		Type: "function",
		Function: toolFunction{
			Name:        toolAgent,
			Description: "Spawn a sub-agent to complete a task. Sub-agents are independent agents with their own system prompt and capabilities.",
			Parameters: jsonSchema{
				Type: "object",
				Properties: map[string]jsonSchema{
					"name": {Type: "string", Description: "Name of the sub-agent, corresponding to a subdirectory under agents/"},
					"task": {Type: "string", Description: "Task description. The sub-agent will complete this independently."},
				},
				Required: []string{"name", "task"},
			},
		},
	},
	{
		Type: "function",
		Function: toolFunction{
			Name:        toolShell,
			Description: "Execute native shell commands. Current platform is " + runtime.GOOS + ".",
			Parameters: jsonSchema{
				Type: "object",
				Properties: map[string]jsonSchema{
					"commands": {
						Type:        "array",
						Description: "The shell commands execute. If redirection.to_next is set, the command's stdout will be piped to the next command as stdin, equaling to `cmd1 | cmd2 | ...`. Otherwise, commands are executed independently one by one, equaling to `cmd1; cmd2; ...`.",
						Items: &jsonSchema{
							Type:        "object",
							Description: "A single shell command with optional arguments and redirections.",
							Properties: map[string]jsonSchema{
								"command": {Type: "string", Description: "The single shell command to execute. Must not contain spaces or redirection operators ('>', '>>', '2>', '2>>', '2>&1'). Put arguments in the 'arguments' field, and put redirections in 'redirection' field."},
								"arguments": {
									Type:        "array",
									Description: "Arguments for the shell command. Must not contain redirection operators ('>', '>>', '2>', '2>>', '2>&1'). Put redirections in the 'redirection' field.",
									Items: &jsonSchema{
										Type: "string",
									},
								},
								"redirection": {
									Type:        "object",
									Description: "If set, redirect stdout and/or stderr of the command.",
									Properties: map[string]jsonSchema{
										"stdout": {
											Type: "object",
											Properties: map[string]jsonSchema{
												"file":      {Type: "string", Description: "Redirect stdout to this file."},
												"append":    {Type: "boolean", Description: "Use append mode for stdout redirection. Default is false (overwrite)."},
												"to_stderr": {Type: "boolean", Description: "Redirect stdout to stderr. Default is false."},
												"to_next":   {Type: "boolean", Description: "Redirect stdout to the next command in the pipeline. Default is false."},
											},
										},
										"stderr": {
											Type: "object",
											Properties: map[string]jsonSchema{
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

type agentArguments struct {
	Name string `json:"name"`
	Task string `json:"task"`
}

type shellArguments struct {
	Commands []shellCommand `json:"commands"`
}

type shellCommand struct {
	Command     string           `json:"command"`
	Arguments   []string         `json:"arguments,omitempty"`
	Redirection shellRedirection `json:"redirection,omitempty"`
}

type shellRedirection struct {
	StdOut shellStdoutRedirection `json:"stdout,omitempty"`
	StdErr shellStderrRedirection `json:"stderr,omitempty"`
}

type shellStdoutRedirection struct {
	File     string `json:"file,omitempty"`
	Append   bool   `json:"append,omitempty"`
	ToStdErr bool   `json:"to_stderr,omitempty"`
	ToNext   bool   `json:"to_next,omitempty"`
}

type shellStderrRedirection struct {
	File     string `json:"file,omitempty"`
	Append   bool   `json:"append,omitempty"`
	ToStdOut bool   `json:"to_stdout,omitempty"`
}

type Agent struct {
	Name         string
	PathName     string
	Config       *Config
	CommonPrompt string
	AgentPrompt  string
	SessionID    string
	ToolCallID   string
	Messages     []message
	SystemDir    string
	HistoryDir   string
	WorkDir      string
}

func generateSessionID() string {
	ts := time.Now().Format("20060102-150405")
	rnd := make([]byte, 6)
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	for i := range rnd {
		rnd[i] = charset[rand.Intn(len(charset))]
	}
	return ts + "-" + string(rnd)
}

func LoadAgent(config *Config, parent *Agent, dir string, toolCallID string) (*Agent, error) {
	var agent Agent
	agent.ToolCallID = toolCallID
	agent.SystemDir = dir
	if parent != nil {
		agent.Name = filepath.Base(dir)
		agent.PathName = parent.PathName + ">" + agent.Name
		agent.Config = parent.Config
		agent.SessionID = parent.SessionID
		agent.HistoryDir = filepath.Join(parent.HistoryDir, agent.ToolCallID)
		_ = os.MkdirAll(agent.HistoryDir, 0755)
		agent.WorkDir = parent.WorkDir
	} else {
		agent.Name = mainAgentName
		agent.PathName = mainAgentName
		agent.Config = config
		agent.SessionID = generateSessionID()
		agent.HistoryDir = filepath.Join(config.Agent.HistoryDir, agent.SessionID)
		_ = os.MkdirAll(agent.HistoryDir, 0755)
		agent.WorkDir = filepath.Join(config.Agent.WorkDir, agent.SessionID)
		_ = os.MkdirAll(agent.WorkDir, 0755)
	}
	if agent.Config == nil {
		return nil, fmt.Errorf("config must be provided for root agent")
	}

	if parent != nil {
		agent.CommonPrompt = parent.CommonPrompt
	} else if config.Prompt.Common != "" {
		commonPromptFilePath := filepath.Join(dir, config.Prompt.Common)
		commonPromptBytes, err := os.ReadFile(commonPromptFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load common prompt, read %s from %s: %w", config.Prompt.Common, dir, err)
		}
		agent.CommonPrompt = string(commonPromptBytes)
	}

	if config.Prompt.AgentImplement == "" {
		return nil, fmt.Errorf("agent_implement must be specified in config")
	}
	agentPromptFilePath := filepath.Join(dir, config.Prompt.AgentImplement)
	agentPromptBytes, err := os.ReadFile(agentPromptFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent implement prompt, read %s from %s: %w", config.Prompt.AgentImplement, dir, err)
	}
	agent.AgentPrompt = string(agentPromptBytes)

	var availableCommands string
	if len(agent.Config.Shell.Commands) > 0 {
		availableCommands = strings.Join(agent.Config.Shell.Commands, ", ")
	} else {
		availableCommands = "(None)"
	}

	entries, err := os.ReadDir(dir)
	var agentNames []string
	var agentDescriptions []string
	if err == nil && len(entries) > 0 {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			apiPromptPath := filepath.Join(dir, e.Name(), config.Prompt.AgentAPI)
			apiPromptBytes, err := os.ReadFile(apiPromptPath)
			if err != nil {
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

	agent.CommonPrompt = strings.Replace(agent.CommonPrompt, commandsPlaceHolder, availableCommands, -1)
	agent.AgentPrompt = strings.Replace(agent.AgentPrompt, agentsPlaceHolder, subAgentDescription, -1)

	return &agent, nil
}

func (a *Agent) appendHistory(message message) {
	a.Messages = append(a.Messages, message)

	if a.HistoryDir != "" {
		var historyFilePath string
		if a.ToolCallID == "" {
			historyFilePath = filepath.Join(a.HistoryDir, "main.json")
		} else {
			historyFilePath = filepath.Join(a.HistoryDir, fmt.Sprintf("%s.json", a.Name))
		}
		messageData, err := json.Marshal(message)
		if err != nil {
			return
		}
		f, err := os.OpenFile(historyFilePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		_, err = f.Write(messageData)
		if err != nil {
			return
		}
		_, err = f.Write([]byte("\n"))
		if err != nil {
			return
		}
	}
}

func (a *Agent) Run(userInput string) (string, error) {
	if err := os.MkdirAll(a.WorkDir, 0755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	if a.Messages == nil {
		a.appendHistory(message{Role: roleSystem, Content: a.CommonPrompt + "\n" + a.AgentPrompt})
	}
	a.appendHistory(message{Role: roleUser, Content: userInput})

	for step := 0; step < a.Config.LLM.MaxSteps; step++ {
		request := chatCompletionRequest{
			Messages: a.Messages,
			Tools:    toolsDefine,
		}
		response, err := chat(&a.Config.LLM, request)
		if err != nil {
			return "", fmt.Errorf("LLM call failed: %w", err)
		}
		if len(response.Choices) == 0 {
			return "", fmt.Errorf("empty LLM response")
		}
		msg := response.Choices[0].Message

		if len(msg.ToolCalls) > 0 {
			a.appendHistory(message{
				Role:      roleAssistant,
				Content:   msg.Content,
				ToolCalls: msg.ToolCalls,
			})
			if msg.Content != "" {
				log.Printf("[%s] Thinking: %s", a.PathName, msg.Content)
			}
			for _, tc := range msg.ToolCalls {
				log.Printf("[%s] ToolCall: %s(%s)", a.PathName, tc.Function.Name, tc.Function.Arguments)
				var result string
				switch tc.Function.Name {
				case toolAgent:
					for {
						var args agentArguments
						if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
							result = fmt.Sprintf("Invalid agent arguments: %v", err)
							break
						}
						subAgentDir := filepath.Join(a.SystemDir, args.Name)
						if _, err := os.Stat(subAgentDir); os.IsNotExist(err) {
							result = fmt.Sprintf("Agent %s not found: %s not exists", args.Name, subAgentDir)
							break
						}
						subAgent, err := LoadAgent(a.Config, a, subAgentDir, tc.ID)
						if err != nil {
							result = fmt.Sprintf("load sub-agent %s: %v", args.Name, err)
							break
						}
						agentResult, err := subAgent.Run(args.Task)
						if err != nil {
							result = fmt.Sprintf("Agent execution failed: %s", err.Error())
							break
						}
						result = agentResult
						break
					}
				case toolShell:
					for {
						var args shellArguments
						if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
							result = fmt.Sprintf("Invalid shell arguments: %v", err)
							break
						}
						type ShellResultsWithError struct {
							Results []shellResult `json:"results,omitempty"`
							Error   string        `json:"error,omitempty"`
						}
						var shellResults ShellResultsWithError
						shellResults.Results, err = shellExec(args.Commands, a.Config.Shell, a.WorkDir)
						if err != nil {
							shellResults.Error = err.Error()
						}
						resultBytes, err := json.Marshal(shellResults)
						if err != nil {
							result = fmt.Sprintf("Failed to marshal shell results: %s\n%v", err.Error(), shellResults)
							break
						}
						result = string(resultBytes)
						break
					}
				default:
					result = fmt.Sprintf("Unknown tool: %s", tc.Function.Name)
				}
				a.appendHistory(message{
					Role:       roleTool,
					ToolCallID: tc.ID,
					Content:    result,
				})
				log.Printf("[%s] ToolResult: %s", a.PathName, truncate(result, 200))
			}
		} else {
			if msg.Content == "" {
				return "", fmt.Errorf("empty response from model")
			}
			a.appendHistory(message{
				Role:    roleAssistant,
				Content: msg.Content,
			})
			return msg.Content, nil
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
