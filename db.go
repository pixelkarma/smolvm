package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func openDB(cfg Config) (*sql.DB, string, error) {
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(cfg.DataDir, "smolvm.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, "", err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, "", err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, "", err
	}
	return db, dbPath, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			shelley_port INTEGER NOT NULL UNIQUE,
			web_port INTEGER NOT NULL UNIQUE,
			memory_mb INTEGER NOT NULL,
			cpu_count INTEGER NOT NULL,
			disk_mb INTEGER NOT NULL,
			api_key_path TEXT NOT NULL DEFAULT '',
			api_key TEXT NOT NULL DEFAULT '',
			initial_prompt TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	_, _ = db.Exec(`ALTER TABLE instances ADD COLUMN api_key TEXT NOT NULL DEFAULT ''`)
	return nil
}

func loadSettings(db *sql.DB, cfg Config) (Settings, error) {
	s := Settings{
		GlobalPrompt:        defaultGlobalPrompt(),
		DefaultOpenAIAPIKey: cfg.DefaultOpenAIAPIKey,
		PublicHost:          cfg.PublicHost,
	}
	rows, err := db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return s, err
		}
		switch key {
		case "password_hash":
			s.PasswordHash = value
		case "session_key":
			s.SessionKey = value
		case "global_prompt":
			s.GlobalPrompt = value
		case "default_openai_api_key":
			s.DefaultOpenAIAPIKey = value
		case "system_key_path":
			s.DefaultOpenAIAPIKey = value
		case "public_host":
			s.PublicHost = value
		}
	}
	return s, rows.Err()
}

func saveSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func insertInstance(db *sql.DB, inst *Instance) error {
	now := time.Now().UTC()
	inst.CreatedAt = now
	inst.UpdatedAt = now
	res, err := db.Exec(`INSERT INTO instances
		(name, slug, shelley_port, web_port, memory_mb, cpu_count, disk_mb, api_key, initial_prompt, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inst.Name, inst.Slug, inst.ShelleyPort, inst.WebPort, inst.MemoryMB, inst.CPUCount, inst.DiskMB, inst.APIKey, inst.InitialPrompt,
		inst.CreatedAt.Format(time.RFC3339), inst.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return err
	}
	inst.ID, err = res.LastInsertId()
	return err
}

func deleteInstanceRecord(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM instances WHERE id = ?`, id)
	return err
}

func listInstances(db *sql.DB) ([]Instance, error) {
	rows, err := db.Query(`SELECT id, name, slug, shelley_port, web_port, memory_mb, cpu_count, disk_mb, COALESCE(api_key, api_key_path, ''), initial_prompt, created_at, updated_at
		FROM instances ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var inst Instance
		var created, updated string
		if err := rows.Scan(&inst.ID, &inst.Name, &inst.Slug, &inst.ShelleyPort, &inst.WebPort, &inst.MemoryMB, &inst.CPUCount, &inst.DiskMB, &inst.APIKey, &inst.InitialPrompt, &created, &updated); err != nil {
			return nil, err
		}
		inst.CreatedAt, _ = time.Parse(time.RFC3339, created)
		inst.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, inst)
	}
	return out, rows.Err()
}

func getInstance(db *sql.DB, id int64) (Instance, error) {
	var inst Instance
	var created, updated string
	err := db.QueryRow(`SELECT id, name, slug, shelley_port, web_port, memory_mb, cpu_count, disk_mb, COALESCE(api_key, api_key_path, ''), initial_prompt, created_at, updated_at
		FROM instances WHERE id = ?`, id).
		Scan(&inst.ID, &inst.Name, &inst.Slug, &inst.ShelleyPort, &inst.WebPort, &inst.MemoryMB, &inst.CPUCount, &inst.DiskMB, &inst.APIKey, &inst.InitialPrompt, &created, &updated)
	if err != nil {
		return inst, err
	}
	inst.CreatedAt, _ = time.Parse(time.RFC3339, created)
	inst.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return inst, nil
}

func nextAvailablePort(instances []Instance, start int, selector func(Instance) int) int {
	used := map[int]bool{}
	for _, inst := range instances {
		used[selector(inst)] = true
	}
	for port := start; port < 65535; port++ {
		if !used[port] {
			return port
		}
	}
	return start
}

func mustUniqueSlug(db *sql.DB, base string) (string, error) {
	slug := slugify(base)
	if slug == "" {
		slug = "instance"
	}
	candidate := slug
	for i := 1; i < 1000; i++ {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM instances WHERE slug = ?`, candidate).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", slug, i+1)
	}
	return "", errors.New("failed to allocate unique slug")
}
