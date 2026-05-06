package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"smolvm/configfile"
)

const (
	dmiSerialPath       = "/sys/class/dmi/id/product_serial"
	guestAdminConfigURL = "http://10.0.2.2:8090/internal/instance-config?instance_id=%d"
	vmPromptPath        = "/root/.smolvm/AGENTS.md"
)

type guestBootstrapResponse struct {
	InstanceID    int64  `json:"instance_id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	OpenAIAPIKey  string `json:"openai_api_key"`
	GlobalPrompt  string `json:"global_prompt"`
	InitialPrompt string `json:"initial_prompt"`
	AgentPort     int    `json:"agent_port"`
	WebPort       int    `json:"web_port"`
	MemoryMB      int    `json:"memory_mb"`
	CPUCount      int    `json:"cpu_count"`
	DiskMB        int    `json:"disk_mb"`
}

func EnsureBootstrapConfig(path string) error {
	resolvedPath, err := configfile.ExpandPath(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(resolvedPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	instanceID, err := instanceIDFromDMI()
	if err != nil {
		return err
	}
	cfgResp, err := fetchGuestConfig(instanceID)
	if err != nil {
		return err
	}

	cfg := Config{
		ListenAddr:     ":9000",
		DBPath:         "/var/lib/smolagent/smolagent.db",
		WorkspaceDir:   "/workspace",
		DefaultModel:   "gpt-5.4",
		RequiredHeader: "X-SmolVM-Admin",
		OpenAIAPIKey:   strings.TrimSpace(cfgResp.OpenAIAPIKey),
		GlobalPrompt:   strings.TrimSpace(cfgResp.GlobalPrompt),
	}
	if err := configfile.WriteJSON(resolvedPath, cfg, 0o600); err != nil {
		return err
	}

	if text := strings.TrimSpace(cfgResp.InitialPrompt); text != "" {
		if err := os.MkdirAll(filepath.Dir(vmPromptPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(vmPromptPath, []byte(text+"\n"), 0o644); err != nil {
			return err
		}
	}

	return nil
}

func instanceIDFromDMI() (int64, error) {
	data, err := os.ReadFile(dmiSerialPath)
	if err != nil {
		return 0, fmt.Errorf("read DMI serial: %w", err)
	}
	serial := strings.TrimSpace(string(data))
	idText, ok := strings.CutPrefix(serial, "smolvm-instance:")
	if !ok {
		return 0, fmt.Errorf("unexpected DMI serial format: %q", serial)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(idText), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid instance id in DMI serial: %q", serial)
	}
	return id, nil
}

func fetchGuestConfig(instanceID int64) (guestBootstrapResponse, error) {
	var respData guestBootstrapResponse
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf(guestAdminConfigURL, instanceID))
	if err != nil {
		return respData, fmt.Errorf("fetch guest config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return respData, fmt.Errorf("fetch guest config: unexpected status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return respData, fmt.Errorf("decode guest config: %w", err)
	}
	return respData, nil
}
