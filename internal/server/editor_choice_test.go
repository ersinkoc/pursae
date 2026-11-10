package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// TestUseBlockEditorDecisionTree covers the Classic Editor settings that pick the editor.
func TestUseBlockEditorDecisionTree(t *testing.T) {
	ts, _, srv := newTestServer(t, false)
	defer ts.Close()
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		settings map[string]any
		query    string
		postMeta map[string]any
		userMeta map[string]any
		typ      string
		want     bool
	}{
		{"plugin off ignores everything", map[string]any{"native_classic_editor": false, "classic-editor-replace": "classic"}, "classic-editor", nil, nil, "post", true},
		{"plugin on, site default block", map[string]any{"native_classic_editor": true, "classic-editor-replace": "block"}, "", nil, nil, "post", true},
		{"plugin on, site default classic", map[string]any{"native_classic_editor": true, "classic-editor-replace": "classic", "classic-editor-allow-users": "disallow"}, "", nil, nil, "post", false},
		{"no switching: URL param ignored", map[string]any{"native_classic_editor": true, "classic-editor-replace": "block", "classic-editor-allow-users": "disallow"}, "classic-editor", nil, nil, "post", true},
		{"switching: classic-editor param", map[string]any{"native_classic_editor": true, "classic-editor-replace": "block", "classic-editor-allow-users": "allow"}, "classic-editor", nil, nil, "post", false},
		{"switching: forget param", map[string]any{"native_classic_editor": true, "classic-editor-replace": "classic", "classic-editor-allow-users": "allow"}, "classic-editor__forget", map[string]any{"classic-editor-remember": "classic-editor"}, nil, "post", true},
		{"switching: per-post remember classic", map[string]any{"native_classic_editor": true, "classic-editor-replace": "block", "classic-editor-allow-users": "allow"}, "", map[string]any{"classic-editor-remember": "classic-editor"}, nil, "post", false},
		{"switching: per-post remember block", map[string]any{"native_classic_editor": true, "classic-editor-replace": "classic", "classic-editor-allow-users": "allow"}, "", map[string]any{"classic-editor-remember": "block-editor"}, nil, "post", true},
		{"switching: user default classic", map[string]any{"native_classic_editor": true, "classic-editor-replace": "block", "classic-editor-allow-users": "allow"}, "", nil, map[string]any{"classic-editor-settings": "classic"}, "post", false},
		{"switching: user default block", map[string]any{"native_classic_editor": true, "classic-editor-replace": "classic", "classic-editor-allow-users": "allow"}, "", nil, map[string]any{"classic-editor-settings": "block"}, "page", true},
		{"other post types never use the block editor here", map[string]any{"native_classic_editor": false}, "", nil, nil, "product", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := srv.settings.Set(context.Background(), tc.settings); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/wp-admin/post?post=1&"+tc.query, nil)
			c.Set(ctxUser, &store.User{Meta: tc.userMeta})
			p := &store.Post{Type: tc.typ, Meta: tc.postMeta}
			if got := srv.useBlockEditor(c, p); got != tc.want {
				t.Fatalf("useBlockEditor = %v, want %v", got, tc.want)
			}
		})
	}
}
