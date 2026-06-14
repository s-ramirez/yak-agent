package channel

import "context"

// NamedTurnHandler exposes the identity needed for topic-scoped delegation.
type NamedTurnHandler interface {
	TurnHandler
	Name() string
}

// DelegatingHandler routes conversations to the main handler by default, but
// can hand topic-scoped conversations to a handler resolved by agent name.
type DelegatingHandler struct {
	Main        TurnHandler
	Subagents   map[string]TurnHandler
	RouteAgents *RouteRegistry
}

func (h *DelegatingHandler) HandleTurn(ctx context.Context, conv *Conversation, userContent string, reply ReplyFunc) error {
	if h == nil || h.Main == nil {
		return nil
	}
	if h.RouteAgents == nil || len(h.Subagents) == 0 {
		return h.Main.HandleTurn(ctx, conv, userContent, reply)
	}
	agent, ok := h.RouteAgents.Lookup(conv.Key)
	if !ok || agent == "" {
		return h.Main.HandleTurn(ctx, conv, userContent, reply)
	}
	handler, ok := h.Subagents[agent]
	if !ok || handler == nil {
		return h.Main.HandleTurn(ctx, conv, userContent, reply)
	}
	return handler.HandleTurn(ctx, conv, userContent, reply)
}
