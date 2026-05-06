package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (a *App) ensureBaseImage() error {
	for _, path := range []string{
		a.cfg.QEMUBinary,
		a.cfg.TemplateImagePath,
		a.cfg.GuestSSHKeyPath,
	} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("required asset not found at %s", path)
		}
	}
	return nil
}

func (a *App) startInstance(inst Instance, settings Settings) error {
	rt := a.runtimeFor(inst)
	if err := os.MkdirAll(rt.InstanceDir, 0o755); err != nil {
		return err
	}
	if err := stopInstanceRuntime(rt, a.cfg); err != nil {
		return err
	}
	if err := prepareGuestDisk(rt, a.cfg); err != nil {
		return err
	}
	if err := launchQEMU(rt, inst, a.cfg); err != nil {
		return err
	}
	if err := waitForSSHBanner("127.0.0.1", rt.SSHPort, 90*time.Second); err != nil {
		_ = stopInstanceRuntime(rt, a.cfg)
		return fmt.Errorf("guest ssh did not become reachable: %w", err)
	}
	if err := provisionGuest(rt, inst, settings, a.cfg); err != nil {
		_ = stopInstanceRuntime(rt, a.cfg)
		return fmt.Errorf("guest provisioning failed: %w", err)
	}
	if err := waitForHTTP("127.0.0.1", inst.ShelleyPort, 120*time.Second); err != nil {
		_ = stopInstanceRuntime(rt, a.cfg)
		return fmt.Errorf("agent did not become reachable: %w", err)
	}
	return nil
}

func (a *App) stopInstance(inst Instance) error {
	return stopInstanceRuntime(a.runtimeFor(inst), a.cfg)
}

func (a *App) deleteInstance(inst Instance) error {
	rt := a.runtimeFor(inst)
	_ = stopInstanceRuntime(rt, a.cfg)
	if err := os.RemoveAll(rt.InstanceDir); err != nil {
		return err
	}
	return deleteInstanceRecord(a.db, inst.ID)
}

func (a *App) instanceStatus(inst Instance) string {
	pid, _, err := findQEMUProcess(a.runtimeFor(inst), a.cfg)
	if err == nil && pid > 0 {
		return "running"
	}
	return "stopped"
}

