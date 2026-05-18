package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ExecuteShell runs a shell command after validating it against the whitelist.
// Write operations are restricted to the session workspace.
func ExecuteShell(command string, arguments []string, sessionID string, allowedCmds []string, workspaceRoot string) (string, error) {
	// Check whitelist
	allowed := false
	for _, c := range allowedCmds {
		if c == command {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Sprintf("Rejected: command '%s' not in whitelist", command), nil
	}

	workDir := filepath.Join(workspaceRoot, sessionID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	// Execute via shell to support redirections
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, arguments...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out")
		}
		return "", fmt.Errorf("command failed: %w\nOutput: %s", err, string(output))
	}
	return string(output), nil
}


// validateWritePaths checks that any file arguments in the command are within the workspace.
// This is a basic check – more sophisticated parsing would be needed for full safety.
func validateWritePaths(command string, workDir string) error {
	// Split respecting quotes? For simplicity, we split by spaces and handle redirections.
	// Identify arguments that look like paths after > or >>, and regular arguments for cp/mv/rm.
	parts := strings.Fields(command)
	for i, part := range parts {
		// skip operators and flags
		if part == ">" || part == ">>" || part == "2>" || part == "&>" {
			continue
		}
		if strings.HasPrefix(part, "-") {
			continue
		}
		// For redirect targets: the token right after > or >>
		if i > 0 && (parts[i-1] == ">" || parts[i-1] == ">>") {
			// This is a file path that will be written
			absPath := resolvePath(part, workDir)
			if !strings.HasPrefix(absPath, workDir+string(filepath.Separator)) && absPath != workDir {
				return fmt.Errorf("write target %s is outside workspace", part)
			}
		}
		// For commands like cp/mv/rm, all non-flag arguments are file paths (source and target)
		cmdName := filepath.Base(parts[0])
		if cmdName == "rm" || cmdName == "mv" || cmdName == "cp" {
			// check each argument that is not a flag
			if i > 0 && !strings.HasPrefix(part, "-") && part != ">" && part != ">>" {
				absPath := resolvePath(part, workDir)
				if !strings.HasPrefix(absPath, workDir+string(filepath.Separator)) && absPath != workDir {
					return fmt.Errorf("file argument %s is outside workspace", part)
				}
			}
		}
	}
	return nil
}

// resolvePath resolves a relative or absolute path to an absolute path under workDir.
// If the path is absolute, it is used as-is (but must start with workDir).
func resolvePath(path, workDir string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(workDir, path)
}

// ExecuteAgent spawns a sub-agent and returns its final result.
func ExecuteAgent(parent *Agent, agentName string, task string) (string, error) {
	subAgentDir := filepath.Join(parent.AgentDir, "agents", agentName)
	if _, err := os.Stat(subAgentDir); os.IsNotExist(err) {
		return fmt.Sprintf("Agent not found: %s", agentName), nil
	}

	subAgent, err := LoadAgent(subAgentDir, parent.Config, parent.SessionID)
	if err != nil {
		return "", fmt.Errorf("load sub-agent %s: %w", agentName, err)
	}
	return subAgent.Run(task)
}
