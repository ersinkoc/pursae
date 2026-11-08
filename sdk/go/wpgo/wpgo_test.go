package wpgo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDispatchFilterAndAction(t *testing.T) {
	AddFilter("test_upper", 10, func(ctx Context, v string, args []any) string {
		return strings.ToUpper(v) + ctx.Hook
	})
	var got []any
	AddAction("test_action", 10, func(_ Context, args []any) { got = args })
	Init()

	callback := func(hook string) string {
		mu.Lock()
		defer mu.Unlock()
		for name := range callbacks {
			if strings.HasPrefix(name, hook+"#") {
				return name
			}
		}
		t.Fatalf("no callback for %s", hook)
		return ""
	}

	out := Dispatch([]byte(`{"callback":"` + callback("test_upper") + `","hook":"test_upper","value":"hi","args":[]}`))
	var resp handleResponse
	if err := json.Unmarshal(out, &resp); err != nil || string(resp.Value) != `"HItest_upper"` {
		t.Fatalf("filter response %s (%v)", out, err)
	}

	Dispatch([]byte(`{"callback":"` + callback("test_action") + `","hook":"test_action","args":[1,"x"]}`))
	if len(got) != 2 || got[0] != 1.0 || got[1] != "x" {
		t.Fatalf("action args %v", got)
	}
}

func TestDispatchRecoversPanics(t *testing.T) {
	AddFilter("test_panic", 10, func(Context, string, []any) string { panic("boom") })
	Init()
	var name string
	mu.Lock()
	for n := range callbacks {
		if strings.HasPrefix(n, "test_panic#") {
			name = n
		}
	}
	mu.Unlock()
	out := Dispatch([]byte(`{"callback":"` + name + `","hook":"test_panic","value":"v"}`))
	if !strings.Contains(string(out), "boom") {
		t.Fatalf("panic not reported: %s", out)
	}
	if !strings.Contains(string(Dispatch([]byte(`{"callback":"nope"}`))), "unknown callback") {
		t.Fatal("unknown callback not reported")
	}
}

func TestHostFunctionsOutsideHost(t *testing.T) {
	if _, _, err := GetOption("x"); err != ErrNotInHost {
		t.Fatalf("GetOption outside host: %v", err)
	}
	if err := (&Error{Code: CodePermissionDenied}); !IsPermissionDenied(err) {
		t.Fatal("IsPermissionDenied")
	}
}

func TestTranslateOutsideHost(t *testing.T) {
	if got := T("Read more"); got != "Read more" {
		t.Fatalf("T = %q", got)
	}
	if got := X("Post", "noun"); got != "Post" {
		t.Fatalf("X = %q", got)
	}
	if got := T(""); got != "" {
		t.Fatalf("T empty = %q", got)
	}
}
