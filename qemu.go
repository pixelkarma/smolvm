package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func (a *App) startInstance(inst Instance) error {
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
	return launchQEMU(rt, inst, a.cfg)
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
	pid, _, err := findQEMUProcess(a.runtimeFor(inst), inst, a.cfg)
	if err == nil && pid > 0 {
		return "running"
	}
	return "stopped"
}

func (a *App) instanceUptime(inst Instance) (string, error) {
	pid, _, err := findQEMUProcess(a.runtimeFor(inst), inst, a.cfg)
	if err != nil || pid <= 0 {
		return "stopped", nil
	}
	cmd := exec.Command("ps", "-o", "etime=", "-p", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	uptime := strings.TrimSpace(string(out))
	if uptime == "" {
		return "running", nil
	}
	return uptime, nil
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

func launchQEMU(rt InstanceRuntime, inst Instance, cfg Config) error {
	_ = os.Remove(rt.QMPPath)

	netdev := fmt.Sprintf(
		"user,id=net0,hostfwd=tcp::%d-:22,hostfwd=tcp:127.0.0.1:%d-:9000,hostfwd=tcp::%d-:80",
		rt.SSHPort,
		inst.ShelleyPort,
		inst.WebPort,
	)

	args := []string{
		"-m", strconv.Itoa(inst.MemoryMB),
		"-smp", strconv.Itoa(inst.CPUCount),
		"-smbios", fmt.Sprintf("type=1,serial=smolvm-instance:%d", inst.ID),
		"-qmp", "unix:" + rt.QMPPath + ",server,nowait",
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
	pid, _, err := findQEMUProcess(rt, Instance{}, cfg)
	if err == nil && pid > 0 {
		if proc, findErr := os.FindProcess(pid); findErr == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
	_ = os.Remove(rt.PIDPath)
	_ = os.Remove(rt.QMPPath)
	return nil
}

func findQEMUProcess(rt InstanceRuntime, inst Instance, cfg Config) (int, string, error) {
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
		if !strings.Contains(command, rt.DiskImagePath) {
			continue
		}
		if inst.ID == 0 {
			return pid, strings.TrimSpace(command), nil
		}
		sshForward := fmt.Sprintf("hostfwd=tcp::%d-:22", rt.SSHPort)
		agentForward := fmt.Sprintf("hostfwd=tcp:127.0.0.1:%d-:9000", inst.ShelleyPort)
		webForward := fmt.Sprintf("hostfwd=tcp::%d-:80", inst.WebPort)
		if strings.Contains(command, sshForward) &&
			strings.Contains(command, agentForward) &&
			strings.Contains(command, webForward) {
			return pid, strings.TrimSpace(command), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, "", err
	}
	return 0, "", fmt.Errorf("qemu process not found")
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
