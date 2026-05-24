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

type shellResult struct {
	StdOut   string `json:"stdout"`
	StdErr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func shellExec(commands []shellCommand, shellConfig ShellConfig, workspace string) ([]shellResult, error) {
	if err := validateCommands(commands, shellConfig, workspace); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	execCommands := make([]*exec.Cmd, len(commands))
	stdOutWriters := make([]*bytes.Buffer, len(commands))
	stdErrWriters := make([]*bytes.Buffer, len(commands))
	shellResults := make([]shellResult, len(commands))
	var lastStdout io.ReadCloser
	for i, cmd := range commands {
		var execCmd *exec.Cmd
		if runtime.GOOS == "windows" {
			args := append([]string{"/C", cmd.Command}, cmd.Arguments...)
			execCmd = exec.CommandContext(ctx, "cmd", args...)
		} else {
			execCmd = exec.CommandContext(ctx, cmd.Command, cmd.Arguments...)
		}
		execCmd.Dir = workspace
		if lastStdout != nil {
			execCmd.Stdin = lastStdout
		}
		stdout, err := execCmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		if cmd.Redirection.StdOut.ToStdErr {
			execCmd.Stdout = execCmd.Stderr
		} else if cmd.Redirection.StdOut.File != "" {
			fileMode := os.O_CREATE | os.O_WRONLY
			if cmd.Redirection.StdOut.Append {
				fileMode |= os.O_APPEND
			} else {
				fileMode |= os.O_TRUNC
			}
			stdoutFile, err := os.OpenFile(filepath.Join(workspace, cmd.Redirection.StdOut.File), fileMode, 0644)
			if err != nil {
				return nil, fmt.Errorf("failed to create stdout file: %w", err)
			}
			execCmd.Stdout = stdoutFile
		} else if cmd.Redirection.StdOut.ToNext {
			lastStdout = stdout
		} else {
			lastStdout = nil
			stdOutWriters[i] = &bytes.Buffer{}
			execCmd.Stdout = stdOutWriters[i]
		}
		if cmd.Redirection.StdErr.ToStdOut {
			execCmd.Stderr = execCmd.Stdout
		} else if cmd.Redirection.StdErr.File != "" {
			fileMode := os.O_CREATE | os.O_WRONLY
			if cmd.Redirection.StdErr.Append {
				fileMode |= os.O_APPEND
			} else {
				fileMode |= os.O_TRUNC
			}
			stderrFile, err := os.OpenFile(filepath.Join(workspace, cmd.Redirection.StdErr.File), fileMode, 0644)
			if err != nil {
				return nil, fmt.Errorf("failed to create stderr file: %w", err)
			}
			execCmd.Stderr = stderrFile
		} else {
			stdErrWriters[i] = &bytes.Buffer{}
			execCmd.Stderr = stdErrWriters[i]
		}
		execCommands[i] = execCmd
	}
	for i, command := range execCommands {
		if err := command.Start(); err != nil {
			return shellResults, fmt.Errorf("failed to start command '%s', error: %w", commands[i].Command, err)
		}
		if !commands[i].Redirection.StdOut.ToNext {
			if err := command.Wait(); err != nil {
				return shellResults, fmt.Errorf("failed to wait for command '%s', error: %w", commands[i].Command, err)
			}
		}
	}
	for i, command := range execCommands {
		if commands[i].Redirection.StdOut.ToNext {
			if err := command.Wait(); err != nil {
				return shellResults, fmt.Errorf("failed to wait for command '%s', error: %w", commands[i].Command, err)
			}
		}
	}
	for i, execCmd := range execCommands {
		if stdOutWriters[i] != nil {
			shellResults[i].StdOut = stdOutWriters[i].String()
		}
		if stdErrWriters[i] != nil {
			shellResults[i].StdErr = stdErrWriters[i].String()
		}
		if execCmd.ProcessState != nil {
			shellResults[i].ExitCode = execCmd.ProcessState.ExitCode()
		} else {
			shellResults[i].ExitCode = -1 // Unknown exit code
		}
	}
	return shellResults, nil
}

func validateCommands(commands []shellCommand, shellConfig ShellConfig, workspace string) error {
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
		if command.Redirection.StdOut.ToNext && (command.Redirection.StdOut.File != "" || command.Redirection.StdOut.ToStdErr) {
			return fmt.Errorf("Rejected: stdout cannot be redirected to next command and also to a file or stderr at the same time")
		}
		if command.Redirection.StdOut.ToNext && index == len(commands)-1 {
			return fmt.Errorf("Rejected: stdout cannot be redirected to next command when it is the last command in the pipeline")
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
