package agent

func DefaultModels() []ModelSpec {
	return []ModelSpec{
		{ID: "gpt-5.4", Label: "GPT-5.4", APIKeyEnv: "OPENAI_API_KEY"},
		{ID: "gpt-5.4-mini", Label: "GPT-5.4 Mini", APIKeyEnv: "OPENAI_API_KEY"},
		{ID: "gpt-5.5", Label: "GPT-5.5", APIKeyEnv: "OPENAI_API_KEY"},
	}
}

func ResolveModels(configured []ModelSpec, defaultModel string) []ModelSpec {
	models := configured
	if len(models) == 0 {
		models = DefaultModels()
	}
	if defaultModel == "" {
		return models
	}
	for _, model := range models {
		if model.ID == defaultModel {
			return models
		}
	}
	return append([]ModelSpec{{
		ID:        defaultModel,
		Label:     defaultModel,
		APIKeyEnv: "OPENAI_API_KEY",
	}}, models...)
}

func FindModel(models []ModelSpec, id string) (ModelSpec, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return ModelSpec{}, false
}
