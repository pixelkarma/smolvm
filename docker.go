package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) ensureBaseImage() error {
	if _, err := os.Stat(a.cfg.AgentBinaryPath); err != nil {
		return fmt.Errorf("smolagent binary not found at %s", a.cfg.AgentBinaryPath)
	}
	buildDir := filepath.Join(a.cfg.DataDir, "image-build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return err
	}
	dockerfile := `FROM alpine:3.23
RUN apk add --no-cache bash ca-certificates curl git ripgrep tini
COPY smolagent /usr/local/bin/smolagent
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /usr/local/bin/smolagent /entrypoint.sh && mkdir -p /var/lib/smolagent /workspace /root/.smolvm
WORKDIR /workspace
EXPOSE 9000
ENTRYPOINT ["/sbin/tini","--","/entrypoint.sh"]`
	entrypoint := `#!/bin/sh
set -eu
exec /usr/local/bin/smolagent --config /root/.smolvm/smolvm.config.json`
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(buildDir, "entrypoint.sh"), []byte(entrypoint), 0o755); err != nil {
		return err
	}
	in, err := os.ReadFile(a.cfg.AgentBinaryPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(buildDir, "smolagent"), in, 0o755); err != nil {
		return err
	}
	return runCmd("", "docker", "build", "-t", a.cfg.ImageName, buildDir)
}

func (a *App) startInstance(inst Instance, settings Settings) error {
	rt := a.runtimeFor(inst)
	if err := os.MkdirAll(rt.InstanceDir, 0o755); err != nil {
		return err
	}
	if err := ensureMountedDisk(rt, inst.DiskMB); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.WorkspaceDir, "AGENTS.md"), []byte(inst.InitialPrompt+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(rt.ConfigDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.ConfigDir, "AGENTS.md"), []byte(settings.GlobalPrompt+"\n"), 0o644); err != nil {
		return err
	}
	agentCfg := map[string]any{
		"listen_addr":     ":9000",
		"db_path":         "/var/lib/smolagent/smolagent.db",
		"workspace_dir":   "/workspace",
		"default_model":   "gpt-5.4",
		"required_header": "X-SmolVM-Admin",
		"openai_api_key":  settings.DefaultOpenAIAPIKey,
	}
	if inst.APIKey != "" {
		agentCfg["openai_api_key"] = inst.APIKey
	}
	data, err := json.MarshalIndent(agentCfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(rt.ConfigDir, "smolvm.config.json"), data, 0o600); err != nil {
		return err
	}
	_ = dockerRemove(rt.ContainerName)
	args := []string{
		"run", "-d",
		"--name", rt.ContainerName,
		"--restart", "unless-stopped",
		"--hostname", inst.Slug,
		"--memory", fmt.Sprintf("%dm", inst.MemoryMB),
		"--memory-swap", fmt.Sprintf("%dm", inst.MemoryMB),
		"--cpus", strconv.Itoa(inst.CPUCount),
		"-p", fmt.Sprintf("127.0.0.1:%d:9000", inst.ShelleyPort),
		"-p", fmt.Sprintf("%d:%d", inst.WebPort, inst.WebPort),
		"-e", fmt.Sprintf("PROJECT_WEB_PORT=%d", inst.WebPort),
		"-v", fmt.Sprintf("%s:/var/lib/smolagent", rt.VarLibDir),
		"-v", fmt.Sprintf("%s:/workspace", rt.WorkspaceDir),
		"-v", fmt.Sprintf("%s:/root/.smolvm", rt.ConfigDir),
	}
	args = append(args, a.cfg.ImageName)
	return runCmd("", "docker", args...)
}

func (a *App) deleteInstance(inst Instance) error {
	rt := a.runtimeFor(inst)
	_ = dockerRemove(rt.ContainerName)
	_ = unmount(rt.MountDir)
	if err := os.RemoveAll(rt.InstanceDir); err != nil {
		return err
	}
	return deleteInstanceRecord(a.db, inst.ID)
}

func (a *App) instanceStatus(inst Instance) string {
	out, err := cmdOutput("docker", "inspect", "-f", "{{.State.Status}}", a.runtimeFor(inst).ContainerName)
	if err != nil {
		return "missing"
	}
	return strings.TrimSpace(out)
}

func ensureMountedDisk(rt InstanceRuntime, diskMB int) error {
	if err := os.MkdirAll(rt.MountDir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(rt.DiskImagePath); os.IsNotExist(err) {
		if err := runCmd("", "truncate", "-s", fmt.Sprintf("%dM", diskMB), rt.DiskImagePath); err != nil {
			return err
		}
		if err := runCmd("", "mkfs.ext4", "-F", rt.DiskImagePath); err != nil {
			return err
		}
	}
	mounted, _ := isMountpoint(rt.MountDir)
	if !mounted {
		if err := runCmd("", "mount", "-o", "loop", rt.DiskImagePath, rt.MountDir); err != nil {
			return err
		}
	}
	for _, dir := range []string{rt.VarLibDir, rt.WorkspaceDir, rt.ConfigDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func isMountpoint(path string) (bool, error) {
	err := exec.Command("mountpoint", "-q", path).Run()
	if err == nil {
		return true, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func unmount(path string) error {
	mounted, err := isMountpoint(path)
	if err != nil || !mounted {
		return err
	}
	return runCmd("", "umount", path)
}

func dockerStop(container string) error {
	return runCmd("", "docker", "stop", container)
}

func dockerRemove(container string) error {
	return runCmd("", "docker", "rm", "-f", container)
}

func dockerLogs(container string) (string, error) {
	return cmdOutput("docker", "logs", "--tail", "200", container)
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		return fmt.Errorf("%s %s failed: %v: %s%s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()), strings.TrimSpace(string(out)))
	}
	return nil
}

func cmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
