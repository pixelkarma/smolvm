package agent

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := migrateStore(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func migrateStore(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			cwd TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
		);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateConversation(title, cwd string) (Conversation, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO conversations(title, cwd, created_at, updated_at) VALUES(?, ?, ?, ?)`,
		title, cwd, now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return Conversation{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Conversation{}, err
	}
	return Conversation{ID: id, Title: title, Cwd: cwd, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) ListConversations() ([]Conversation, error) {
	out := make([]Conversation, 0)
	rows, err := s.db.Query(`SELECT id, title, cwd, created_at, updated_at FROM conversations ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Conversation
		var created, updated string
		if err := rows.Scan(&c.ID, &c.Title, &c.Cwd, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConversation(id int64) (Conversation, error) {
	var c Conversation
	var created, updated string
	err := s.db.QueryRow(`SELECT id, title, cwd, created_at, updated_at FROM conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Title, &c.Cwd, &created, &updated)
	if err != nil {
		return Conversation{}, err
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, created)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return c, nil
}

func (s *Store) TouchConversation(id int64) error {
	_, err := s.db.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) UpdateConversationCwd(id int64, cwd string) error {
	_, err := s.db.Exec(`UPDATE conversations SET cwd = ?, updated_at = ? WHERE id = ?`, cwd, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) AddMessage(conversationID int64, role, content string) (Message, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO messages(conversation_id, role, content, created_at) VALUES(?, ?, ?, ?)`,
		conversationID, role, content, now.Format(time.RFC3339),
	)
	if err != nil {
		return Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	if err := s.TouchConversation(conversationID); err != nil {
		return Message{}, err
	}
	return Message{ID: id, ConversationID: conversationID, Role: role, Content: content, CreatedAt: now}, nil
}

func (s *Store) ListMessages(conversationID int64) ([]Message, error) {
	out := make([]Message, 0)
	rows, err := s.db.Query(`SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY id ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		var created string
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &created); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, m)
	}
	return out, rows.Err()
}
