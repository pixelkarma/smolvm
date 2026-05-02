package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) ensureBaseImage() error {
	if _, err := os.Stat(a.cfg.ShelleyBinaryPath); err != nil {
		return fmt.Errorf("shelley binary not found at %s", a.cfg.ShelleyBinaryPath)
	}
	buildDir := filepath.Join(a.cfg.DataDir, "image-build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return err
	}
	dockerfile := `FROM alpine:3.23
RUN apk add --no-cache bash ca-certificates curl git ripgrep tini
COPY shelley /usr/local/bin/shelley
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /usr/local/bin/shelley /entrypoint.sh && mkdir -p /var/lib/shelley /workspace /root/.config/shelley
WORKDIR /workspace
EXPOSE 9000
ENTRYPOINT ["/sbin/tini","--","/entrypoint.sh"]`
	entrypoint := `#!/bin/sh
set -eu
KEY_FILE="${OPENAI_KEY_FILE:-/run/secrets/openai}"
if [ -z "${OPENAI_API_KEY:-}" ] && [ -f "$KEY_FILE" ]; then
  first_line=$(head -n 1 "$KEY_FILE" | tr -d '\r\n')
  case "$first_line" in
    OPENAI_API_KEY=*) export "$first_line" ;;
    *) export OPENAI_API_KEY="$first_line" ;;
  esac
fi
exec /usr/local/bin/shelley --db /var/lib/shelley/shelley.db --default-model gpt-5.4 serve -port 9000 --require-header X-SmolVM-Admin`
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(buildDir, "entrypoint.sh"), []byte(entrypoint), 0o755); err != nil {
		return err
	}
	in, err := os.ReadFile(a.cfg.ShelleyBinaryPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(buildDir, "shelley"), in, 0o755); err != nil {
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
	if err := os.MkdirAll(filepath.Join(rt.RootConfigDir, "shelley"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.RootConfigDir, "shelley", "AGENTS.md"), []byte(settings.GlobalPrompt+"\n"), 0o644); err != nil {
		return err
	}
	_ = dockerRemove(rt.ContainerName)
	keyPath := inst.APIKeyPath
	if keyPath == "" {
		keyPath = settings.SystemKeyPath
	}
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
		"-v", fmt.Sprintf("%s:/var/lib/shelley", rt.VarLibDir),
		"-v", fmt.Sprintf("%s:/workspace", rt.WorkspaceDir),
		"-v", fmt.Sprintf("%s:/root/.config", rt.RootConfigDir),
	}
	if keyPath != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/run/secrets/openai:ro", keyPath))
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
	for _, dir := range []string{rt.VarLibDir, rt.WorkspaceDir, rt.RootConfigDir} {
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
