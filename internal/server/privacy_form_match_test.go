package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// A stored form message is a person's when the address is in one of the form's e-mail fields or
// the person's account sent it, never because the address appears in another field. Entries stored
// before the e-mail fields were recorded (email_fields NULL) match on the fallback field names only.
func TestPrivacyFormEntriesMatchEmailFieldsOnly(t *testing.T) {
	f := newPrivacyFixture(t)
	// Contact Form entries; the message text names who the entry is.
	f.exec(`INSERT INTO gontact_entries (form_id, form_title, posted, email_fields, user_id) VALUES
		(1, 'Contact', '{"contact-address":"ada@example.test","your-message":"ada-own"}', '{contact-address}', NULL),
		(1, 'Refer', '{"your-email":"bob@example.test","friend-email":"ada@example.test","your-message":"bob-refers-ada"}', '{your-email}', NULL),
		(1, 'Contact', '{"your-email":"bob@example.test","your-message":"ada@example.test"}', '{your-email}', NULL),
		(1, 'Contact', '{"your-email":"carol@example.test","your-message":"ada-signed-in"}', '{your-email}', $1),
		(1, 'Contact', '{"note":"ada@example.test","your-message":"no-email-field"}', '{}', NULL),
		(1, 'Old', '{"your-email":"ada@example.test","your-message":"ada-old"}', NULL, NULL),
		(1, 'Old', '{"friend":"ada@example.test","your-email":"bob@example.test","your-message":"bob-old"}', NULL, NULL),
		(1, 'Bob', '{"your-email":"bob@example.test","your-message":"bob-signed-in"}', '{your-email}', $2)`, f.ada, f.bob)
	// Web Builder and code form submissions.
	f.exec(`INSERT INTO web_builder_form_submissions (post_id, section_id, form_title, fields, email_fields, user_id) VALUES
		(1, 's1', 'Quote', '{"email":"ADA@example.test","name":"ada-own"}', '{email}', NULL),
		(1, 's1', 'Quote', '{"email":"bob@example.test","message":"ada@example.test","name":"bob-message"}', '{email}', NULL),
		(1, 's2', 'Code', '{"field_work":"ada@example.test","email":"bob@example.test","name":"bob-code"}', '{email}', NULL),
		(1, 's2', 'Code', '{"name":"ada-signed-in"}', '{}', $1),
		(1, 's1', 'Quote', '{"e-mail":"ada@example.test","name":"ada-old"}', NULL, NULL),
		(1, 's1', 'Quote', '{"message":"ada@example.test","name":"bob-old"}', NULL, NULL)`, f.ada)

	names := func(sql string, args ...any) []string {
		t.Helper()
		rows, err := f.srv.store.Pool.Query(f.ctx, sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		return out
	}
	exported := func(email, group, key string) string {
		t.Helper()
		var out []string
		for _, g := range f.srv.registeredPersonalData(f.ctx, email) {
			if g.ID != group {
				continue
			}
			for _, item := range g.Items {
				for _, kv := range item {
					if kv.Name == key {
						out = append(out, kv.Value)
					}
				}
			}
		}
		return strings.Join(out, ",")
	}
	if got := exported(f.adaEmail, "pursae-contact-form-entries", "your-message"); got != "ada-own,ada-signed-in,ada-old" {
		t.Errorf("Ada's contact form export: %q", got)
	}
	if got := exported(f.bobEmail, "pursae-contact-form-entries", "your-message"); got != "bob-refers-ada,ada@example.test,bob-old,bob-signed-in" {
		t.Errorf("Bob's contact form export: %q", got)
	}
	if got := exported(f.adaEmail, "pursae-web-builder-submissions", "name"); got != "ada-own,ada-signed-in,ada-old" {
		t.Errorf("Ada's Web Builder export: %q", got)
	}
	if got := exported(f.bobEmail, "pursae-web-builder-submissions", "name"); got != "bob-message,bob-code" {
		t.Errorf("Bob's Web Builder export: %q", got)
	}

	f.runErase()
	if got := strings.Join(names(`SELECT posted->>'your-message' FROM gontact_entries ORDER BY id`), ","); got != "bob-refers-ada,ada@example.test,no-email-field,bob-old,bob-signed-in" {
		t.Errorf("contact form entries left: %q", got)
	}
	if got := strings.Join(names(`SELECT fields->>'name' FROM web_builder_form_submissions ORDER BY id`), ","); got != "bob-message,bob-code,bob-old" {
		t.Errorf("Web Builder submissions left: %q", got)
	}
}

// Webhook deliveries (copies of an entry) follow the entry's match: the recorded e-mail fields and
// account, the fallback names for deliveries queued before they were recorded.
func TestPrivacyWebhookJobsMatchEmailFieldsOnly(t *testing.T) {
	f := newPrivacyFixture(t)
	webhook := func(tag string, posted map[string]string, emailFields []string, uid int64) {
		t.Helper()
		posted["tag"] = tag
		body, _ := json.Marshal(map[string]any{"posted_data": posted})
		p := map[string]any{"url": "https://hook.example.test", "body": string(body), "form": 1}
		if emailFields != nil {
			p["email_fields"] = emailFields
		}
		if uid != 0 {
			p["user_id"] = uid
		}
		raw, _ := json.Marshal(p)
		f.exec(`INSERT INTO jobs (name, payload, status, run_at) VALUES ($1, $2, 'done', now())`, gontactWebhookJob, string(raw))
	}
	webhook("ada-own", map[string]string{"contact": "ada@example.test"}, []string{"contact"}, 0)
	webhook("bob-refers-ada", map[string]string{"your-email": "bob@example.test", "friend": "ada@example.test"}, []string{"your-email"}, 0)
	webhook("ada-signed-in", map[string]string{"your-email": "carol@example.test"}, []string{"your-email"}, f.ada)
	webhook("ada-old", map[string]string{"your-email": "ada@example.test"}, nil, 0)
	webhook("bob-old", map[string]string{"your-email": "bob@example.test", "your-message": "ada@example.test"}, nil, 0)
	f.runErase()
	var left []string
	rows, err := f.srv.store.Pool.Query(f.ctx, `SELECT (payload->>'body')::jsonb->'posted_data'->>'tag' FROM jobs WHERE name = $1 ORDER BY id`, gontactWebhookJob)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		left = append(left, s)
	}
	rows.Close()
	if got := strings.Join(left, ","); got != "bob-refers-ada,bob-old" {
		t.Errorf("webhook jobs left: %q", got)
	}
}

