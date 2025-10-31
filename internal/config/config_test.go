package config

import (
	"os"
	"testing"
)

func TestLoadLLMConfig(t *testing.T) {
	content := `llm:
  enabled: true
  baseURL: "http://localhost:8080"
  apiKey: "secret"
  model: "mistral-8b"
  timeout: 3s
  maxTokens: 256
  temperature: 0.1
`

	tmp := t.TempDir() + "/cfg.yaml"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.LLM.Enabled {
		t.Errorf("expected llm.enabled true")
	}
	if cfg.LLM.BaseURL != "http://localhost:8080" {
		t.Errorf("unexpected baseURL: %s", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "secret" {
		t.Errorf("unexpected apiKey: %s", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "mistral-8b" {
		t.Errorf("unexpected model: %s", cfg.LLM.Model)
	}
	if cfg.LLM.MaxTokens != 256 {
		t.Errorf("unexpected maxTokens: %d", cfg.LLM.MaxTokens)
	}
}
