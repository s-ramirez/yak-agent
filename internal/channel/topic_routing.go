package channel

import "strings"

// TopicRouter can override the target conversation and agent-facing content
// for a single inbound message. It is intended for channels that expose
// native subthreads/topics (for example Telegram forum topics).
type TopicRouter interface {
	Route(in Inbound) TopicRoute
}

// TopicRoute describes the resolved routing for one inbound message.
type TopicRoute struct {
	Key         Key
	Content     string
	AgentName   string
	ResetPrefix bool
}

// DefaultTopicRoute keeps the existing channel/thread/topic and content.
func DefaultTopicRoute(in Inbound) TopicRoute {
	return TopicRoute{
		Key:     Key{Channel: in.Channel, Thread: in.Thread, Topic: in.Topic},
		Content: in.Content,
	}
}

// NormalizeTopicAgentName returns a conservative subagent/agent token.
func NormalizeTopicAgentName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	prevDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if b.Len() == 0 || prevDash {
				continue
			}
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}
