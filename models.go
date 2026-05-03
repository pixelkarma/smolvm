package main

import "time"

type Config struct {
	ListenAddr      string
	DataDir         string
	DBPath          string
	AgentBinaryPath string
	ImageName       string
	PublicHost      string
	SystemKeyPath   string
	AdminPassword   string
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
	APIKeyPath    string
	InitialPrompt string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Settings struct {
	PasswordHash  string
	SessionKey    string
	GlobalPrompt  string
	SystemKeyPath string
	PublicHost    string
}

type InstanceRuntime struct {
	ContainerName string
	InstanceDir   string
	DiskImagePath string
	MountDir      string
	WorkspaceDir  string
	RootConfigDir string
	VarLibDir     string
}

type DashboardData struct {
	Title        string
	Auth         bool
	Instances    []InstanceView
	SystemKey    string
	GlobalPrompt string
	AdminHost    string
	Error        string
}

type InstanceView struct {
	Instance
	Status     string
	ShelleyURL string
	AppURL     string
	CreatedAgo string
}

type InstanceFormData struct {
	Title          string
	Instance       Instance
	GlobalPrompt   string
	SystemKeyPath  string
	DefaultWebPort int
	Error          string
	IsEdit         bool
}

type LoginData struct {
	Title string
	Error string
}

type SettingsData struct {
	Title         string
	GlobalPrompt  string
	SystemKeyPath string
	PublicHost    string
	Error         string
	Success       string
}
