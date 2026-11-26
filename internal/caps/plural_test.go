package caps

import "testing"

// A registered capability type maps the post meta capabilities to its own primitives
// (edit_products, edit_others_products, …) while it applies, and back to the post ones when not.
func TestRegisteredTypePlural(t *testing.T) {
	on := true
	RegisterTypePluralWhen("plural_test_product", "products", func() bool { return on })
	defer delete(typePlurals, "plural_test_product")

	editor := Grants{"edit_posts": true, "edit_others_posts": true, "edit_published_posts": true, "edit_private_posts": true,
		"publish_posts": true, "delete_posts": true, "delete_others_posts": true, "delete_published_posts": true,
		"read_private_posts": true, "read": true}
	manager := Grants{"edit_products": true, "edit_others_products": true, "edit_published_products": true,
		"edit_private_products": true, "publish_products": true, "delete_products": true, "delete_others_products": true,
		"delete_published_products": true, "delete_private_products": true, "read_private_products": true, "read": true}
	published := fakePost{author: 1, status: "publish", typ: "plural_test_product"}
	private := fakePost{author: 1, status: "private", typ: "plural_test_product"}
	draft := fakePost{author: 1, status: "draft", typ: "plural_test_product"}

	if got := Plural("plural_test_product"); got != "products" {
		t.Fatalf("Plural = %q", got)
	}
	if !HasTypeCaps("plural_test_product") {
		t.Error("HasTypeCaps is false for a registered type")
	}
	for _, c := range []string{"edit_post", "delete_post", "publish_post"} {
		for _, p := range []fakePost{published, private, draft} {
			if Can(2, editor, c, p) {
				t.Errorf("editor without product capabilities passed %s on a %s product", c, p.status)
			}
			if !Can(2, manager, c, p) {
				t.Errorf("shop manager failed %s on a %s product", c, p.status)
			}
		}
	}
	if Can(2, editor, "read_post", private) || !Can(2, manager, "read_post", private) {
		t.Error("read_post on a private product must need read_private_products")
	}
	if Can(2, editor, "read_post", draft) {
		t.Error("an editor read another user's draft product")
	}
	if !Can(2, editor, "read_post", published) {
		t.Error("a published product is readable by everyone")
	}
	own := fakePost{author: 2, status: "draft", typ: "plural_test_product"}
	if !Can(2, Grants{"edit_products": true}, "edit_post", own) || Can(2, Grants{"edit_posts": true}, "edit_post", own) {
		t.Error("own draft product must need edit_products, not edit_posts")
	}
	if got := MapMetaCap(2, "edit_post", published); len(got) != 2 || got[0] != "edit_others_products" || got[1] != "edit_published_products" {
		t.Errorf("edit_post on another user's published product maps to %v", got)
	}

	// Switched off (the plugin is off): the core rules come back.
	on = false
	if Plural("plural_test_product") != "posts" || HasTypeCaps("plural_test_product") {
		t.Error("an inactive capability type still applies")
	}
	if !Can(2, editor, "edit_post", published) {
		t.Error("with the type off the post capabilities decide")
	}

	// Pages and unregistered types keep their mapping.
	if Plural("page") != "pages" || Plural("post") != "posts" || Plural("unregistered_type") != "posts" || HasTypeCaps("post") {
		t.Error("core types changed plural")
	}
}

func TestHasTypeCapsFollowsTypeCap(t *testing.T) {
	on := false
	RegisterTypeCapWhen("cap_test_type", "manage_options", func() bool { return on })
	defer delete(typeCaps, "cap_test_type")
	if HasTypeCaps("cap_test_type") {
		t.Error("inactive type capability reported")
	}
	on = true
	if !HasTypeCaps("cap_test_type") {
		t.Error("active type capability not reported")
	}
}
