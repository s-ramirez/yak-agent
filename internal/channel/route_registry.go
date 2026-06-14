package channel

import "sync"

// RouteRegistry records topic-scoped agent routing decisions so the main
// dispatcher can hand those conversations to the correct turn handler.
type RouteRegistry struct {
	mu     sync.RWMutex
	agents map[Key]string
}

func NewRouteRegistry() *RouteRegistry {
	return &RouteRegistry{agents: make(map[Key]string)}
}

func (r *RouteRegistry) Set(key Key, agent string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents == nil {
		r.agents = make(map[Key]string)
	}
	r.agents[key] = agent
}

func (r *RouteRegistry) Lookup(key Key) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[key]
	return agent, ok
}