// The e-mail fields a Contact Form submission records: email / email* tags and the fields the mails
// take their sender, Reply-To or autoresponder recipient from; not other fields, not special tags.
func TestGontactEmailFields(t *testing.T) {
	tags := []*gfTag{{Name: "your-name", BaseType: "text"}, {Name: "your-email", BaseType: "email"}, {Name: "friend-email", BaseType: "text"},
		{Name: "work-address", BaseType: "text"}, {Name: "alt", BaseType: "text"}, {Name: "your-message", BaseType: "textarea"}, {BaseType: "submit"}}
	form := &gfForm{
		Mail: gfMail{Sender: "[_site_title] <wordpress@example.test>", Recipient: "[_site_admin_email]",
			AdditionalHeaders: "Cc: [friend-email]\nReply-To: [work-address]"},
		Mail2: gfMail{Active: true, Recipient: "[alt]", AdditionalHeaders: "Reply-To: [[your-name]]"},
	}
	sub := &gfSubmission{form: form, tags: tags}
	if got := strings.Join(sub.emailFields(), ","); got != "alt,work-address,your-email" {
		t.Errorf("email fields %q", got)
	}
	form.Mail2.Active = false
	if got := strings.Join(sub.emailFields(), ","); got != "work-address,your-email" {
		t.Errorf("email fields without Mail (2) %q", got)
	}
	if got := (&gfSubmission{form: &gfForm{}}).emailFields(); got == nil || len(got) != 0 {
		t.Errorf("no e-mail field: %#v, want an empty list", got)
	}
	if (&gfSubmission{}).userID() != nil || *(&gfSubmission{user: &store.User{ID: 7}}).userID() != 7 {
		t.Error("userID")
	}
}

func TestWebBuilderFormEmailFields(t *testing.T) {
	schema := webbuilder.FormSchema{Fields: []webbuilder.FormField{{Name: "name", Type: "text"}, {Name: "email", Type: "email"},
		{Name: "field_work", Type: "email"}, {Name: "field_friend", Type: "text"}, {Name: "message", Type: "textarea"}}}
	if got := strings.Join(wbFormEmailFields(schema), ","); got != "email,field_work" {
		t.Errorf("email fields %q", got)
	}
	if got := wbFormEmailFields(webbuilder.FormSchema{}); got == nil || len(got) != 0 {
		t.Errorf("no e-mail field: %#v", got)
	}
}

