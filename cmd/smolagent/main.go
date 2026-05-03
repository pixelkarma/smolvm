package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"smolvm/agent"
)

func main() {
	var cfg agent.Config
	flag.StringVar(&cfg.ListenAddr, "listen", envOrDefault("SMOLAGENT_LISTEN", ":9000"), "HTTP listen address")
	flag.StringVar(&cfg.DBPath, "db", envOrDefault("SMOLAGENT_DB", "/var/lib/smolagent/smolagent.db"), "SQLite database path")
	flag.StringVar(&cfg.WorkspaceDir, "workspace", envOrDefault("SMOLAGENT_WORKSPACE", "/workspace"), "Workspace directory")
	flag.StringVar(&cfg.UIDir, "ui-dir", envOrDefault("SMOLAGENT_UI_DIR", ""), "Serve UI files from this directory instead of embedded assets")
	flag.StringVar(&cfg.GlobalPrompt, "global-prompt", envOrDefault("SMOLAGENT_GLOBAL_PROMPT", ""), "Global prompt text")
	flag.StringVar(&cfg.DefaultModel, "model", envOrDefault("SMOLAGENT_MODEL", "gpt-5.4"), "Default model ID")
	flag.StringVar(&cfg.RequiredHeader, "require-header", envOrDefault("SMOLAGENT_REQUIRE_HEADER", ""), "Require this header on all requests")
	flag.Parse()

	srv, err := agent.NewServer(cfg)
	if err != nil {
		log.Fatalf("failed to initialize smolagent: %v", err)
	}
	defer srv.Close()

	log.Printf("smolagent listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Routes()); err != nil {
		log.Fatalf("smolagent server failed: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
