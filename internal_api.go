package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type instanceConfigResponse struct {
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

func (a *App) handleInternalInstanceConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isInternalGuestRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("instance_id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid instance_id", http.StatusBadRequest)
		return
	}
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.Error(w, "instance not found", http.StatusNotFound)
		return
	}
	settings, err := loadSettings(a.db, a.cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key := strings.TrimSpace(inst.APIKey)
	if key == "" {
		key = strings.TrimSpace(settings.DefaultOpenAIAPIKey)
	}
	resp := instanceConfigResponse{
		InstanceID:    inst.ID,
		Name:          inst.Name,
		Slug:          inst.Slug,
		OpenAIAPIKey:  key,
		GlobalPrompt:  settings.GlobalPrompt,
		InitialPrompt: inst.InitialPrompt,
		AgentPort:     9000,
		WebPort:       80,
		MemoryMB:      inst.MemoryMB,
		CPUCount:      inst.CPUCount,
		DiskMB:        inst.DiskMB,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func isInternalGuestRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	_, guestNet, _ := net.ParseCIDR("10.0.2.0/24")
	return guestNet.Contains(ip)
}

func (a *App) handleInternalAgentBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isInternalGuestRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	binaryPath := filepath.Join(filepath.Dir(a.cfg.DataDir), "bin", "smolagent-linux-amd64")
	f, err := os.Open(binaryPath)
	if err != nil {
		http.Error(w, "agent binary not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="smolagent-linux-amd64"`)
	_, _ = io.Copy(w, f)
}
