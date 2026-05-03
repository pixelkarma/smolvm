package agent

import "time"

type Config struct {
	ListenAddr     string      `json:"listen_addr"`
	DBPath         string      `json:"db_path"`
	WorkspaceDir   string      `json:"workspace_dir"`
	UIDir          string      `json:"ui_dir,omitempty"`
	GlobalPrompt   string      `json:"global_prompt,omitempty"`
	DefaultModel   string      `json:"default_model"`
	RequiredHeader string      `json:"required_header,omitempty"`
	OpenAIAPIKey   string      `json:"openai_api_key,omitempty"`
	Models         []ModelSpec `json:"models,omitempty"`
}

type ModelSpec struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
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
