package plugins

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func appender(s string) FilterFunc {
	return func(_ context.Context, v any, _ ...any) any { return v.(string) + s }
}

func TestFilterPriorityAndRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	r.AddFilter("t", 20, appender("d"))
	r.AddFilter("t", DefaultPriority, appender("b"))
	r.AddFilter("t", 5, appender("a"))
	r.AddFilter("t", DefaultPriority, appender("c")) // same priority: after "b"
	r.AddFilter("t", -1, appender("0"))

	if got := r.ApplyFilters(context.Background(), "t", ""); got != "0abcd" {
		t.Fatalf("got %q, want 0abcd", got)
	}
	if got := r.ApplyFilters(context.Background(), "missing", "x"); got != "x" {
		t.Fatalf("filter without callbacks changed value: %q", got)
	}
}

func TestFilterReceivesArgs(t *testing.T) {
	r := NewRegistry()
	r.AddFilter("t", 10, func(_ context.Context, v any, args ...any) any {
		return fmt.Sprintf("%v:%v", v, args)
	})
	if got := r.ApplyFilters(context.Background(), "t", "v", 1, "two"); got != "v:[1 two]" {
		t.Fatalf("got %q", got)
	}
}

func TestDoActionOrderAndCount(t *testing.T) {
	r := NewRegistry()
	var calls []string
	r.AddAction("init", 10, func(_ context.Context, args ...any) { calls = append(calls, fmt.Sprint("b", args)) })
	r.AddAction("init", 1, func(_ context.Context, args ...any) { calls = append(calls, fmt.Sprint("a", args)) })
	// A filter registered on an action hook runs too (WordPress semantics).
	r.AddFilter("init", 50, func(_ context.Context, v any, _ ...any) any { calls = append(calls, fmt.Sprint("f", v)); return v })

	r.DoAction(context.Background(), "init", "x")
	r.DoAction(context.Background(), "init", "y")
	want := "a[x] b[x] fx a[y] b[y] fy"
	if got := strings.Join(calls, " "); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if n := r.DidAction("init"); n != 2 {
		t.Fatalf("DidAction = %d", n)
	}
}

func TestRemoveAllAndRemove(t *testing.T) {
	r := NewRegistry()
	r.Owner("plug").AddFilter("t", 10, appender("p"))
	id := r.AddFilter("t", 10, appender("c"))
	r.Owner("plug").AddAction("other", 10, func(context.Context, ...any) {})

	if !r.HasFilter("other") {
		t.Fatal("HasFilter(other) = false")
	}
	if n := r.RemoveAll("plug"); n != 2 {
		t.Fatalf("RemoveAll removed %d", n)
	}
	if r.HasAction("other") {
		t.Fatal("other still registered")
	}
	if got := r.ApplyFilters(context.Background(), "t", ""); got != "c" {
		t.Fatalf("got %q", got)
	}
	if !r.Remove(id) || r.HasFilter("t") || r.Remove(id) {
		t.Fatal("Remove did not behave")
	}
}

func TestCallbacksMayModifyRegistryWhileRunning(t *testing.T) {
	r := NewRegistry()
	r.AddFilter("t", 10, func(_ context.Context, v any, _ ...any) any {
		r.AddFilter("t", 1, appender("late"))
		r.RemoveAll(CoreOwner)
		return v.(string) + "a"
	})
	r.AddFilter("t", 20, appender("b"))
	if got := r.ApplyFilters(context.Background(), "t", ""); got != "ab" {
		t.Fatalf("got %q; changes must apply to the next dispatch only", got)
	}
	if got := r.ApplyFilters(context.Background(), "t", ""); got != "" {
		t.Fatalf("second dispatch got %q", got)
	}
}

func TestRegistryConcurrency(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			owner := fmt.Sprint("p", i)
			for j := 0; j < 200; j++ {
				r.Owner(owner).AddFilter("t", j%3, appender("x"))
				r.ApplyFilters(context.Background(), "t", "")
				r.DoAction(context.Background(), "t", "")
			}
			r.RemoveAll(owner)
		}(i)
	}
	wg.Wait()
	if r.HasFilter("t") {
		t.Fatal("hooks left behind")
	}
}

func TestDoActionOwner(t *testing.T) {
	r := NewRegistry()
	var got []string
	r.Owner("a").AddAction("cron_x", 10, func(_ context.Context, args ...any) { got = append(got, "a") })
	r.Owner("b").AddAction("cron_x", 10, func(_ context.Context, args ...any) { got = append(got, "b") })
	if !r.DoActionOwner(context.Background(), "a", "cron_x", 1) || strings.Join(got, ",") != "a" {
		t.Fatalf("owner a ran %v", got)
	}
	if r.DoActionOwner(context.Background(), "c", "cron_x") {
		t.Fatal("owner without callbacks reported as run")
	}
}
