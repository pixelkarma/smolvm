package main

import (
	"flag"
	"log"
	"net/http"

	"smolvm/agent"
	"smolvm/configfile"
)

func main() {
	defaultConfigPath := "smolvm.config.json"
	if p, err := configfile.DefaultPath(); err == nil {
		defaultConfigPath = p
	}
	var configPath string
	flag.StringVar(&configPath, "config", defaultConfigPath, "Path to smolagent config JSON")
	flag.Parse()

	if err := agent.EnsureBootstrapConfig(configPath); err != nil {
		log.Fatalf("failed to prepare smolagent config: %v", err)
	}
	cfg, err := agent.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("failed to load smolagent config: %v", err)
	}
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
