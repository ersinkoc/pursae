package server

import (
	"reflect"
	"testing"

	"wpgo/internal/store"
)

func TestDashboardLayout(t *testing.T) {
	u := &store.User{Meta: map[string]any{}}
	n, s, c3, c4 := dashboardLayout(u, false)
	if len(n) != 3 || len(s) != 2 || c3 != nil || c4 != nil {
		t.Fatalf("default layout: %v %v %v %v", n, s, c3, c4)
	}
	// Dragging the news box to the third column and Activity to the top of the side column.
	u.Meta["meta-box-order_dashboard"] = map[string]any{
		"normal":  "dashboard_site_health,dashboard_right_now",
		"side":    "dashboard_activity,dashboard_quick_press",
		"column3": "dashboard_primary,bogus_widget",
	}
	n, s, c3, c4 = dashboardLayout(u, false)
	if !reflect.DeepEqual(n, []string{"dashboard_site_health", "dashboard_right_now"}) ||
		!reflect.DeepEqual(s, []string{"dashboard_activity", "dashboard_quick_press"}) ||
		!reflect.DeepEqual(c3, []string{"dashboard_primary"}) || c4 != nil {
		t.Fatalf("saved layout: %v %v %v %v", n, s, c3, c4)
	}
	// A widget that appears later (the SMTP box) joins its default column.
	_, s, _, _ = dashboardLayout(u, true)
	if s[len(s)-1] != "wp_mail_smtp_reports_widget_lite" {
		t.Fatalf("mail widget not appended: %v", s)
	}
}
