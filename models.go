package main

import "time"

type Config struct {
	ListenAddr          string `json:"listen_addr"`
	DataDir             string `json:"data_dir"`
	DBPath              string `json:"db_path"`
	AgentBinaryPath     string `json:"agent_binary_path"`
	ImageName           string `json:"image_name"`
	PublicHost          string `json:"public_host"`
	DefaultOpenAIAPIKey string `json:"default_openai_api_key"`
	AdminPassword       string `json:"admin_password"`
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
	ContainerName string
	InstanceDir   string
	DiskImagePath string
	MountDir      string
	WorkspaceDir  string
	ConfigDir     string
	VarLibDir     string
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
