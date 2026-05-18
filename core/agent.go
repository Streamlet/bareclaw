package core

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Fixed tool definitions used by every agent.
var Tools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "shell",
			Description: "Execute a whitelisted shell command.",
			Parameters: Parameters{
				Type: "object",
				Properties: map[string]Property{
					"command": {
						Type:        "string",
						Description: "The shell command to execute",
					},
					"arguments": {
						Type:        "array",
						Description: "Arguments for the shell command",
						Items: &Property{
							Type: "string",
						},
					},
				},
				Required: []string{"command"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "agent",
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
}

// Interaction protocol appended to every system prompt.
const interactionProtocol = `

## Interaction Protocol

### Tool Calls
You have two tools. Use OpenAI Function Calling format: return tool_calls in your response.

- shell: Execute a whitelisted shell command.
  Parameters: command (string), arguments (array of strings)
  Example: {"command": "ls", "arguments": ["-la"]}

- agent: Spawn a sub-agent to complete a task.
  Parameters: name (string), task (string)
  Example: {"name": "write_file", "task": "Write Hello World to test.txt"}

### Session Completion
When the user's request is fully satisfied, respond with a message containing <FINAL> tags.
The content between <FINAL> and </FINAL> will be returned as the final result.

- If the user input can be answered directly (no tools required, e.g., greetings or simple questions),
  you MUST include <FINAL> in your very first response. Do NOT return intermediate thoughts.
- If tools are needed, you may first think (content without <FINAL>) or call tools.
  Once all actions are done, respond with a message containing only <FINAL>result</FINAL>.

### Important Rules
1. Never return both content and tool_calls simultaneously.
2. When executing an action, return only tool_calls with content empty.
3. When ending a task, return only content (with <FINAL>), no tool_calls.
4. Call only one tool at a time. Wait for the result before deciding next step.
5. You may write your thinking in content (without <FINAL>) ONLY as a prelude to calling a tool.
6. If you can fulfill the request immediately, output <FINAL> directly — do not output mere pleasantries first.
`

type Agent struct {
	SystemPrompt string
	Config       *Config
	SessionID    string
	AgentDir     string // directory containing system.md and agents/
}

// LoadAgent reads system.md, scans sub-agents, replaces {{AGENTS}}, and returns an Agent.
// It works identically for root and sub-agents.
func LoadAgent(dir string, cfg *Config, sessionID string) (*Agent, error) {
	// Read system prompt
	systemPath := filepath.Join(dir, "system.md")
	systemBytes, err := os.ReadFile(systemPath)
	if err != nil {
		return nil, fmt.Errorf("read system.md from %s: %w", dir, err)
	}
	systemPrompt := string(systemBytes)

	// Look for sub-agents
	agentsDir := filepath.Join(dir, "agents")
	entries, err := os.ReadDir(agentsDir)
	var agentDescriptions string
	if err == nil && len(entries) > 0 {
		var parts []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			apiPath := filepath.Join(agentsDir, e.Name(), "api.md")
			apiBytes, err := os.ReadFile(apiPath)
			if err != nil {
				// Skip sub-agents without api.md
				continue
			}
			parts = append(parts, fmt.Sprintf("## %s\n%s", e.Name(), string(apiBytes)))
		}
		if len(parts) > 0 {
			agentDescriptions = strings.Join(parts, "\n")
		}
	}
	if agentDescriptions == "" {
		agentDescriptions = "No sub-agents available."
	}

	// Replace placeholder and append protocol
	finalPrompt := strings.Replace(systemPrompt, "{{AGENTS}}", agentDescriptions, 1)
	finalPrompt += interactionProtocol

	return &Agent{
		SystemPrompt: finalPrompt,
		Config:       cfg,
		SessionID:    sessionID,
		AgentDir:     dir,
	}, nil
}

// Run executes the main agent loop with the given user input.
func (a *Agent) Run(userInput string) (string, error) {
	workDir := filepath.Join(a.Config.Agent.Workspace, a.SessionID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	messages := []Message{
		{Role: "system", Content: a.SystemPrompt},
		{Role: "user", Content: userInput},
	}
	log.Printf("[session=%s step=%d] User input: %s", a.SessionID, 0, truncate(userInput, 200))

	for step := 0; step < a.Config.LLM.MaxSteps; step++ {
		log.Printf("[session=%s step=%d] Calling LLM...", a.SessionID, step)
		resp, err := Chat(a.Config, messages, Tools)
		if err != nil {
			return "", fmt.Errorf("LLM call failed: %w", err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("empty LLM response")
		}
		msg := resp.Choices[0].Message
		log.Printf("[session=%s step=%d] LLM respond: %s", a.SessionID, step, truncate(msg.Content, 200))

		// Case 1: model wants to call a tool
		if len(msg.ToolCalls) > 0 {
			tc := msg.ToolCalls[0]
			log.Printf("[session=%s step=%d] Tool call: %s(%s)", a.SessionID, step, tc.Function.Name, truncate(tc.Function.Arguments, 200))

			var result string
			switch tc.Function.Name {
			case "shell":
				var args struct {
					Command string `json:"command"`
					Arguments []string `json:"arguments,omitempty"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					result = fmt.Sprintf("Invalid shell arguments: %v", err)
				} else {
					res, err := ExecuteShell(args.Command, args.Arguments, a.SessionID, a.Config.Shell.AllowedCmd, a.Config.Agent.Workspace)
					if err != nil {
						result = fmt.Sprintf("Execution failed: %s", err.Error())
					} else {
						result = res
					}
				}
			case "agent":
				var args struct {
					Name string `json:"name"`
					Task string `json:"task"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					result = fmt.Sprintf("Invalid agent arguments: %v", err)
				} else {
					res, err := ExecuteAgent(a, args.Name, args.Task)
					if err != nil {
						result = fmt.Sprintf("Agent execution failed: %s", err.Error())
					} else {
						result = res
					}
				}
			default:
				result = fmt.Sprintf("Unknown tool: %s", tc.Function.Name)
			}

			log.Printf("[session=%s step=%d] Tool result: %s", a.SessionID, step, truncate(result, 200))

			// Append assistant message (with tool_calls)
			messages = append(messages, Message{
				Role:      "assistant",
				ToolCalls: msg.ToolCalls,
			})
			// Append tool result
			messages = append(messages, Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
			continue
		}

		// Case 2: model returns text
		if msg.Content != "" {
			if strings.Contains(msg.Content, "<FINAL>") {
				log.Printf("[session=%s step=%d] Task complete", a.SessionID, step)
				return extractFinal(msg.Content), nil
			}
			log.Printf("[session=%s step=%d] Thinking: %s", a.SessionID, step, truncate(msg.Content, 200))
			messages = append(messages, Message{
				Role:    "assistant",
				Content: msg.Content,
			})
			continue
		}

		return "", fmt.Errorf("empty response from model")
	}

	return "", fmt.Errorf("reached max steps %d without completion", a.Config.LLM.MaxSteps)
}

// extractFinal returns the content between the first <FINAL> and </FINAL>.
func extractFinal(content string) string {
	start := strings.Index(content, "<FINAL>")
	if start == -1 {
		return content
	}
	start += len("<FINAL>")
	end := strings.Index(content[start:], "</FINAL>")
	if end == -1 {
		return content[start:]
	}
	return content[start : start+end]
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
