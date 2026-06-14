package telegram

import "testing"

func TestConfigFromEnvParsesTopicAgents(t *testing.T) {
	cfg := ConfigFromEnv(func(key string) string {
		if key == "YAK_TELEGRAM_TOPIC_AGENTS" {
			return "exercise=rocky, nutrition=scout, general=main"
		}
		return ""
	})
	if got := cfg.Topics["exercise"].Subagent; got != "rocky" {
		t.Fatalf("exercise = %q", got)
	}
	if got := cfg.Topics["nutrition"].Subagent; got != "scout" {
		t.Fatalf("nutrition = %q", got)
	}
	if got := cfg.Topics["general"].Subagent; got != "main" {
		t.Fatalf("general = %q", got)
	}
}
