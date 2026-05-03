package agent

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
		cfg.ListenAddr = ":9000"
	}
	if cfg.DBPath == "" {
		return cfg, fmt.Errorf("db_path is required")
	}
	if cfg.WorkspaceDir == "" {
		return cfg, fmt.Errorf("workspace_dir is required")
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "gpt-5.4"
	}
	return cfg, nil
}
