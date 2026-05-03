package main

import (
	"flag"
	"log"
	"net/http"
	"os"
)

func main() {
	var cfg Config
	flag.StringVar(&cfg.ListenAddr, "listen", envOrDefault("SMOLVM_LISTEN", ":8090"), "HTTP listen address")
	flag.StringVar(&cfg.DataDir, "data-dir", envOrDefault("SMOLVM_DATA_DIR", "./data"), "Data directory")
	flag.StringVar(&cfg.DBPath, "db", envOrDefault("SMOLVM_DB_PATH", ""), "SQLite database path")
	flag.StringVar(&cfg.AgentBinaryPath, "agent-binary", firstEnv("./smolagent-linux-aarch64", "SMOLVM_AGENT_BINARY", "SMOLVM_SHELLEY_BINARY"), "Path to smolagent runtime binary used to build instance image")
	flag.StringVar(&cfg.ImageName, "image", envOrDefault("SMOLVM_IMAGE", "smolvm-agent:latest"), "Docker image name for managed instances")
	flag.StringVar(&cfg.PublicHost, "public-host", envOrDefault("SMOLVM_PUBLIC_HOST", ""), "Host/IP users use to reach this admin and app ports")
	flag.StringVar(&cfg.SystemKeyPath, "system-key", envOrDefault("SMOLVM_SYSTEM_KEY", "/root/.openai"), "Default OpenAI key file path on host")
	flag.StringVar(&cfg.AdminPassword, "admin-password", envOrDefault("SMOLVM_ADMIN_PASSWORD", ""), "Initial admin password when database is first created")
	flag.Parse()

	app, err := NewApp(cfg)
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}
	defer app.Close()

	log.Printf("smolvm admin listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, app.Routes()); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstEnv(fallback string, keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return fallback
}
