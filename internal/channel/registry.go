package channel

import "sync"

// Registry is a name-keyed lookup of channels. It is used by the
// dispatcher both to spawn listeners at startup and to resolve reply
// routing at turn time.
type Registry struct {
	mu       sync.RWMutex
	channels map[string]Channel
}

func NewRegistry() *Registry {
	return &Registry{channels: make(map[string]Channel)}
}

func (r *Registry) Register(ch Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.channels[ch.Name()] = ch
}

func (r *Registry) Lookup(name string) (Channel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.channels[name]
	return ch, ok
}

func (r *Registry) All() []Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Channel, 0, len(r.channels))
	for _, c := range r.channels {
		out = append(out, c)
	}
	return out
}
