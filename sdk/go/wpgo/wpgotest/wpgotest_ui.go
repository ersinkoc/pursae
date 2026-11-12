//go:build !(wasip1 && wasm)

package wpgotest

import (
	"encoding/json"
	"errors"
	"fmt"

	"wpgo/sdk/go/wpgo"
)

// UI registrations: admin pages, dashboard widgets, meta boxes, blocks and Web Builder widgets.
// The fake host records them and can run their callbacks the way the core does.

func (h *Host) registerUI(name string, body []byte) ([]byte, error) {
	var reg map[string]any
	if err := json.Unmarshal(body, &reg); err != nil {
		return fail(wpgo.CodeInvalidRequest, err.Error())
	}
	if h.ui == nil {
		h.ui = map[string][]map[string]any{}
	}
	h.ui[name] = append(h.ui[name], reg)
	switch name {
	case "register_meta_box":
		return ok(map[string]string{"meta_prefix": "plugin_test_"})
	case "register_builder_widget":
		return ok(map[string]string{"type": "test/" + fmt.Sprint(reg["id"])})
	}
	return ok(map[string]any{})
}

// Registered returns what the plugin registered through a host function, e.g.
// "register_admin_page", in order, as decoded JSON objects.
func (h *Host) Registered(hostFunction string) []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.ui[hostFunction]...)
}

func (h *Host) findUI(hostFunction, key, value, callbackKey string) (string, error) {
	for _, r := range h.Registered(hostFunction) {
		if fmt.Sprint(r[key]) == value {
			if cb, _ := r[callbackKey].(string); cb != "" {
				return cb, nil
			}
			return "", fmt.Errorf("wpgotest: %s %q has no %s", hostFunction, value, callbackKey)
		}
	}
	return "", fmt.Errorf("wpgotest: nothing registered through %s with %s %q (did you call wpgo.Init?)", hostFunction, key, value)
}

func call(callback string, args ...any) (json.RawMessage, error) {
	req, _ := json.Marshal(map[string]any{"plugin": "test", "callback": callback, "args": args})
	var resp struct {
		Value json.RawMessage `json:"value"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(wpgo.Dispatch(req), &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Value, nil
}

// RenderAdminPage runs the callback of the admin page slug ("" for the main page).
func (h *Host) RenderAdminPage(slug string, req wpgo.AdminRequest) (wpgo.AdminResponse, error) {
	var out wpgo.AdminResponse
	cb, err := h.findUI("register_admin_page", "slug", slug, "callback")
	if err != nil {
		return out, err
	}
	req.Page = slug
	raw, err := call(cb, req)
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}

func (h *Host) renderString(fn, key, id, cbKey string, args ...any) (string, error) {
	cb, err := h.findUI(fn, key, id, cbKey)
	if err != nil {
		return "", err
	}
	raw, err := call(cb, args...)
	if err != nil {
		return "", err
	}
	var out string
	err = json.Unmarshal(raw, &out)
	return out, err
}

// RenderDashboardWidget runs a dashboard widget's callback.
func (h *Host) RenderDashboardWidget(id string, req wpgo.AdminRequest) (string, error) {
	return h.renderString("register_dashboard_widget", "id", id, "callback", req)
}

// RenderMetaBox runs a meta box's render callback.
func (h *Host) RenderMetaBox(id string, req wpgo.AdminRequest) (string, error) {
	return h.renderString("register_meta_box", "id", id, "callback", req)
}

// SaveMetaBox runs a meta box's save callback with the post id and field values.
func (h *Host) SaveMetaBox(id string, postID int64, values map[string]any) error {
	cb, err := h.findUI("register_meta_box", "id", id, "save_callback")
	if err != nil {
		return err
	}
	_, err = call(cb, postID, values)
	return err
}

// RenderBlock runs a block's render callback (name as registered).
func (h *Host) RenderBlock(name string, attrs map[string]any, content string) (string, error) {
	return h.renderString("register_block", "name", name, "callback", attrs, content)
}

// RenderBuilderWidget runs a Web Builder widget's render callback.
func (h *Host) RenderBuilderWidget(id string, settings map[string]any) (string, error) {
	return h.renderString("register_builder_widget", "id", id, "callback", settings, "")
}
