package server

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// A profile picture whose avatar_id meta is a numeric string (wpgo user meta update, WordPress imports)
// is shown in the profile form and survives a profile save that leaves the picture alone.
func TestProfileAvatarStringMetaKept(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	img, err := srv.insertAttachmentFile(ctx, "me.png", bytes.NewReader(forumTestImage(t, 100, 100, "png", false)), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(img.ID, 10)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || jsonb_build_object('avatar_id', $2::text) WHERE id = $1`, 1, id); err != nil {
		t.Fatal(err)
	}

	_, body, _ := get(t, c, base+"/wp-admin/profile")
	if !strings.Contains(body, `name="avatar_id" id="avatar_id" value="`+id+`"`) {
		t.Fatalf("profile form avatar_id is not %s: %s", id, regexpFirst(t, body, `(<input type="hidden" name="avatar_id"[^>]*>)`))
	}
	// The browser posts the hidden field back as rendered.
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "from": {"profile"}, "checkuser_id": {"1"}, "email": {"admin@example.test"},
		"nickname": {"admin"}, "display_name": {"admin"}, "avatar_id": {regexpFirst(t, body, `name="avatar_id" id="avatar_id" value="(\d+)"`)}}
	if code, _, _ := post(t, c, base+"/wp-admin/profile", form); code != http.StatusFound {
		t.Fatalf("profile save: %d", code)
	}
	u, _ := srv.store.UserByID(ctx, 1)
	if avatarIDOf(u) != img.ID {
		t.Fatalf("avatar_id after save = %v, want %d", u.Meta["avatar_id"], img.ID)
	}
}
