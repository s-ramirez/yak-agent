package telegram

import (
	"context"
	"strings"

	"yak-go/internal/channel"
)

const Name = "telegram"

// TopicConfig describes one logical topic within a Telegram thread/group.
type TopicConfig struct {
	Subagent string
}

// Config is a minimal routing-only Telegram config. It does not implement
// Telegram network I/O yet; it exists so the dispatcher can isolate topic
// conversations and map topics to subagents.
type Config struct {
	Topics map[string]TopicConfig
}

// Channel is a routing stub used by the dispatcher and tests. Listen/Send are
// intentionally inert until a real Telegram transport is added.
type Channel struct {
	cfg    Config
	routes *channel.RouteRegistry
}

func New(cfg Config, routes *channel.RouteRegistry) *Channel {
	if cfg.Topics == nil {
		cfg.Topics = map[string]TopicConfig{}
	}
	return &Channel{cfg: cfg, routes: routes}
}

func (c *Channel) Name() string { return Name }

func (c *Channel) Listen(ctx context.Context, out chan<- channel.Inbound) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *Channel) Send(ctx context.Context, msg channel.Outbound) error {
	_ = ctx
	_ = msg
	return nil
}

func (c *Channel) Route(in channel.Inbound) channel.TopicRoute {
	route := channel.DefaultTopicRoute(in)
	topic := strings.TrimSpace(in.Topic)
	if topic == "" {
		return route
	}
	cfg, ok := c.cfg.Topics[topic]
	if !ok {
		return route
	}
	agent := channel.NormalizeTopicAgentName(cfg.Subagent)
	if agent == "" {
		return route
	}
	route.Key.Thread = in.Thread + "::agent=" + agent
	route.AgentName = agent
	if c.routes != nil {
		c.routes.Set(route.Key, agent)
	}
	return route
}
