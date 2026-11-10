package wpgo

import (
	"encoding/json"
	"strings"
	"testing"
)

// callbackFor returns the registered callback id with the given kind:name prefix.
func callbackFor(t *testing.T, prefix string) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	for name := range callbacks {
		if strings.HasPrefix(name, prefix+"#") {
			return name
		}
	}
	t.Fatalf("no callback for %s", prefix)
	return ""
}

func dispatchJSON(t *testing.T, req any) handleResponse {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var resp handleResponse
	if err := json.Unmarshal(Dispatch(b), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDispatchShortcode(t *testing.T) {
	AddShortcode("sdk_test_sc", func(ctx Context, attrs map[string]string, content string) string {
		return "<b>" + attrs["name"] + "|" + attrs["0"] + "|" + content + "</b>"
	})
	cb := callbackFor(t, "shortcode:sdk_test_sc")
	cases := []struct {
		name string
		args []any
		want string
	}{
		{"attrs and content", []any{map[string]string{"name": "x", "0": "pos"}, "inner"}, "<b>x|pos|inner</b>"},
		{"self closing", []any{map[string]string{"name": "y"}}, "<b>y||</b>"},
		{"no args", []any{}, "<b>||</b>"},
	}
	for _, c := range cases {
		resp := dispatchJSON(t, map[string]any{"callback": cb, "hook": "sdk_test_sc", "args": c.args})
		var got string
		_ = json.Unmarshal(resp.Value, &got)
		if resp.Error != "" || got != c.want {
			t.Errorf("%s: got %q err %q, want %q", c.name, got, resp.Error, c.want)
		}
	}
}

func TestDispatchRestRoute(t *testing.T) {
	RegisterRestRoute("sdk-test/v1", "/items/(?P<id>\\d+)", []string{"GET", "POST"}, func(ctx Context, req *RestRequest) *RestResponse {
		if req.Method == "DELETE" {
			return nil
		}
		body, _ := json.Marshal(map[string]string{"id": req.Params["id"], "q": req.Query["q"], "m": req.Method})
		return &RestResponse{Status: 201, Body: body, Headers: map[string]string{"X-A": "1"}}
	})
	cb := callbackFor(t, "rest:sdk-test/v1/items/(?P<id>\\d+)")

	resp := dispatchJSON(t, map[string]any{"callback": cb, "args": []any{map[string]any{
		"method": "POST", "route": "/sdk-test/v1/items/7", "params": map[string]string{"id": "7"}, "query": map[string]string{"q": "z"}}}})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var out RestResponse
	if err := json.Unmarshal(resp.Value, &out); err != nil || out.Status != 201 || out.Headers["X-A"] != "1" {
		t.Fatalf("response %s (%v)", resp.Value, err)
	}
	if string(out.Body) != `{"id":"7","m":"POST","q":"z"}` {
		t.Fatalf("body %s", out.Body)
	}

	// A nil handler result becomes 204.
	resp = dispatchJSON(t, map[string]any{"callback": cb, "args": []any{map[string]any{"method": "DELETE"}}})
	if err := json.Unmarshal(resp.Value, &out); err != nil || out.Status != 204 {
		t.Fatalf("nil response %s (%v)", resp.Value, err)
	}

	// A malformed request is reported, not swallowed.
	resp = dispatchJSON(t, map[string]any{"callback": cb, "args": []any{"not an object"}})
	if !strings.Contains(resp.Error, "decode REST request") {
		t.Fatalf("expected decode error, got %+v", resp)
	}
}

func TestDispatchCronAction(t *testing.T) {
	var got map[string]any
	AddAction("cron_sdk_test", 10, func(_ Context, args []any) {
		if len(args) > 0 {
			got, _ = args[0].(map[string]any)
		}
	})
	cb := callbackFor(t, "cron_sdk_test")
	resp := dispatchJSON(t, map[string]any{"callback": cb, "hook": "cron_sdk_test",
		"args": []any{map[string]any{"plugin": "p", "args": map[string]any{"order": 5}}}})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	inner, _ := got["args"].(map[string]any)
	if got["plugin"] != "p" || inner["order"] != 5.0 {
		t.Fatalf("cron payload %v", got)
	}
}

func TestSettingsOutsideHost(t *testing.T) {
	settingsMu.Lock()
	old := settingsPrefix
	settingsPrefix = ""
	settingsMu.Unlock()
	defer func() { settingsMu.Lock(); settingsPrefix = old; settingsMu.Unlock() }()
	if SettingName("f") != "" {
		t.Fatal("SettingName before registration must be empty")
	}
	if _, _, err := Setting("f"); err == nil {
		t.Fatal("Setting before registration must fail")
	}
	if got := SettingString("f", "dflt"); got != "dflt" {
		t.Fatalf("SettingString = %q", got)
	}
	if !SettingBool("f", true) {
		t.Fatal("SettingBool default")
	}
}
