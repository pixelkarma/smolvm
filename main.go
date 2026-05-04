package main

import (
	"flag"
	"log"
	"net/http"

	"smolvm/configfile"
)

func main() {
	defaultConfigPath := "smolvm.config.json"
	if p, err := configfile.DefaultPath(); err == nil {
		defaultConfigPath = p
	}
	var configPath string
	flag.StringVar(&configPath, "config", defaultConfigPath, "Path to smolvm config JSON")
	flag.Parse()

	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
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
