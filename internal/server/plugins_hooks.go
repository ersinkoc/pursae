package server

import (
	"context"
	"time"

	"wpgo/internal/plugins"
	"wpgo/internal/store"
)

// More hooks the core fires for Wasm plugins: post status transitions, trash and delete, the
// the_title filter and the Pursae Commerce order actions. (save_post, comment_post, the user
// actions and the content filters are in plugins_host.go and next to the code that fires them.)

// pluginHooksFor returns the hook registry when a plugin listens on any of the names and the post
// (if given) is one plugins may see.
func (s *Server) pluginHooksFor(p *store.Post, names ...string) *plugins.Registry {
	if s.plugins == nil || (p != nil && !pluginPostTypes[p.Type]) {
		return nil
	}
	for _, n := range names {
		if s.plugins.hooks.HasAction(n) {
			return s.plugins.hooks
		}
	}
	return nil
}

// pluginPostTransition fires transition_post_status (new status, old status, the post) when the
// status of a post or page changed. A new post's old status is "new", as in WordPress.
func (s *Server) pluginPostTransition(ctx context.Context, p *store.Post, newStatus, oldStatus string) {
	if p == nil || newStatus == oldStatus || oldStatus == "" {
		return
	}
	hooks := s.pluginHooksFor(p, "transition_post_status")
	if hooks == nil {
		return
	}
	data := (&pluginHost{s: s}).postData(ctx, p)
	data.Status = newStatus
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	hooks.DoAction(ctx, "transition_post_status", newStatus, oldStatus, data)
}

// pluginPostAction fires a post lifecycle action (trashed_post, untrashed_post,
// before_delete_post, deleted_post): the post id and the post as GetPost returns it.
func (s *Server) pluginPostAction(ctx context.Context, hook string, p *store.Post) {
	if p == nil {
		return
	}
	hooks := s.pluginHooksFor(p, hook)
	if hooks == nil {
		return
	}
	data := (&pluginHost{s: s}).postData(ctx, p)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	hooks.DoAction(ctx, hook, p.ID, data)
}

// pluginPostMoved runs after a post was trashed, restored or deleted (action "trash", "untrash" or
// "delete"; p is the post as it was before): transition_post_status and trashed_post /
// untrashed_post / deleted_post. Call pluginPostAction(before_delete_post) before a delete.
func (s *Server) pluginPostMoved(ctx context.Context, action string, p *store.Post) {
	if p == nil || s.plugins == nil || !pluginPostTypes[p.Type] {
		return
	}
	switch action {
	case "trash":
		if p.Status == "trash" {
			return
		}
		s.pluginPostTransition(ctx, p, "trash", p.Status)
		s.pluginPostAction(ctx, "trashed_post", p)
	case "untrash":
		if p.Status != "trash" {
			return
		}
		s.pluginPostTransition(ctx, p, "draft", "trash")
		s.pluginPostAction(ctx, "untrashed_post", p)
	case "delete":
		s.pluginPostAction(ctx, "deleted_post", p)
	}
}

// pluginTitle passes a post title (already escaped and texturized) through the the_title filter:
// the value is the title HTML, the argument {"id", "type"}. What a plugin adds is sanitised unless
// it holds content:html.
func (s *Server) pluginTitle(ctx context.Context, title string, p *store.Post) string {
	if p == nil || s.plugins == nil || !s.plugins.hooks.HasFilter("the_title") {
		return title
	}
	out, ok := s.applyFilters(ctx, "the_title", title, map[string]any{"id": p.ID, "type": p.Type}).(string)
	if !ok {
		return title
	}
	return out
}

// Pursae Commerce ------------------------------------------------------------------

// pluginOrderData is the order object the commerce hooks carry (commerce:orders).
func pluginOrderData(o *gooOrder) map[string]any {
	items := make([]map[string]any, 0, len(o.Items))
	for _, it := range o.Items {
		if it.Type != "line_item" {
			continue
		}
		items = append(items, map[string]any{"name": it.Name, "product_id": it.ProductID, "variation_id": it.VariationID,
			"quantity": it.Quantity, "total": it.Total})
	}
	out := map[string]any{
		"id": o.ID, "status": o.Status, "currency": o.Currency, "total": o.Total, "customer_id": o.CustomerID,
		"payment_method": o.PaymentMethod, "billing": o.Billing, "shipping": o.Shipping, "items": items,
		"date_created": o.DateCreated.UTC().Format(time.RFC3339),
	}
	return out
}

func (s *Server) pluginOrderAction(ctx context.Context, hook string, o *gooOrder, args ...any) {
	if o == nil || o.Type != "shop_order" || s.plugins == nil || !s.plugins.hooks.HasAction(hook) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.plugins.hooks.DoAction(ctx, hook, append(append([]any{o.ID}, args...), pluginOrderData(o))...)
}

func init() {
	gooOn("order_created", 90, func(s *Server, ctx context.Context, arg any) {
		if o, ok := arg.(*gooOrder); ok {
			s.pluginOrderAction(ctx, "woocommerce_new_order", o)
		}
	})
	gooOn("order_status_changed", 90, func(s *Server, ctx context.Context, arg any) {
		if ev, ok := arg.(*gooStatusChange); ok {
			s.pluginOrderAction(ctx, "woocommerce_order_status_changed", ev.Order, ev.From, ev.To)
		}
	})
	gooOn("payment_complete", 90, func(s *Server, ctx context.Context, arg any) {
		if o, ok := arg.(*gooOrder); ok {
			s.pluginOrderAction(ctx, "woocommerce_payment_complete", o)
		}
	})
}
