package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (a *App) ensureBaseImage() error {
	for _, path := range []string{
		a.cfg.AgentBinaryPath,
		a.cfg.QEMUBinary,
		a.cfg.KernelImagePath,
		a.cfg.InitramfsPath,
		a.cfg.TemplateImagePath,
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
	if err := stopInstanceRuntime(rt); err != nil {
		return err
	}
	if err := prepareGuestDisk(rt, inst, settings, a.cfg); err != nil {
		return err
	}
	if err := launchQEMU(rt, inst, a.cfg); err != nil {
		return err
	}
	if err := waitForSSHBanner("127.0.0.1", rt.SSHPort, 60*time.Second); err != nil {
		_ = stopInstanceRuntime(rt)
		return fmt.Errorf("guest ssh did not become reachable: %w", err)
	}
	if err := waitForHTTP("127.0.0.1", inst.ShelleyPort, 120*time.Second); err != nil {
		_ = stopInstanceRuntime(rt)
		return fmt.Errorf("agent did not become reachable: %w", err)
	}
	return nil
}

func (a *App) stopInstance(inst Instance) error {
	return stopInstanceRuntime(a.runtimeFor(inst))
}

func (a *App) deleteInstance(inst Instance) error {
	rt := a.runtimeFor(inst)
	_ = stopInstanceRuntime(rt)
	if err := os.RemoveAll(rt.InstanceDir); err != nil {
		return err
	}
	return deleteInstanceRecord(a.db, inst.ID)
}

func (a *App) instanceStatus(inst Instance) string {
	if runningPID(a.runtimeFor(inst).PIDPath) {
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

func prepareGuestDisk(rt InstanceRuntime, inst Instance, settings Settings, cfg Config) error {
	if _, err := os.Stat(rt.DiskImagePath); os.IsNotExist(err) {
		if err := copyFile(cfg.TemplateImagePath, rt.DiskImagePath, 0o644); err != nil {
			return err
		}
	}
	if err := ensureMountedDisk(rt); err != nil {
		return err
	}
	defer func() {
		_ = unmount(rt.MountDir)
	}()
	for _, dir := range []string{
		filepath.Join(rt.MountDir, "root", ".smolvm"),
		filepath.Join(rt.MountDir, "root", ".ssh"),
		filepath.Join(rt.MountDir, "workspace"),
		filepath.Join(rt.MountDir, "var", "lib", "smolagent"),
		filepath.Join(rt.MountDir, "etc", "conf.d"),
		filepath.Join(rt.MountDir, "usr", "local", "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := copyFile(cfg.AgentBinaryPath, filepath.Join(rt.MountDir, "usr", "local", "bin", "smolagent"), 0o755); err != nil {
		return err
	}
	return writeGuestFiles(rt, inst, settings, cfg)
}

func writeGuestFiles(rt InstanceRuntime, inst Instance, settings Settings, cfg Config) error {
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
	data, err := jsonMarshalIndent(agentCfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.MountDir, "root", ".smolvm", "smolvm.config.json"), data, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.MountDir, "root", ".smolvm", "AGENTS.md"), []byte(settings.GlobalPrompt+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.MountDir, "workspace", "AGENTS.md"), []byte(inst.InitialPrompt+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.MountDir, "root", ".smolvm", "instance.env"), []byte("PROJECT_WEB_PORT="+strconv.Itoa(inst.WebPort)+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rt.MountDir, "etc", "hostname"), []byte(inst.Slug+"\n"), 0o644); err != nil {
		return err
	}
	hosts := "127.0.0.1 localhost\n10.0.2.15 " + inst.Slug + "\n"
	if err := os.WriteFile(filepath.Join(rt.MountDir, "etc", "hosts"), []byte(hosts), 0o644); err != nil {
		return err
	}
	if err := writeResolvConf(rt.MountDir); err != nil {
		return err
	}
	pubKey, err := ensureGuestDebugKey(filepath.Dir(cfg.AgentBinaryPath))
	if err != nil {
		return err
	}
	return writeAuthorizedKey(rt.MountDir, pubKey)
}

func launchQEMU(rt InstanceRuntime, inst Instance, cfg Config) error {
	logFile, err := os.OpenFile(rt.SerialLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}

	machineArg := "-machine"
	machineValue := "q35,accel=tcg"
	if runtime.GOOS == "linux" {
		machineValue = "q35,accel=kvm:tcg"
	}
	if runtime.GOOS == "darwin" {
		machineValue = "q35,accel=hvf:tcg"
	}

	netdev := fmt.Sprintf(
		"user,id=net0,hostfwd=tcp:127.0.0.1:%d-:22,hostfwd=tcp:127.0.0.1:%d-:9000,hostfwd=tcp:0.0.0.0:%d-:%d",
		rt.SSHPort,
		inst.ShelleyPort,
		inst.WebPort,
		inst.WebPort,
	)
	args := []string{
		machineArg, machineValue,
		"-nodefaults",
		"-no-user-config",
		"-no-reboot",
		"-display", "none",
		"-serial", "stdio",
		"-monitor", "none",
		"-m", strconv.Itoa(inst.MemoryMB),
		"-smp", strconv.Itoa(inst.CPUCount),
		"-kernel", cfg.KernelImagePath,
		"-initrd", cfg.InitramfsPath,
		"-append", "console=ttyS0 reboot=k panic=1 root=/dev/vda1 rootfstype=ext4 rootwait rw",
		"-drive", "if=none,id=rootfs,format=raw,file=" + rt.DiskImagePath,
		"-netdev", netdev,
		"-device", "virtio-blk-pci,drive=rootfs",
		"-device", "virtio-net-pci,netdev=net0,mac=" + rt.GuestMAC,
		"-device", "virtio-rng-pci",
	}
	cmd := exec.Command(cfg.QEMUBinary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	if err := os.WriteFile(rt.PIDPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	return nil
}

func stopInstanceRuntime(rt InstanceRuntime) error {
	_ = stopPIDFile(rt.PIDPath)
	_ = unmount(rt.MountDir)
	return nil
}

func ensureMountedDisk(rt InstanceRuntime) error {
	if err := os.MkdirAll(rt.MountDir, 0o755); err != nil {
		return err
	}
	mounted, _ := isMountpoint(rt.MountDir)
	if !mounted {
		if err := runCmd("", "mount", "-o", "loop,offset=1048576", rt.DiskImagePath, rt.MountDir); err != nil {
			return err
		}
	}
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

func stopPIDFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err == nil && pid > 0 {
		proc, findErr := os.FindProcess(pid)
		if findErr == nil {
			_ = proc.Signal(syscall.SIGTERM)
			time.Sleep(300 * time.Millisecond)
			_ = proc.Signal(syscall.SIGKILL)
		}
	}
	return os.Remove(path)
}

func runningPID(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	parts := strings.Fields(string(stat))
	return len(parts) > 2 && parts[2] != "Z"
}

func jsonMarshalIndent(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeResolvConf(root string) error {
	src := "/etc/resolv.conf"
	dest := filepath.Join(root, "etc", "resolv.conf")
	if _, err := os.Stat(src); err == nil {
		return copyFile(src, dest, 0o644)
	}
	return os.WriteFile(dest, []byte("nameserver 1.1.1.1\nnameserver 8.8.8.8\n"), 0o644)
}

func writeAuthorizedKey(root, pubKey string) error {
	sshDir := filepath.Join(root, "root", ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sshDir, "authorized_keys"), []byte(strings.TrimSpace(pubKey)+"\n"), 0o600); err != nil {
		return err
	}
	return nil
}

func ensureGuestDebugKey(hostDir string) (string, error) {
	keyPath := filepath.Join(hostDir, "guest-debug-key")
	pubPath := keyPath + ".pub"
	if _, err := os.Stat(pubPath); err == nil {
		data, readErr := os.ReadFile(pubPath)
		return string(data), readErr
	}
	if err := runCmd("", "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath); err != nil {
		return "", err
	}
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
