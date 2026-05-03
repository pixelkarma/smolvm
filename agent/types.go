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
	Models         []ModelSpec
}

type ModelSpec struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

type Conversation struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Cwd       string    `json:"cwd"`
	ModelID   string    `json:"model_id"`
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
