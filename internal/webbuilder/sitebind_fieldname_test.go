package webbuilder

import (
	"strings"
	"testing"
)

// SiteFieldName accepts the names BindSite stores as plain post meta, and nothing else: the front
// end reads only those from a post's meta for a code binding.
func TestSiteFieldName(t *testing.T) {
	for _, name := range []string{"design_1", "design_12", "design_3_href", "design_3_src", "design_3_aria_current",
		"list1_1", "list2_14", "list1_2_alt", "list1_2_srcset"} {
		if !SiteFieldName(name) {
			t.Errorf("%s: not a site import field", name)
		}
	}
	// Every attribute companion a field-attrs binding reads.
	for _, a := range SlotAttrs {
		if n := "design_1_" + strings.ReplaceAll(a, "-", "_"); !SiteFieldName(n) {
			t.Errorf("%s: not a site import field", n)
		}
	}
	for _, name := range []string{"", "design", "design_", "design_0", "design_x", "design_1_", "design_1_HREF", "design_1_a_b_c",
		"list1", "list1_", "list_1_1", "list0_1", "private_note", "_design_1", "design_1 ", "price", "email", "design_12345"} {
		if SiteFieldName(name) {
			t.Errorf("%q: accepted as a site import field", name)
		}
	}
}
