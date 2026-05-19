package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ExecuteShell runs a shell command after validating it against the whitelist.
// Write operations are restricted to the session workspace.
func ExecuteShell(commands []ShellCommand, sessionID string, commandConfig map[string]CommandConfig, workspace string) (string, error) {
	if err := validateCommands(commands, commandConfig, workspace); err != nil {
		return "", err
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	// Execute via shell to support redirections
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exec_cmds := []*exec.Cmd{}
	var lastStdout io.ReadCloser
	for _, cmd := range commands {
		exec_cmd := exec.CommandContext(ctx, cmd.Command, cmd.Arguments...)
		exec_cmd.Dir = workspace
		if lastStdout != nil {
			exec_cmd.Stdin = lastStdout
		}
		stdout, err := exec_cmd.StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		if cmd.StderrToStdout {
			exec_cmd.Stderr = exec_cmd.Stdout
		}
		if cmd.StderrToFile != "" {
			stderrFile, err := os.Create(filepath.Join(workspace, cmd.StderrToFile))
			if err != nil {
				return "", fmt.Errorf("failed to create stderr file: %w", err)
			}
			exec_cmd.Stderr = stderrFile
		}
		if cmd.StdoutToFile != "" {
			stdoutFile, err := os.Create(filepath.Join(workspace, cmd.StdoutToFile))
			if err != nil {
				return "", fmt.Errorf("failed to create stdout file: %w", err)
			}
			exec_cmd.Stdout = stdoutFile
		}
		exec_cmds = append(exec_cmds, exec_cmd)
		lastStdout = stdout
	}
	for _, exec_cmd := range exec_cmds {
		if err := exec_cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start command: %w", err)
		}
	}
	for _, exec_cmd := range exec_cmds {
		if err := exec_cmd.Wait(); err != nil {
			return "", fmt.Errorf("command execution failed: %w", err)
		}
	}
	output, err := exec_cmds[len(exec_cmds)-1].CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out")
		}
		return "", fmt.Errorf("command failed: %w\nOutput: %s", err, string(output))
	}
	return string(output), nil
}

func validateCommands(commands []ShellCommand, commandConfig map[string]CommandConfig, workspace string) error {
	for index, command := range commands {
		cfg, ok := commandConfig[command.Command]
		if !ok || !cfg.Enabled {
			return fmt.Errorf("Rejected: command '%s' not in whitelist", command.Command)
		}
		if len(cfg.PathPos) > 0 {
			for _, argIndex := range cfg.PathPos {
				if argIndex >= uint(len(command.Arguments)) {
					continue
				}
				argPath := command.Arguments[argIndex]
				if err := validatePath(argPath, workspace); err != nil {
					return fmt.Errorf("Rejected: argument '%s' is not a valid path within the workspace", argPath)
				}
			}
		}
		if len(cfg.PathAfter) > 0 {
			for _, previousArgument := range cfg.PathAfter {
				for i, arg := range command.Arguments {
					if arg == previousArgument && i+1 < len(command.Arguments) {
						if err := validatePath(command.Arguments[i+1], workspace); err != nil {
							return fmt.Errorf("Rejected: argument '%s' is not a valid path within the workspace", command.Arguments[i+1])
						}
					}
				}
			}
		}
		if len(cfg.PathPrefix) > 0 {
			for _, prefix := range cfg.PathPrefix {
				for _, arg := range command.Arguments {
					if strings.HasPrefix(arg, prefix) {
						if err := validatePath(arg[len(prefix):], workspace); err != nil {
							return fmt.Errorf("Rejected: argument '%s' is not a valid path within the workspace", arg)
						}
					}
				}
			}
		}
		if command.StdoutToFile != "" && validatePath(command.StdoutToFile, workspace) != nil {
			return fmt.Errorf("Rejected: stdout file '%s' is not a valid path within the workspace", command.StdoutToFile)
		}
		if command.StderrToFile != "" && validatePath(command.StderrToFile, workspace) != nil {
			return fmt.Errorf("Rejected: stderr file '%s' is not a valid path within the workspace", command.StderrToFile)
		}
		if index != len(commands)-1 && command.StdoutToFile != "" {
			return fmt.Errorf("Rejected: only the last command in the pipeline can have output redirection")
		}
	}
	return nil
}

func validatePath(path string, workspace string) error {
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %v", err)
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("invalid workspace path: %v", err)
	}
	if !strings.HasPrefix(absPath, workspaceAbs) {
		return fmt.Errorf("path outside workspace")
	}
	return nil
}
