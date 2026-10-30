package provider

import "github.com/lavluda/cd-switcher/internal/harness"

// Presets are starting points for the Add Provider form. Anything else is
// "Custom". The local servers each speak Anthropic's Messages API (for Claude
// Code) or OpenAI's Responses API (for Codex) on their default port.
var Presets = []Provider{
	{Name: "Anthropic API key", Harness: harness.Claude, BaseURL: "https://api.anthropic.com", KeyVar: "ANTHROPIC_API_KEY"},
	{Name: "OpenRouter", Harness: harness.Claude, BaseURL: "https://openrouter.ai/api", KeyVar: "ANTHROPIC_AUTH_TOKEN", Env: map[string]string{"ANTHROPIC_API_KEY": ""}},
	{Name: "Amazon Bedrock", Harness: harness.Claude, Env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_REGION": "us-east-1"}},
	{Name: "Google Vertex AI", Harness: harness.Claude, Env: map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "CLOUD_ML_REGION": "us-east5", "ANTHROPIC_VERTEX_PROJECT_ID": ""}},
	{Name: "Ollama", Harness: harness.Claude, BaseURL: "http://localhost:11434", KeyVar: "ANTHROPIC_AUTH_TOKEN"},
	{Name: "LM Studio", Harness: harness.Claude, BaseURL: "http://localhost:1234", KeyVar: "ANTHROPIC_AUTH_TOKEN"},
	{Name: "llama.cpp", Harness: harness.Claude, BaseURL: "http://localhost:8080", KeyVar: "ANTHROPIC_AUTH_TOKEN"},
	{Name: "vLLM", Harness: harness.Claude, BaseURL: "http://localhost:8000", KeyVar: "ANTHROPIC_AUTH_TOKEN"},

	{Name: "OpenRouter", Harness: harness.Codex, BaseURL: "https://openrouter.ai/api/v1"},
	{Name: "Ollama", Harness: harness.Codex, BaseURL: "http://localhost:11434/v1"},
	{Name: "LM Studio", Harness: harness.Codex, BaseURL: "http://localhost:1234/v1"},
	{Name: "vLLM", Harness: harness.Codex, BaseURL: "http://localhost:8000/v1"},
	{Name: "llama.cpp", Harness: harness.Codex, BaseURL: "http://localhost:8080/v1"},
}

// PresetsFor lists the presets for one tool.
func PresetsFor(h string) []Provider {
	var out []Provider
	for _, p := range Presets {
		if p.Harness == h {
			out = append(out, p)
		}
	}
	return out
}
