package telegram

import "strings"

// ConfigFromEnv reads minimal Telegram topic routing config from env.
// This is routing-only for now; it does not enable a live Telegram transport.
//
// YAK_TELEGRAM_TOPIC_AGENTS format:
//
//	<topic>=<subagent>[,<topic>=<subagent>...]
//
// Example:
//
//	exercise=rocky,nutrition=scout,general=main
func ConfigFromEnv(getenv func(string) string) Config {
	cfg := Config{Topics: map[string]TopicConfig{}}
	raw := strings.TrimSpace(getenv("YAK_TELEGRAM_TOPIC_AGENTS"))
	if raw == "" {
		return cfg
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		left, right, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		topic := strings.TrimSpace(left)
		agent := strings.TrimSpace(right)
		if topic == "" || agent == "" {
			continue
		}
		cfg.Topics[topic] = TopicConfig{Subagent: agent}
	}
	return cfg
}
