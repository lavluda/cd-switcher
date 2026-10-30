package main

import "testing"

func TestUsageFormats(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5": "Opus 5.5", "claude-haiku-4-5-20251001": "Haiku 4.5", "claude-fable-5": "Fable 5",
		"gpt-6.1-sol": "GPT-6.1 Sol", "minimax-m3": "minimax-m3",
	} {
		if got := modelName(in); got != want {
			t.Errorf("modelName(%q) = %q, want %q", in, got, want)
		}
	}
	if tokens(448_300_000) != "448M" || tokens(2_463_000_000) != "2.46B" || tokens(12_340) != "12.3K" || count(1234567) != "1,234,567" || money(1011.6) != "$1,011" {
		t.Error("number formats")
	}
}
