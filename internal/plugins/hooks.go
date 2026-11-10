package plugins

import (
	"context"
	"sort"
	"sync"
)

// DefaultPriority is the WordPress default hook priority.
const DefaultPriority = 10

// CoreOwner is the owner recorded for hooks registered by the core itself.
const CoreOwner = "core"

// FilterFunc is a filter callback: it receives the current value plus any
// extra arguments and returns the (possibly modified) value.
type FilterFunc func(ctx context.Context, value any, args ...any) any

// ActionFunc is an action callback.
type ActionFunc func(ctx context.Context, args ...any)

// HookID identifies a single registration so it can be removed again.
type HookID uint64

// hookEntry is one registered callback. Exactly one of filter/action is set.
type hookEntry struct {
	id       HookID
	owner    string
	priority int
	filter   FilterFunc
	action   ActionFunc
}

// Registry is a WordPress-style hook registry. Callbacks run in ascending
// priority order and, within a priority, in registration order. As in
// WordPress, actions and filters share one namespace: DoAction runs filter
// callbacks too (their return value is discarded) and ApplyFilters runs action
// callbacks with the value as their first argument (leaving it unchanged).
//
// A Registry is safe for concurrent use. Dispatch works on a snapshot, so
// callbacks may add or remove hooks while running; changes apply to the next
// dispatch.
type Registry struct {
	mu     sync.RWMutex
	hooks  map[string][]*hookEntry
	nextID HookID
	fired  map[string]int
}

// NewRegistry returns an empty hook registry.
func NewRegistry() *Registry {
	return &Registry{hooks: map[string][]*hookEntry{}, fired: map[string]int{}}
}

// Scope registers hooks on behalf of a single owner (a plugin slug or
// CoreOwner), so that RemoveAll(owner) can later unregister all of them.
type Scope struct {
	r     *Registry
	owner string
}

// Owner returns a Scope that records owner on every registration.
func (r *Registry) Owner(owner string) Scope { return Scope{r: r, owner: owner} }

// AddAction registers a core-owned action callback.
func (r *Registry) AddAction(name string, priority int, fn ActionFunc) HookID {
	return r.Owner(CoreOwner).AddAction(name, priority, fn)
}

// AddFilter registers a core-owned filter callback.
func (r *Registry) AddFilter(name string, priority int, fn FilterFunc) HookID {
	return r.Owner(CoreOwner).AddFilter(name, priority, fn)
}

// AddAction registers an action callback owned by s's owner.
func (s Scope) AddAction(name string, priority int, fn ActionFunc) HookID {
	return s.r.add(name, &hookEntry{owner: s.owner, priority: priority, action: fn})
}

// AddFilter registers a filter callback owned by s's owner.
func (s Scope) AddFilter(name string, priority int, fn FilterFunc) HookID {
	return s.r.add(name, &hookEntry{owner: s.owner, priority: priority, filter: fn})
}

func (r *Registry) add(name string, e *hookEntry) HookID {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	e.id = r.nextID
	list := r.hooks[name]
	// Insert after every entry with priority <= e.priority to keep
	// registration order stable within a priority.
	i := sort.Search(len(list), func(i int) bool { return list[i].priority > e.priority })
	list = append(list, nil)
	copy(list[i+1:], list[i:])
	list[i] = e
	r.hooks[name] = list
	return e.id
}

// Remove unregisters a single callback. It reports whether it was found.
func (r *Registry) Remove(id HookID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, list := range r.hooks {
		for i, e := range list {
			if e.id == id {
				r.setList(name, append(list[:i:i], list[i+1:]...))
				return true
			}
		}
	}
	return false
}

// RemoveAll unregisters every callback registered by owner and returns how
// many were removed.
func (r *Registry) RemoveAll(owner string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for name, list := range r.hooks {
		kept := make([]*hookEntry, 0, len(list))
		for _, e := range list {
			if e.owner == owner {
				removed++
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) != len(list) {
			r.setList(name, kept)
		}
	}
	return removed
}

// setList replaces a hook's callback list; callers hold r.mu.
func (r *Registry) setList(name string, list []*hookEntry) {
	if len(list) == 0 {
		delete(r.hooks, name)
		return
	}
	r.hooks[name] = list
}

// HasFilter reports whether any callback (filter or action) is registered for
// name.
func (r *Registry) HasFilter(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.hooks[name]) > 0
}

// HasAction is an alias of HasFilter, as in WordPress.
func (r *Registry) HasAction(name string) bool { return r.HasFilter(name) }

// DidAction returns how many times DoAction has been called for name.
func (r *Registry) DidAction(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fired[name]
}

// Owners returns the owners with at least one callback on name, in
// execution order (duplicates removed).
func (r *Registry) Owners(name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range r.snapshot(name, false) {
		if !seen[e.owner] {
			seen[e.owner] = true
			out = append(out, e.owner)
		}
	}
	return out
}

func (r *Registry) snapshot(name string, countAction bool) []*hookEntry {
	if countAction {
		r.mu.Lock()
		r.fired[name]++
		list := append([]*hookEntry(nil), r.hooks[name]...)
		r.mu.Unlock()
		return list
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*hookEntry(nil), r.hooks[name]...)
}

// DoAction runs every callback registered for name.
func (r *Registry) DoAction(ctx context.Context, name string, args ...any) {
	for _, e := range r.snapshot(name, true) {
		if e.action != nil {
			e.action(ctx, args...)
			continue
		}
		var value any
		var rest []any
		if len(args) > 0 {
			value, rest = args[0], args[1:]
		}
		e.filter(ctx, value, rest...)
	}
}

// DoActionOwner runs only the callbacks owner registered for name (a plugin's
// own scheduled event must not reach other plugins listening on the same name).
// It reports whether owner had any callback there.
func (r *Registry) DoActionOwner(ctx context.Context, owner, name string, args ...any) bool {
	ran := false
	for _, e := range r.snapshot(name, true) {
		if e.owner != owner {
			continue
		}
		ran = true
		if e.action != nil {
			e.action(ctx, args...)
			continue
		}
		var value any
		var rest []any
		if len(args) > 0 {
			value, rest = args[0], args[1:]
		}
		e.filter(ctx, value, rest...)
	}
	return ran
}

// ApplyFilters passes value through every callback registered for name and
// returns the result.
func (r *Registry) ApplyFilters(ctx context.Context, name string, value any, args ...any) any {
	for _, e := range r.snapshot(name, false) {
		if e.filter != nil {
			value = e.filter(ctx, value, args...)
			continue
		}
		e.action(ctx, append([]any{value}, args...)...)
	}
	return value
}
