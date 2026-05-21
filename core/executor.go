package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func ShellExec(commands []ShellCommand, sessionID string, shellConfig ShellConfig, workspace string) (string, []int, error) {
	if err := validateCommands(commands, shellConfig, workspace); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return "", nil, fmt.Errorf("create workspace: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exec_cmds := []*exec.Cmd{}
	var lastStdout io.ReadCloser
	var finalOutput bytes.Buffer
	for i, cmd := range commands {
		var exec_cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			args := append([]string{"/C", cmd.Command}, cmd.Arguments...)
			exec_cmd = exec.CommandContext(ctx, "cmd", args...)
		} else {
			exec_cmd = exec.CommandContext(ctx, cmd.Command, cmd.Arguments...)
		}
		exec_cmd.Dir = workspace
		if lastStdout != nil {
			exec_cmd.Stdin = lastStdout
		}
		stdout, err := exec_cmd.StdoutPipe()
		if err != nil {
			return "", nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		if cmd.Redirection.StdOut.ToStdErr {
			exec_cmd.Stdout = exec_cmd.Stderr
		} else if cmd.Redirection.StdOut.File != "" {
			fileMode := os.O_CREATE | os.O_WRONLY
			if cmd.Redirection.StdOut.Append {
				fileMode |= os.O_APPEND
			} else {
				fileMode |= os.O_TRUNC
			}
			stdoutFile, err := os.OpenFile(filepath.Join(workspace, cmd.Redirection.StdOut.File), fileMode, 0644)
			if err != nil {
				return "", nil, fmt.Errorf("failed to create stdout file: %w", err)
			}
			exec_cmd.Stdout = stdoutFile
		} else if i == len(commands)-1 {
			exec_cmd.Stdout = &finalOutput
		}
		if cmd.Redirection.StdErr.ToStdOut {
			exec_cmd.Stderr = exec_cmd.Stdout
		} else if cmd.Redirection.StdErr.File != "" {
			fileMode := os.O_CREATE | os.O_WRONLY
			if cmd.Redirection.StdErr.Append {
				fileMode |= os.O_APPEND
			} else {
				fileMode |= os.O_TRUNC
			}
			stderrFile, err := os.OpenFile(filepath.Join(workspace, cmd.Redirection.StdErr.File), fileMode, 0644)
			if err != nil {
				return "", nil, fmt.Errorf("failed to create stderr file: %w", err)
			}
			exec_cmd.Stderr = stderrFile
		} else if i == len(commands)-1 {
			exec_cmd.Stderr = &finalOutput
		}
		exec_cmds = append(exec_cmds, exec_cmd)
		lastStdout = stdout
	}
	for _, exec_cmd := range exec_cmds {
		if err := exec_cmd.Start(); err != nil {
			return "", nil, fmt.Errorf("failed to start command: %w", err)
		}
	}
	for _, exec_cmd := range exec_cmds {
		if err := exec_cmd.Wait(); err != nil {
			return "", nil, fmt.Errorf("command execution failed: %w", err)
		}
	}
	exitCodes := []int{}
	for _, exec_cmd := range exec_cmds {
		if exec_cmd.ProcessState != nil {
			exitCodes = append(exitCodes, exec_cmd.ProcessState.ExitCode())
		} else {
			exitCodes = append(exitCodes, -1) // Unknown exit code
		}
	}
	return finalOutput.String(), exitCodes, nil
}

func validateCommands(commands []ShellCommand, shellConfig ShellConfig, workspace string) error {
	allowedCommands := map[string]bool{}
	for _, command := range shellConfig.Commands {
		allowedCommands[command] = true
	}
	for index, command := range commands {
		if _, ok := allowedCommands[command.Command]; !ok {
			return fmt.Errorf("Rejected: command '%s' not allowed", command.Command)
		}
		if pathLocation, ok := shellConfig.PathLocation[command.Command]; ok {
			if len(pathLocation.Position) > 0 {
				for _, argIndex := range pathLocation.Position {
					if argIndex >= uint(len(command.Arguments)) {
						continue
					}
					argPath := command.Arguments[argIndex]
					if err := validatePath(argPath, workspace); err != nil {
						return fmt.Errorf("Rejected: argument '%s' is not a valid path within the workspace", argPath)
					}
				}
			}
			if len(pathLocation.After) > 0 {
				for _, previousArgument := range pathLocation.After {
					for i, arg := range command.Arguments {
						if arg == previousArgument && i+1 < len(command.Arguments) {
							if err := validatePath(command.Arguments[i+1], workspace); err != nil {
								return fmt.Errorf("Rejected: argument '%s' is not a valid path within the workspace", command.Arguments[i+1])
							}
						}
					}
				}
			}
			if len(pathLocation.Prefix) > 0 {
				for _, prefix := range pathLocation.Prefix {
					for _, arg := range command.Arguments {
						if strings.HasPrefix(arg, prefix) {
							if err := validatePath(arg[len(prefix):], workspace); err != nil {
								return fmt.Errorf("Rejected: argument '%s' is not a valid path within the workspace", arg)
							}
						}
					}
				}
			}
		}
		if command.Redirection.StdOut.ToStdErr && command.Redirection.StdErr.ToStdOut {
			return fmt.Errorf("Rejected: stdout and stderr cannot be redirected to each other at the same time")
		}
		if command.Redirection.StdOut.ToStdErr && command.Redirection.StdOut.File != "" {
			return fmt.Errorf("Rejected: stdout cannot be redirected to both a file and stderr at the same time")
		}
		if command.Redirection.StdErr.ToStdOut && command.Redirection.StdErr.File != "" {
			return fmt.Errorf("Rejected: stderr cannot be redirected to both a file and stdout at the same time")
		}
		if command.Redirection.StdOut.File != "" && validatePath(command.Redirection.StdOut.File, workspace) != nil {
			return fmt.Errorf("Rejected: stdout file '%s' is not a valid path within the workspace", command.Redirection.StdOut.File)
		}
		if command.Redirection.StdErr.File != "" && validatePath(command.Redirection.StdErr.File, workspace) != nil {
			return fmt.Errorf("Rejected: stderr file '%s' is not a valid path within the workspace", command.Redirection.StdErr.File)
		}
		if index != len(commands)-1 && (command.Redirection.StdOut.File != "" || command.Redirection.StdOut.ToStdErr) {
			return fmt.Errorf("Rejected: only the last command in the pipeline can redirect stdout")
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
