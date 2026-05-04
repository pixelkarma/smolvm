package main

import (
	"bytes"
	"context"
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
		a.cfg.AgentBinaryPath,
		a.cfg.FirecrackerBinary,
		a.cfg.KernelImagePath,
		a.cfg.TemplateImagePath,
	} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("required asset not found at %s", path)
		}
	}
	return ensureHostNetwork(a.cfg)
}

func (a *App) startInstance(inst Instance, settings Settings) error {
	rt := a.runtimeFor(inst)
	if err := os.MkdirAll(rt.InstanceDir, 0o755); err != nil {
		return err
	}
	if err := ensureHostNetwork(a.cfg); err != nil {
		return err
	}
	if err := stopInstanceRuntime(rt, a.cfg); err != nil {
		return err
	}
	if err := setupTap(rt.TapName, rt.HostCIDR); err != nil {
		return err
	}
	if err := ensureNATRule(rt.SubnetCIDR, a.cfg.OutboundInterface); err != nil {
		_ = teardownTap(rt.TapName)
		return err
	}
	if err := prepareGuestDisk(rt, inst, settings, a.cfg); err != nil {
		_ = teardownTap(rt.TapName)
		_ = deleteNATRule(rt.SubnetCIDR, a.cfg.OutboundInterface)
		return err
	}
	if err := launchFirecracker(rt, inst, a.cfg); err != nil {
		_ = teardownTap(rt.TapName)
		_ = deleteNATRule(rt.SubnetCIDR, a.cfg.OutboundInterface)
		return err
	}
	if err := startForwarders(rt, inst); err != nil {
		_ = stopInstanceRuntime(rt, a.cfg)
		return err
	}
	if err := waitForPort("127.0.0.1", rt.SSHPort, 60*time.Second); err != nil {
		_ = stopInstanceRuntime(rt, a.cfg)
		return fmt.Errorf("guest ssh forward did not become reachable: %w", err)
	}
	if err := waitForPort("127.0.0.1", inst.ShelleyPort, 120*time.Second); err != nil {
		_ = stopInstanceRuntime(rt, a.cfg)
		return fmt.Errorf("agent forward did not become reachable: %w", err)
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
	data, err := json.MarshalIndent(agentCfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
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
	hosts := "127.0.0.1 localhost\n" + rt.GuestIP + " " + inst.Slug + "\n"
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

func launchFirecracker(rt InstanceRuntime, inst Instance, cfg Config) error {
	_ = os.Remove(rt.SocketPath)
	logFile, err := os.OpenFile(rt.SerialLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(cfg.FirecrackerBinary, "--api-sock", rt.SocketPath)
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
	if err := waitForSocket(rt.SocketPath, 10*time.Second); err != nil {
		return err
	}

	bootArgs := strings.Join([]string{
		"console=ttyS0",
		"reboot=k",
		"panic=1",
		"pci=off",
		"root=/dev/vda",
		"rw",
		fmt.Sprintf("ip=%s::%s:255.255.255.0::eth0:off", rt.GuestIP, rt.HostIP),
	}, " ")

	if err := fcPut(rt.SocketPath, "/machine-config", map[string]any{
		"vcpu_count":   inst.CPUCount,
		"mem_size_mib": inst.MemoryMB,
	}); err != nil {
		return err
	}
	if err := fcPut(rt.SocketPath, "/boot-source", map[string]any{
		"kernel_image_path": cfg.KernelImagePath,
		"boot_args":         bootArgs,
	}); err != nil {
		return err
	}
	if err := fcPut(rt.SocketPath, "/drives/rootfs", map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   rt.DiskImagePath,
		"is_root_device": true,
		"is_read_only":   false,
	}); err != nil {
		return err
	}
	if err := fcPut(rt.SocketPath, "/network-interfaces/eth0", map[string]any{
		"iface_id":      "eth0",
		"host_dev_name": rt.TapName,
		"guest_mac":     rt.GuestMAC,
	}); err != nil {
		return err
	}
	return fcPut(rt.SocketPath, "/actions", map[string]any{"action_type": "InstanceStart"})
}

func startForwarders(rt InstanceRuntime, inst Instance) error {
	if err := startSocatForward(rt.SSHForwardPID, "127.0.0.1", rt.SSHPort, rt.GuestIP, 22, rt.SerialLogPath); err != nil {
		return err
	}
	if err := startSocatForward(rt.AgentForwardPID, "127.0.0.1", inst.ShelleyPort, rt.GuestIP, 9000, rt.SerialLogPath); err != nil {
		_ = stopPIDFile(rt.SSHForwardPID)
		return err
	}
	if err := startSocatForward(rt.AppForwardPID, "0.0.0.0", inst.WebPort, rt.GuestIP, inst.WebPort, rt.SerialLogPath); err != nil {
		_ = stopPIDFile(rt.AgentForwardPID)
		_ = stopPIDFile(rt.SSHForwardPID)
		return err
	}
	return nil
}

func stopInstanceRuntime(rt InstanceRuntime, cfg Config) error {
	_ = stopPIDFile(rt.SSHForwardPID)
	_ = stopPIDFile(rt.AgentForwardPID)
	_ = stopPIDFile(rt.AppForwardPID)
	_ = stopPIDFile(rt.PIDPath)
	_ = teardownTap(rt.TapName)
	_ = deleteNATRule(rt.SubnetCIDR, cfg.OutboundInterface)
	_ = os.Remove(rt.SocketPath)
	_ = unmount(rt.MountDir)
	return nil
}

func ensureMountedDisk(rt InstanceRuntime) error {
	if err := os.MkdirAll(rt.MountDir, 0o755); err != nil {
		return err
	}
	mounted, _ := isMountpoint(rt.MountDir)
	if !mounted {
		if err := runCmd("", "mount", "-o", "loop", rt.DiskImagePath, rt.MountDir); err != nil {
			return err
		}
	}
	return nil
}

func ensureHostNetwork(cfg Config) error {
	if cfg.OutboundInterface == "" {
		return nil
	}
	if err := runCmd("", "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	return nil
}

func setupTap(name, hostCIDR string) error {
	_ = teardownTap(name)
	if err := runCmd("", "ip", "tuntap", "add", name, "mode", "tap"); err != nil {
		return err
	}
	if err := runCmd("", "ip", "addr", "add", hostCIDR, "dev", name); err != nil {
		_ = teardownTap(name)
		return err
	}
	return runCmd("", "ip", "link", "set", name, "up")
}

func teardownTap(name string) error {
	_ = exec.Command("ip", "link", "set", name, "down").Run()
	return exec.Command("ip", "link", "del", name).Run()
}

func ensureNATRule(subnetCIDR, outboundIF string) error {
	if outboundIF == "" {
		return nil
	}
	check := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", subnetCIDR, "-o", outboundIF, "-j", "MASQUERADE")
	if err := check.Run(); err == nil {
		return nil
	}
	return runCmd("", "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", subnetCIDR, "-o", outboundIF, "-j", "MASQUERADE")
}

func deleteNATRule(subnetCIDR, outboundIF string) error {
	if subnetCIDR == "" || outboundIF == "" {
		return nil
	}
	_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", subnetCIDR, "-o", outboundIF, "-j", "MASQUERADE").Run()
	return nil
}

func fcPut(socketPath, apiPath string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, "http://localhost"+apiPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("firecracker %s failed: %s: %s", apiPath, resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear", path)
}

func waitForPort(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("port %s did not open", addr)
}

func startSocatForward(pidPath, bindHost string, listenPort int, destHost string, destPort int, logPath string) error {
	_ = stopPIDFile(pidPath)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command("socat",
		fmt.Sprintf("TCP-LISTEN:%d,bind=%s,reuseaddr,fork", listenPort, bindHost),
		fmt.Sprintf("TCP:%s:%d", destHost, destPort),
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
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
			time.Sleep(200 * time.Millisecond)
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
