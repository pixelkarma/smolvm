package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

type toolCallResult struct {
	Output string
	NewCwd string
}

func toolDefinitions() []openai.Tool {
	return []openai.Tool{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "bash",
				Description: "Run a bash command in the current working directory.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{"type": "string"},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "change_dir",
				Description: "Change the working directory for future tool calls.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
					"required": []string{"path"},
				},
			},
		},
	}
}

func executeTool(ctx context.Context, workspace, cwd string, call openai.ToolCall) (toolCallResult, error) {
	switch call.Function.Name {
	case "bash":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return toolCallResult{}, err
		}
		return runBash(ctx, cwd, args.Command)
	case "change_dir":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return toolCallResult{}, err
		}
		newCwd, err := resolveDir(workspace, cwd, args.Path)
		if err != nil {
			return toolCallResult{}, err
		}
		return toolCallResult{
			Output: fmt.Sprintf("working directory changed to %s", newCwd),
			NewCwd: newCwd,
		}, nil
	default:
		return toolCallResult{}, fmt.Errorf("unsupported tool: %s", call.Function.Name)
	}
}

func runBash(ctx context.Context, cwd, command string) (toolCallResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	shell, args := detectShell(command)
	cmd := exec.CommandContext(runCtx, shell, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	output := strings.TrimSpace(stdout.String())
	errText := strings.TrimSpace(stderr.String())
	parts := []string{}
	if output != "" {
		parts = append(parts, "stdout:\n"+output)
	}
	if errText != "" {
		parts = append(parts, "stderr:\n"+errText)
	}
	if err != nil {
		if len(parts) == 0 {
			parts = append(parts, err.Error())
		} else {
			parts = append(parts, "exit_error: "+err.Error())
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "(no output)")
	}
	return toolCallResult{Output: truncate(strings.Join(parts, "\n\n"), 12000), NewCwd: cwd}, nil
}

func detectShell(command string) (string, []string) {
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash", []string{"-lc", command}
	}
	return "/bin/sh", []string{"-lc", command}
}

func resolveDir(workspace, cwd, raw string) (string, error) {
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	target = filepath.Clean(target)
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absWorkspace, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace %q", raw, absWorkspace)
	}
	info, err := os.Stat(absTarget)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absTarget)
	}
	return absTarget, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n\n[truncated]"
}