// A real Contact Form submission records its e-mail fields (the default form's your-email).
func TestGontactEntryRecordsEmailFields(t *testing.T) {
	_, _, srv, _, _, post := gontactEntryEnv(t)
	if _, out, raw := post(); out["status"] != "mail_sent" {
		t.Fatalf("submission: %s", raw)
	}
	var fields []string
	var uid *int64
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT email_fields, user_id FROM gontact_entries`).Scan(&fields, &uid); err != nil {
		t.Fatal(err)
	}
	if strings.Join(fields, ",") != "your-email" || uid != nil {
		t.Errorf("entry email_fields %v, user %v", fields, uid)
	}
}

// A Web Builder form submission records its e-mail fields and the signed-in visitor.
func TestWebBuilderSubmissionRecordsEmailFields(t *testing.T) {
	w := wbformStart(t)
	if code, out, _ := w.submit(t, nil, w.page.ID, wbfNewsletter, url.Values{"email": {"bob@example.test"}}, true); code != http.StatusOK || out["ok"] != true {
		t.Fatalf("visitor submit: %d %v", code, out)
	}
	if code, out, _ := w.submit(t, w.user, w.page.ID, wbfNewsletter, url.Values{"email": {"carol@example.test"}}, true); code != http.StatusOK || out["ok"] != true {
		t.Fatalf("signed-in submit: %d %v", code, out)
	}
	rows, err := w.srv.store.Pool.Query(context.Background(), `SELECT fields->>'email', email_fields, coalesce(user_id, 0) FROM web_builder_form_submissions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var email string
		var fields []string
		var uid int64
		if err := rows.Scan(&email, &fields, &uid); err != nil {
			t.Fatal(err)
		}
		got = append(got, email+":"+strings.Join(fields, "|")+":"+strconv.FormatBool(uid > 0))
	}
	if strings.Join(got, ",") != "bob@example.test:email:false,carol@example.test:email:true" {
		t.Errorf("submissions %v", got)
	}
}

