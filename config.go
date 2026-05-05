package main

import (
	"fmt"

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
	if cfg.QEMUBinary == "" {
		cfg.QEMUBinary = "qemu-system-x86_64"
	}
	if cfg.KernelImagePath == "" {
		return cfg, fmt.Errorf("kernel_image_path is required")
	}
	if cfg.InitramfsPath == "" {
		return cfg, fmt.Errorf("initramfs_path is required")
	}
	if cfg.TemplateImagePath == "" {
		return cfg, fmt.Errorf("template_image_path is required")
	}
	return cfg, nil
}
