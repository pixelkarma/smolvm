package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"smolvm/configfile"
)

func LoadConfig(path string) (Config, error) {
	var cfg Config
	resolvedPath, err := configfile.ExpandPath(path)
	if err != nil {
		return cfg, err
	}
	if err := configfile.LoadJSON(resolvedPath, &cfg); err != nil {
		return cfg, err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8090"
	}
	if cfg.DataDir == "" {
		return cfg, fmt.Errorf("data_dir is required")
	}
	if cfg.AgentBinaryPath == "" {
		return cfg, fmt.Errorf("agent_binary_path is required")
	}
	if cfg.FirecrackerBinary == "" {
		cfg.FirecrackerBinary = "/usr/local/bin/firecracker"
	}
	if cfg.KernelImagePath == "" {
		return cfg, fmt.Errorf("kernel_image_path is required")
	}
	if cfg.TemplateImagePath == "" {
		return cfg, fmt.Errorf("template_image_path is required")
	}
	if cfg.OutboundInterface == "" {
		cfg.OutboundInterface = detectOutboundInterface()
	}
	if runtime.GOARCH != "amd64" {
		return cfg, fmt.Errorf("firecracker runtime currently requires amd64 host architecture")
	}
	return cfg, nil
}

func detectOutboundInterface() string {
	out, err := exec.Command("sh", "-c", "ip route get 1.1.1.1 2>/dev/null | awk '/dev/ {for (i = 1; i <= NF; i++) if ($i == \"dev\") {print $(i+1); exit}}'").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