// Deleting two accounts that liked the same forum post (Users › Delete): both deletions succeed,
// the likes stay as anonymous likes, so the post's like count and its author's points do not change.
func TestDeleteUsersWhoLikedSamePost(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	forumActivateLink(t, c, ts.URL)
	ctx := context.Background()
	people := map[string]int64{}
	for _, login := range []string{"author", "liker1", "liker2", "liker3"} {
		var id int64
		if err := srv.store.Pool.QueryRow(ctx, `INSERT INTO users (login, email, password_hash, nicename, display_name)
			VALUES ($1, $1 || '@example.test', '', $1, $1) RETURNING id`, login).Scan(&id); err != nil {
			t.Fatal(err)
		}
		people[login] = id
	}
	fid, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Lobby"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	tp, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: people["author"], Title: "Liked", Content: "<p>Liked</p>"})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"liker1", "liker2", "liker3"} {
		if _, _, err := srv.forumSetLike(ctx, &store.User{ID: people[l]}, tp.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	state := func() string {
		t.Helper()
		var pts, likesReceived int
		if err := srv.store.Pool.QueryRow(ctx, `SELECT coalesce(sum(points), 0)::int FROM forum_points WHERE user_id = $1`, people["author"]).Scan(&pts); err != nil {
			t.Fatal(err)
		}
		stats, err := srv.forumExtraStats(ctx, []int64{people["author"]})
		if err != nil {
			t.Fatal(err)
		}
		likesReceived = stats[people["author"]][1]
		return "likes=" + strconv.Itoa(srv.forumLikeCount(ctx, tp.ID)) + " received=" + strconv.Itoa(likesReceived) + " ledger=" + strconv.Itoa(pts)
	}
	before := state()
	if !strings.HasPrefix(before, "likes=3 received=3 ") {
		t.Fatalf("before: %s", before)
	}

	nonceRe := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`)
	deleteUser := func(id int64) {
		t.Helper()
		_, list, _ := get(t, c, ts.URL+"/wp-admin/users")
		bulk := nonceRe.FindStringSubmatch(list)
		if bulk == nil {
			t.Fatal("no bulk nonce on the users screen")
		}
		_, page, _ := get(t, c, ts.URL+"/wp-admin/users?action=delete&user="+strconv.FormatInt(id, 10)+"&_wpnonce="+bulk[1])
		m := nonceRe.FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no nonce on the delete screen:\n%s", page)
		}
		resp, err := c.PostForm(ts.URL+"/wp-admin/users", url.Values{"action": {"dodelete"}, "users[]": {strconv.FormatInt(id, 10)},
			"delete_option": {"delete"}, "_wpnonce": {m[1]}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "update=del") {
			t.Fatalf("delete user %d: HTTP %d %s", id, resp.StatusCode, resp.Header.Get("Location"))
		}
		var n int
		_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, id).Scan(&n)
		if n != 0 {
			t.Fatalf("user %d still exists", id)
		}
	}
	deleteUser(people["liker1"])
	deleteUser(people["liker2"])
	if after := state(); after != before {
		t.Errorf("after deleting two likers: %s, want %s", after, before)
	}
	var anon, events int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM forum_likes WHERE post_id = $1 AND user_id IS NULL`, tp.ID).Scan(&anon)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM forum_points WHERE post_id = $1 AND kind = 'like' AND actor_id IS NULL`, tp.ID).Scan(&events)
	if anon != 2 || events != 1 {
		t.Errorf("anonymous likes %d (want 2), anonymous like events %d (want 1, merged)", anon, events)
	}
	// The remaining liker can still unlike and like again.
	if _, n, err := srv.forumSetLike(ctx, &store.User{ID: people["liker3"]}, tp.ID, false); err != nil || n != 2 {
		t.Errorf("unlike: %d %v", n, err)
	}
}

// Entries and webhook deliveries stored before the e-mail fields were recorded also match on the
// tag generator's e-mail field names (email-391) and on the e-mail fields of their form's current
// template, so upgrading does not hide (or leave behind on erasure) what 0.4.x found.
func TestPrivacyLegacyEntriesMatchFormEmailFields(t *testing.T) {
	f := newPrivacyFixture(t)
	form := &gfForm{Title: "Legacy", Form: "[text* contact] [email* email-391] [textarea your-message]",
		Mail: gfMail{Sender: "[_site_title] <wordpress@example.test>", Recipient: "[_site_admin_email]",
			AdditionalHeaders: "Reply-To: [_raw_contact]"}}
	// The plugin is not activated here; its post type is all the form needs.
	f.exec(`INSERT INTO content_types (slug, name, public) VALUES ($1, 'Contact Forms', false) ON CONFLICT (slug) DO NOTHING`, gontactPostType)
	if err := f.srv.gontactSave(f.ctx, form, 0); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO gontact_entries (form_id, form_title, posted, email_fields) VALUES
		($1, 'Legacy', '{"email-391":"ada@example.test","your-message":"ada-generated"}', NULL),
		($1, 'Legacy', '{"contact":"ada@example.test","your-message":"ada-reply-to"}', NULL),
		($1, 'Legacy', '{"your-message":"ada@example.test","contact":"bob@example.test"}', NULL),
		(999999, 'Gone', '{"Email-12":"ada@example.test","your-message":"ada-gone-form"}', NULL),
		(999999, 'Gone', '{"contact":"ada@example.test","your-message":"bob-gone-form"}', NULL)`, form.ID)
	webhook := func(tag string, formID int64, posted map[string]string) {
		t.Helper()
		posted["tag"] = tag
		body, _ := json.Marshal(map[string]any{"posted_data": posted})
		raw, _ := json.Marshal(map[string]any{"url": "https://hook.example.test", "body": string(body), "form": formID})
		f.exec(`INSERT INTO jobs (name, payload, status, run_at) VALUES ($1, $2, 'failed', now())`, gontactWebhookJob, string(raw))
	}
	webhook("ada-generated", form.ID, map[string]string{"email-391": "ada@example.test"})
	webhook("ada-reply-to", form.ID, map[string]string{"contact": "ada@example.test"})
	webhook("bob-message", form.ID, map[string]string{"contact": "bob@example.test", "your-message": "ada@example.test"})

	var got []string
	for _, g := range f.srv.registeredPersonalData(f.ctx, f.adaEmail) {
		if g.ID != "pursae-contact-form-entries" {
			continue
		}
		for _, item := range g.Items {
			for _, kv := range item {
				if kv.Name == "your-message" {
					got = append(got, kv.Value)
				}
			}
		}
	}
	if s := strings.Join(got, ","); s != "ada-generated,ada-reply-to,ada-gone-form" {
		t.Errorf("Ada's legacy export: %q", s)
	}
	f.runErase()
	if n := f.count(`SELECT count(*) FROM gontact_entries`); n != 2 {
		t.Errorf("%d entries left, want Bob's two", n)
	}
	if n := f.count(`SELECT count(*) FROM jobs WHERE name = $1 AND payload->>'body' LIKE '%bob-message%'`, gontactWebhookJob); n != 1 {
		t.Error("Bob's webhook delivery was erased")
	}
	if n := f.count(`SELECT count(*) FROM jobs WHERE name = $1 AND payload->>'body' LIKE '%ada-%'`, gontactWebhookJob); n != 0 {
		t.Errorf("%d of Ada's legacy webhook deliveries left", n)
	}
}

// A field the mails use as [_raw_x] or [_format_x] in Sender, Reply-To or the Mail (2) recipient is
// an e-mail field, as the mail engine resolves those to field x.
func TestGontactEmailFieldsRawFormatTags(t *testing.T) {
	tags := []*gfTag{{Name: "contact", BaseType: "text"}, {Name: "auto", BaseType: "text"}, {Name: "other", BaseType: "text"}}
	form := &gfForm{Mail: gfMail{AdditionalHeaders: "Reply-To: [_raw_contact]"},
		Mail2: gfMail{Active: true, Recipient: `[_format_auto "x"]`, AdditionalHeaders: "Cc: [_raw_other]"}}
	if got := strings.Join(gfEmailFields(form, tags), ","); got != "auto,contact" {
		t.Errorf("email fields %q", got)
	}
}
