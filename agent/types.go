package agent

import "time"

type Config struct {
	ListenAddr     string
	DBPath         string
	WorkspaceDir   string
	UIDir          string
	GlobalPrompt   string
	DefaultModel   string
	RequiredHeader string
}

type Conversation struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Cwd       string    `json:"cwd"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             int64     `json:"id"`
	ConversationID int64     `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}