func instanceLogs(inst Instance, rt InstanceRuntime) (string, error) {
	data, err := os.ReadFile(rt.SerialLogPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func prepareGuestDisk(rt InstanceRuntime, cfg Config) error {
	if _, err := os.Stat(rt.DiskImagePath); os.IsNotExist(err) {
		return copyFile(cfg.TemplateImagePath, rt.DiskImagePath, 0o644)
	}
	return nil
}

func provisionGuest(rt InstanceRuntime, inst Instance, settings Settings, cfg Config) error {
	stageDir := filepath.Join(rt.InstanceDir, "staging")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
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
	cfgData, err := jsonMarshalIndent(agentCfg)
	if err != nil {
		return err
	}

	globalPromptPath := filepath.Join(stageDir, "global-AGENTS.md")
	workspacePromptPath := filepath.Join(stageDir, "workspace-AGENTS.md")
	configPath := filepath.Join(stageDir, "smolvm.config.json")
	provisionPath := filepath.Join(stageDir, "provision.sh")

	if err := os.WriteFile(configPath, cfgData, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(globalPromptPath, []byte(settings.GlobalPrompt+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(workspacePromptPath, []byte(inst.InitialPrompt+"\n"), 0o644); err != nil {
		return err
	}

	provisionScript := fmt.Sprintf(`#!/bin/sh
set -eu
mkdir -p /root/.smolvm /workspace /var/lib/smolagent
install -m 600 /tmp/smolvm.config.json /root/.smolvm/smolvm.config.json
install -m 644 /tmp/global-AGENTS.md /root/.smolvm/AGENTS.md
install -m 644 /tmp/workspace-AGENTS.md /workspace/AGENTS.md
printf 'PROJECT_WEB_PORT=%d\n' > /root/.smolvm/instance.env
printf '%s\n' > /etc/hostname
hostname '%s' || true
rc-service smolagentd restart
`, inst.WebPort, inst.Slug, shellSingleQuote(inst.Slug))
	if err := os.WriteFile(provisionPath, []byte(provisionScript), 0o755); err != nil {
		return err
	}

	for _, localPath := range []string{configPath, globalPromptPath, workspacePromptPath, provisionPath} {
		if err := scpToGuest(cfg, rt, localPath, "/tmp/"+filepath.Base(localPath)); err != nil {
			return err
		}
	}
	if err := runGuestCommand(cfg, rt, "sh /tmp/provision.sh"); err != nil {
		return err
	}
	return nil
}

func launchQEMU(rt InstanceRuntime, inst Instance, cfg Config) error {
	netdev := fmt.Sprintf(
		"user,id=net0,hostfwd=tcp::%d-:22,hostfwd=tcp:127.0.0.1:%d-:9000,hostfwd=tcp::%d-:80",
		rt.SSHPort,
		inst.ShelleyPort,
		inst.WebPort,
	)

	args := []string{
		"-m", strconv.Itoa(inst.MemoryMB),
		"-smp", strconv.Itoa(inst.CPUCount),
		"-drive", "file=" + rt.DiskImagePath + ",format=qcow2,if=virtio",
		"-netdev", netdev,
		"-device", "virtio-net,netdev=net0",
		"-display", "none",
		"-serial", "none",
		"-daemonize",
	}

	cmd := exec.Command(cfg.QEMUBinary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(rt.PIDPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return nil
}

func stopInstanceRuntime(rt InstanceRuntime, cfg Config) error {
	pid, _, err := findQEMUProcess(rt, cfg)
	if err == nil && pid > 0 {
		if proc, findErr := os.FindProcess(pid); findErr == nil {
			_ = proc.Signal(syscall.SIGTERM)
			time.Sleep(500 * time.Millisecond)
			_ = proc.Signal(syscall.SIGKILL)
		}
	}
	_ = os.Remove(rt.PIDPath)
	return nil
}

func waitForSSHBanner(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			buf := make([]byte, 64)
			n, readErr := conn.Read(buf)
			_ = conn.Close()
			if readErr == nil && strings.HasPrefix(string(buf[:n]), "SSH-") {
				return nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("ssh banner did not appear on %s", addr)
}

func waitForHTTP(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://%s:%d/", host, port)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("http service did not respond on %s", url)
}

func findQEMUProcess(rt InstanceRuntime, cfg Config) (int, string, error) {
	cmd := exec.Command("ps", "-ax", "-o", "pid=", "-o", "command=")
	out, err := cmd.Output()
	if err != nil {
		return 0, "", err
	}
	qemuName := filepath.Base(cfg.QEMUBinary)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		command := strings.TrimSpace(line[len(parts[0]):])
		if !strings.Contains(command, qemuName) {
			continue
		}
		if strings.Contains(command, rt.MachineName) && strings.Contains(command, rt.DiskImagePath) {
			return pid, strings.TrimSpace(command), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, "", err
	}
	return 0, "", fmt.Errorf("qemu process not found")
}

func guestSSHArgs(cfg Config, rt InstanceRuntime) []string {
	return []string{
		"-i", cfg.GuestSSHKeyPath,
		"-p", strconv.Itoa(rt.SSHPort),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"root@127.0.0.1",
	}
}

func scpToGuest(cfg Config, rt InstanceRuntime, localPath, remotePath string) error {
	args := []string{
		"-i", cfg.GuestSSHKeyPath,
		"-P", strconv.Itoa(rt.SSHPort),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		localPath,
		"root@127.0.0.1:" + remotePath,
	}
	return runCommand("", "scp", args...)
}

func runGuestCommand(cfg Config, rt InstanceRuntime, command string) error {
	args := append(guestSSHArgs(cfg, rt), command)
	return runCommand("", "ssh", args...)
}

func runCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		return fmt.Errorf("%s %s failed: %v: %s%s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()), strings.TrimSpace(string(out)))
	}
	return nil
}

func jsonMarshalIndent(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func shellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'"'"'`)
}
