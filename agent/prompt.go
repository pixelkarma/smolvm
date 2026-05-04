package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func BuildSystemPrompt(cfg Config, cwd string) string {
	var sections []string
	sections = append(sections, strings.TrimSpace(`You are smolagent, a coding agent running inside a managed smolvm Firecracker microVM.
Work directly in the provided Linux environment.
Keep responses concise and execution-focused.
Use tools when you need to inspect files, run commands, or change directories.`))

	if cfg.GlobalPrompt != "" {
		sections = append(sections, "Global guidance:\n"+strings.TrimSpace(cfg.GlobalPrompt))
	}

	if text := readPromptFile("/root/.smolvm/AGENTS.md"); text != "" {
		sections = append(sections, "VM guidance:\n"+text)
	}
	if text := readPromptFile(filepath.Join(cfg.WorkspaceDir, "AGENTS.md")); text != "" {
		sections = append(sections, "Workspace guidance:\n"+text)
	}

	if webPort := os.Getenv("PROJECT_WEB_PORT"); webPort != "" {
		sections = append(sections, fmt.Sprintf("The project web server should bind to 0.0.0.0:%s.", webPort))
	}

	sections = append(sections, fmt.Sprintf("Current working directory: %s", cwd))
	sections = append(sections, strings.TrimSpace(`Available tools:
- bash: run a shell command in the current working directory
- change_dir: update the current working directory for future commands

When editing files, prefer straightforward shell commands and keep changes minimal.
If a bash command fails, inspect the output and adjust.`))

	return strings.Join(sections, "\n\n")
}

func readPromptFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
