package plugins

import (
	"context"
	"errors"
	"sync"
)

// Host is implemented by the core to serve plugin host-function calls. The
// runtime checks permissions, validates and size-limits requests before
// calling it, so implementations only deal with well-formed input. plugin is
// the calling plugin's slug.
//
// ctx carries the runtime's per-call deadline; implementations must honour it
// and should pass it on to any hooks they fire, so that the runtime can detect
// (and skip) re-entrant calls into the same plugin.
type Host interface {
	// GetOption returns a site option. Implementations may hide sensitive
	// options from plugins by reporting them as not found.
	GetOption(ctx context.Context, plugin, name string) (value any, found bool, err error)
	// UpdateOption creates or updates a site option. value is JSON-decoded
	// (string, float64, bool, nil, []any or map[string]any).
	UpdateOption(ctx context.Context, plugin, name string, value any) error
	// HTTPGet performs an outbound GET request. Implementations should apply
	// destination allow-lists (e.g. block private networks) and size limits.
	HTTPGet(ctx context.Context, plugin, url string) (*HTTPResponse, error)
	// StorageGet reads from the plugin-private key/value store.
	StorageGet(ctx context.Context, plugin, key string) (value string, found bool, err error)
	// StorageSet writes to the plugin-private key/value store.
	StorageSet(ctx context.Context, plugin, key, value string) error
	// AdminNotice queues a dashboard notice. Message is plain text and must
	// be escaped by the core when rendered.
	AdminNotice(ctx context.Context, plugin string, notice Notice) error
}

// HTTPResponse is the result of Host.HTTPGet.
type HTTPResponse struct {
	Status int               `json:"status"`
	Header map[string]string `json:"header,omitempty"`
	Body   string            `json:"body"`
}

// Notice is an admin dashboard notice queued by a plugin.
type Notice struct {
	Plugin  string `json:"plugin"`
	Type    string `json:"type"` // info, success, warning or error
	Message string `json:"message"`
}

// ErrHTTPDisabled is returned by MemoryHost.HTTPGet when no HTTP function is
// configured.
var ErrHTTPDisabled = errors.New("outbound HTTP is not configured")

// MemoryHost is an in-memory Host for tests and development.
type MemoryHost struct {
	// HTTP serves HTTPGet; nil makes HTTPGet fail with ErrHTTPDisabled.
	HTTP func(ctx context.Context, url string) (*HTTPResponse, error)

	mu      sync.Mutex
	options map[string]any
	storage map[string]map[string]string
	notices []Notice
}

// NewMemoryHost returns an empty MemoryHost.
func NewMemoryHost() *MemoryHost {
	return &MemoryHost{options: map[string]any{}, storage: map[string]map[string]string{}}
}

// SetOption sets an option directly (test helper).
func (h *MemoryHost) SetOption(name string, value any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options[name] = value
}

// Option returns an option directly (test helper).
func (h *MemoryHost) Option(name string) (any, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.options[name]
	return v, ok
}

// Notices returns the queued notices.
func (h *MemoryHost) Notices() []Notice {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Notice(nil), h.notices...)
}

// GetOption implements Host.
func (h *MemoryHost) GetOption(_ context.Context, _, name string) (any, bool, error) {
	v, ok := h.Option(name)
	return v, ok, nil
}

// UpdateOption implements Host.
func (h *MemoryHost) UpdateOption(_ context.Context, _, name string, value any) error {
	h.SetOption(name, value)
	return nil
}

// HTTPGet implements Host.
func (h *MemoryHost) HTTPGet(ctx context.Context, _, url string) (*HTTPResponse, error) {
	if h.HTTP == nil {
		return nil, ErrHTTPDisabled
	}
	return h.HTTP(ctx, url)
}

// StorageGet implements Host.
func (h *MemoryHost) StorageGet(_ context.Context, plugin, key string) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.storage[plugin][key]
	return v, ok, nil
}

// StorageSet implements Host.
func (h *MemoryHost) StorageSet(_ context.Context, plugin, key, value string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.storage[plugin] == nil {
		h.storage[plugin] = map[string]string{}
	}
	h.storage[plugin][key] = value
	return nil
}

// AdminNotice implements Host.
func (h *MemoryHost) AdminNotice(_ context.Context, _ string, n Notice) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notices = append(h.notices, n)
	return nil
}
