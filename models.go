package main

import "time"

type Config struct {
	ListenAddr          string `json:"listen_addr"`
	DataDir             string `json:"data_dir"`
	DBPath              string `json:"db_path"`
	PublicHost          string `json:"public_host"`
	DefaultOpenAIAPIKey string `json:"default_openai_api_key"`
	AdminPassword       string `json:"admin_password"`
	QEMUBinary          string `json:"qemu_binary_path"`
	TemplateImagePath   string `json:"template_image_path"`
	GuestSSHKeyPath     string `json:"guest_ssh_key_path"`
}

type Instance struct {
	ID            int64
	Name          string
	Slug          string
	ShelleyPort   int
	WebPort       int
	MemoryMB      int
	CPUCount      int
	DiskMB        int
	APIKey        string
	InitialPrompt string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Settings struct {
	PasswordHash        string
	SessionKey          string
	GlobalPrompt        string
	DefaultOpenAIAPIKey string
	PublicHost          string
}

type InstanceRuntime struct {
	MachineName     string
	InstanceDir     string
	DiskImagePath   string
	SerialLogPath   string
	PIDPath         string
	QMPPath         string
	SSHPort         int
}

type DashboardData struct {
	Title               string
	Auth                bool
	Instances           []InstanceView
	DefaultOpenAIAPIKey string
	GlobalPrompt        string
	AdminHost           string
	Error               string
}

type InstanceView struct {
	Instance
	Status     string
	ShelleyURL string
	AppURL     string
	CreatedAgo string
}

type InstanceFormData struct {
	Title               string
	Instance            Instance
	GlobalPrompt        string
	DefaultOpenAIAPIKey string
	DefaultWebPort      int
	Error               string
	IsEdit              bool
}

type LoginData struct {
	Title string
	Error string
}

type SettingsData struct {
	Title               string
	GlobalPrompt        string
	DefaultOpenAIAPIKey string
	PublicHost          string
	Error               string
	Success             string
}
