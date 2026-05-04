package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

type OpenAIClient struct {
	clients map[string]*openai.Client
	models  map[string]ModelSpec
}

func NewOpenAIClient(cfg Config) (*OpenAIClient, error) {
	models := cfg.Models
	clients := make(map[string]*openai.Client, len(models))
	modelIndex := make(map[string]ModelSpec, len(models))
	for _, model := range models {
		key := strings.TrimSpace(model.APIKey)
		envName := model.APIKeyEnv
		if key == "" && envName != "" {
			key = strings.TrimSpace(os.Getenv(envName))
		}
		if key == "" {
			key = strings.TrimSpace(cfg.OpenAIAPIKey)
		}
		if key == "" && envName == "" {
			key = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		}
		if key == "" {
			modelIndex[model.ID] = model
			continue
		}
		cfg := openai.DefaultConfig(key)
		if model.BaseURL != "" {
			cfg.BaseURL = model.BaseURL
		}
		clients[model.ID] = openai.NewClientWithConfig(cfg)
		modelIndex[model.ID] = model
	}
	return &OpenAIClient{
		clients: clients,
		models:  modelIndex,
	}, nil
}

func (c *OpenAIClient) RunTurn(ctx context.Context, cfg Config, conv Conversation, history []Message, store *Store) (string, error) {
	model, ok := c.models[conv.ModelID]
	if !ok {
		return "", fmt.Errorf("unknown model: %s", conv.ModelID)
	}
	client := c.clients[model.ID]
	if client == nil {
		return "", fmt.Errorf("model %s is not configured with an API key", conv.ModelID)
	}
	messages := buildChatMessages(cfg, conv.Cwd, history)
	tools := toolDefinitions()

	for i := 0; i < 8; i++ {
		req := openai.ChatCompletionRequest{
			Model:    model.ID,
			Messages: messages,
			Tools:    tools,
		}
		resp, err := client.CreateChatCompletion(ctx, req)
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("empty model response")
		}
		msg := resp.Choices[0].Message

		if len(msg.ToolCalls) == 0 {
			text := strings.TrimSpace(msg.Content)
			if text == "" {
				text = "(empty response)"
			}
			return text, nil
		}

		messages = append(messages, msg)

		for _, call := range msg.ToolCalls {
			result, err := executeTool(ctx, cfg.WorkspaceDir, conv.Cwd, call)
			output := ""
			if err != nil {
				output = "tool error: " + err.Error()
			} else {
				output = result.Output
				if result.NewCwd != "" && result.NewCwd != conv.Cwd {
					conv.Cwd = result.NewCwd
					if saveErr := store.UpdateConversationCwd(conv.ID, conv.Cwd); saveErr != nil {
						return "", saveErr
					}
				}
			}
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Content:    output,
			})
			if _, saveErr := store.AddMessage(conv.ID, "tool", fmt.Sprintf("%s\n\n%s", call.Function.Name, output)); saveErr != nil {
				return "", saveErr
			}
		}
	}

	return "", fmt.Errorf("tool loop exceeded limit")
}

func buildChatMessages(cfg Config, cwd string, history []Message) []openai.ChatCompletionMessage {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: BuildSystemPrompt(cfg, cwd)},
	}
	for _, msg := range history {
		switch msg.Role {
		case "user":
			messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: msg.Content})
		case "assistant":
			messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: msg.Content})
		case "tool":
			// Tool history is retained for the UI, but not replayed. Fresh tool state per turn keeps the loop simpler.
		}
	}
	return messages
}
