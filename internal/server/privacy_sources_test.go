package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// privacyFixture is a site with two people, Ada (the subject of the requests) and Bob (whose rows
// must survive), each with an account.
type privacyFixture struct {
	srv          *Server
	ctx          context.Context
	ada, bob     int64
	adaEmail     string
	bobEmail     string
	exec         func(sql string, args ...any)
	count        func(sql string, args ...any) int
	groupsByID   func() map[string][]([]exportItem)
	runErase     func() eraseResult
	exportString func() string
}

func newPrivacyFixture(t *testing.T) *privacyFixture {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	f := &privacyFixture{srv: srv, ctx: context.Background(), adaEmail: "Ada@Example.test", bobEmail: "bob@example.test"}
	f.exec = func(sql string, args ...any) {
		t.Helper()
		if _, err := srv.store.Pool.Exec(f.ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	f.count = func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := srv.store.Pool.QueryRow(f.ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	for _, u := range []struct {
		login, email string
		id           *int64
	}{{"ada", "ada@example.test", &f.ada}, {"bob", f.bobEmail, &f.bob}} {
		if err := srv.store.Pool.QueryRow(f.ctx, `INSERT INTO users (login, email, password_hash, nicename, display_name) VALUES ($1, $2, '', $1, $1) RETURNING id`,
			u.login, u.email).Scan(u.id); err != nil {
			t.Fatal(err)
		}
	}
	f.groupsByID = func() map[string][]([]exportItem) {
		out := map[string][]([]exportItem){}
		for _, g := range srv.registeredPersonalData(f.ctx, f.adaEmail) {
			out[g.ID] = append(out[g.ID], g.Items...)
		}
		return out
	}
	f.exportString = func() string {
		return personalDataExportHTML(f.adaEmail, srv.registeredPersonalData(f.ctx, f.adaEmail))
	}
	f.runErase = func() eraseResult {
		t.Helper()
		res := srv.erasePersonalDataRegistered(f.ctx, f.adaEmail)
		if res.Err != nil {
			t.Fatalf("erase: %v", res.Err)
		}
		return res
	}
	return f
}

func TestPrivacyFieldHelpers(t *testing.T) {
	if got := gooCustomerFieldLabel("billing_first_name"); got != "Billing First Name" {
		t.Errorf("label %q", got)
	}
	items := fieldItems(nil, []byte(`{"b":"2","a":"1"}`))
	if len(items) != 2 || items[0].Name != "a" || items[1].Value != "2" {
		t.Errorf("fields %v", items)
	}
	if items := fieldItems(nil, []byte(`{"n":3,"s":["x"]}`)); len(items) != 2 || items[0].Value != "3" {
		t.Errorf("non-string fields %v", items)
	}
	if items := fieldItems([]exportItem{{"k", "v"}}, []byte(`not json`)); len(items) != 1 {
		t.Errorf("bad JSON %v", items)
	}
}

func TestPrivacyContactFormEntries(t *testing.T) {
	f := newPrivacyFixture(t)
	f.exec(`INSERT INTO gontact_entries (form_id, form_title, posted, ip, user_agent, page_url) VALUES
		(1, 'Contact', '{"your-email":" ada@example.test ","your-message":"Hello from Ada"}', '198.51.100.7', 'UA-Ada', '/contact'),
		(1, 'Contact', '{"your-email":"bob@example.test","your-message":"Hello from Bob, cc ada@example.test"}', '198.51.100.8', 'UA-Bob', '/contact')`)
	g := f.groupsByID()["pursae-contact-form-entries"]
	if len(g) != 1 {
		t.Fatalf("export has %d entries, want Ada's one", len(g))
	}
	if html := f.exportString(); !strings.Contains(html, "Hello from Ada") || !strings.Contains(html, "198.51.100.7") || strings.Contains(html, "Hello from Bob") {
		t.Errorf("export content wrong")
	}
	res := f.runErase()
	if res.Removed < 1 {
		t.Errorf("erase reported %+v", res)
	}
	if n := f.count(`SELECT count(*) FROM gontact_entries WHERE posted->>'your-message' = 'Hello from Ada'`); n != 0 {
		t.Error("Ada's entry survived the erasure")
	}
	if n := f.count(`SELECT count(*) FROM gontact_entries WHERE posted->>'your-email' = 'bob@example.test'`); n != 1 {
		t.Error("Bob's entry was erased (it only mentions Ada in the text)")
	}
}

func TestPrivacyWebBuilderSubmissions(t *testing.T) {
	f := newPrivacyFixture(t)
	f.exec(`INSERT INTO web_builder_form_submissions (post_id, section_id, form_title, page_url, fields, ip) VALUES
		(1, 's1', 'Quote', '/quote', '{"email":"ADA@example.test","name":"Ada L."}', '203.0.113.1'),
		(1, 's1', 'Quote', '/quote', '{"email":"bob@example.test","name":"Bob"}', '203.0.113.2')`)
	g := f.groupsByID()["pursae-web-builder-submissions"]
	if len(g) != 1 || !strings.Contains(f.exportString(), "Ada L.") {
		t.Fatalf("export: %v", g)
	}
	f.runErase()
	if n := f.count(`SELECT count(*) FROM web_builder_form_submissions`); n != 1 {
		t.Fatalf("%d submissions left, want Bob's", n)
	}
	if n := f.count(`SELECT count(*) FROM web_builder_form_submissions WHERE fields->>'name' = 'Bob'`); n != 1 {
		t.Error("Bob's submission was erased")
	}
}

func TestPrivacyMaintenanceSubscribers(t *testing.T) {
	f := newPrivacyFixture(t)
	// The plugin's table does not exist until the plugin is used: nothing fails.
	if g := f.groupsByID()["pursae-maintenance-subscribers"]; len(g) != 0 {
		t.Fatal("export without the table")
	}
	f.runErase()
	if err := maintEnsureTable(f.ctx, f.srv.store); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO maintenance_subscribers (email, consent) VALUES ('ada@example.test', true), ('bob@example.test', false)`)
	if g := f.groupsByID()["pursae-maintenance-subscribers"]; len(g) != 1 {
		t.Fatalf("export: %v", g)
	}
	if res := f.runErase(); res.Removed < 1 {
		t.Errorf("erase reported %+v", res)
	}
	if n := f.count(`SELECT count(*) FROM maintenance_subscribers WHERE email = 'ada@example.test'`); n != 0 {
		t.Error("Ada is still subscribed")
	}
	if n := f.count(`SELECT count(*) FROM maintenance_subscribers WHERE email = 'bob@example.test'`); n != 1 {
		t.Error("Bob was unsubscribed")
	}
}

func TestPrivacyForum(t *testing.T) {
	f := newPrivacyFixture(t)
	var adaTopic, bobTopic, adaReply int64
	insert := func(typ string, author, parent int64, title string, id *int64) {
		t.Helper()
		if err := f.srv.store.Pool.QueryRow(f.ctx, `INSERT INTO posts (type, author_id, parent_id, slug, title, content, status, meta)
			VALUES ($1, $2, NULLIF($3, 0), $4, $5, $5 || ' body', 'publish', jsonb_build_object('`+forumMetaIP+`', $6::text)) RETURNING id`,
			typ, author, parent, strings.ToLower(strings.ReplaceAll(title, " ", "-")), title, fmt.Sprintf("192.0.2.%d", author)).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	insert("topic", f.ada, 0, "Ada topic", &adaTopic)
	insert("topic", f.bob, 0, "Bob topic", &bobTopic)
	insert("reply", f.ada, bobTopic, "Ada reply", &adaReply)
	f.exec(`INSERT INTO forum_subscriptions (topic_id, user_id) VALUES ($1, $2), ($1, $3)`, bobTopic, f.ada, f.bob)
	f.exec(`INSERT INTO forum_likes (post_id, user_id) VALUES ($1, $2), ($3, $4)`, bobTopic, f.ada, adaTopic, f.bob)
	f.exec(`INSERT INTO forum_reports (post_id, user_id, reason, note) VALUES ($1, $2, 'spam', 'Ada thinks so'), ($3, $4, 'rude', 'Bob thinks so')`, bobTopic, f.ada, adaTopic, f.bob)
	f.exec(`INSERT INTO forum_points (user_id, kind, points, post_id) VALUES ($1, 'topic', 5, $2), ($3, 'topic', 5, $4)`, f.ada, adaTopic, f.bob, bobTopic)
	f.exec(`UPDATE users SET meta = coalesce(meta, '{}') || jsonb_build_object('`+forumUMetaDraft+`', 'draft text', '`+forumUMetaGoogleEmail+`', 'ada@gmail.test') WHERE id IN ($1, $2)`, f.ada, f.bob)

	groups := f.groupsByID()
	for id, want := range map[string]int{"pursae-forum-posts": 2, "pursae-forum-subscriptions": 1, "pursae-forum-likes": 1,
		"pursae-forum-reports": 1, "pursae-forum-points": 1, "pursae-forum-profile": 1} {
		if len(groups[id]) != want {
			t.Errorf("%s: %d items, want %d", id, len(groups[id]), want)
		}
	}
	html := f.exportString()
	for _, want := range []string{"Ada topic body", "Ada reply body", "192.0.2.", "Ada thinks so", "ada@gmail.test"} {
		if !strings.Contains(html, want) {
			t.Errorf("export misses %q", want)
		}
	}
	if strings.Contains(html, "Bob topic body") || strings.Contains(html, "Bob thinks so") {
		t.Error("export holds Bob's data")
	}

	res := f.runErase()
	if res.Retained < 2 || len(res.Messages) == 0 {
		t.Errorf("erase should report the posts it keeps: %+v", res)
	}
	if n := f.count(`SELECT count(*) FROM posts WHERE author_id = $1 AND meta ? '`+forumMetaIP+`'`, f.ada); n != 0 {
		t.Error("Ada's posts still carry her IP")
	}
	if n := f.count(`SELECT count(*) FROM posts WHERE author_id = $1 AND meta ? '`+forumMetaIP+`'`, f.bob); n != 1 {
		t.Error("Bob's IP was removed")
	}
	if n := f.count(`SELECT count(*) FROM forum_subscriptions WHERE user_id = $1`, f.ada); n != 0 {
		t.Error("Ada's subscription survived")
	}
	if n := f.count(`SELECT count(*) FROM forum_subscriptions WHERE user_id = $1`, f.bob); n != 1 {
		t.Error("Bob's subscription was removed")
	}
	if n := f.count(`SELECT count(*) FROM forum_reports WHERE user_id IS NULL AND note = '' AND reason = 'spam'`); n != 1 {
		t.Error("Ada's report was not detached")
	}
	if n := f.count(`SELECT count(*) FROM forum_reports WHERE user_id = $1 AND note = 'Bob thinks so'`, f.bob); n != 1 {
		t.Error("Bob's report changed")
	}
	if n := f.count(`SELECT count(*) FROM users WHERE meta ? '` + forumUMetaDraft + `'`); n != 1 {
		t.Error("the draft erasure touched the wrong accounts")
	}
}

func TestPrivacyCommerceCustomerData(t *testing.T) {
	f := newPrivacyFixture(t)
	f.exec(`UPDATE users SET meta = coalesce(meta, '{}') || '{"billing_first_name":"Ada","billing_phone":"555-0100","shipping_city":"London","locale_pref":"x"}' WHERE id = $1`, f.ada)
	f.exec(`UPDATE users SET meta = coalesce(meta, '{}') || '{"billing_first_name":"Bob"}' WHERE id = $1`, f.bob)
	f.exec(`INSERT INTO goo_payment_tokens (user_id, gateway_id, token, brand, last4, exp_month, exp_year) VALUES ($1, 'stripe', 'tok_a', 'visa', '4242', 1, 2030), ($2, 'stripe', 'tok_b', 'visa', '1111', 1, 2030)`, f.ada, f.bob)
	f.exec(`INSERT INTO goo_gift_cards (code, balance, recipient, recipient_name, sender, sender_email, message, status) VALUES
		('CARD-USED-0001', 0, 'friend@example.test', 'Friend', 'Ada', 'ada@example.test', 'For you', 'redeemed'),
		('CARD-OPEN-0002', 10, 'ada@example.test', 'Ada', 'Bob', 'bob@example.test', 'Enjoy', 'active'),
		('CARD-BOBS-0003', 0, 'carol@example.test', 'Carol', 'Bob', 'bob@example.test', 'Hi', 'redeemed')`)
	g := f.groupsByID()
	if len(g["woocommerce-customer-data"]) != 1 || len(g["woocommerce-customer-tokens"]) != 1 || len(g["woocommerce-gift-cards"]) != 2 {
		t.Fatalf("export groups: customer %d, tokens %d, gift cards %d", len(g["woocommerce-customer-data"]), len(g["woocommerce-customer-tokens"]), len(g["woocommerce-gift-cards"]))
	}
	html := f.exportString()
	if !strings.Contains(html, "555-0100") || !strings.Contains(html, "4242") || strings.Contains(html, "CARD-USED-0001") || strings.Contains(html, "1111") {
		t.Error("export content wrong (gift card codes must be shortened, Bob's card absent)")
	}
	res := f.runErase()
	if res.Retained < 1 || !strings.Contains(strings.Join(res.Messages, " "), "Gift cards") {
		t.Errorf("open gift card should be reported as retained: %+v", res)
	}
	if n := f.count(`SELECT count(*) FROM users WHERE id = $1 AND (meta ? 'billing_first_name' OR meta ? 'shipping_city')`, f.ada); n != 0 {
		t.Error("Ada's addresses survived")
	}
	if n := f.count(`SELECT count(*) FROM users WHERE id = $1 AND meta ? 'locale_pref'`, f.ada); n != 1 {
		t.Error("other account meta was removed")
	}
	if n := f.count(`SELECT count(*) FROM users WHERE id = $1 AND meta->>'billing_first_name' = 'Bob'`, f.bob); n != 1 {
		t.Error("Bob's address was removed")
	}
	if n := f.count(`SELECT count(*) FROM goo_payment_tokens`); n != 1 {
		t.Errorf("%d tokens left, want Bob's", n)
	}
	if n := f.count(`SELECT count(*) FROM goo_gift_cards WHERE code = 'CARD-USED-0001' AND sender = '' AND sender_email = '' AND recipient = 'friend@example.test'`); n != 1 {
		t.Error("Ada's side of the used card was not cleared, or the recipient's was")
	}
	if n := f.count(`SELECT count(*) FROM goo_gift_cards WHERE code = 'CARD-OPEN-0002' AND recipient = 'ada@example.test'`); n != 1 {
		t.Error("an open gift card lost its recipient")
	}
	if n := f.count(`SELECT count(*) FROM goo_gift_cards WHERE code = 'CARD-BOBS-0003' AND sender_email = 'bob@example.test' AND message = 'Hi'`); n != 1 {
		t.Error("Bob's card changed")
	}
}

func TestPrivacyMailLogReplyToAndJobs(t *testing.T) {
	f := newPrivacyFixture(t)
	f.exec(`INSERT INTO wpmailsmtp_emails_log (subject, to_addrs, reply_to) VALUES
		('Contact: Ada', ARRAY['owner@example.test'], 'Ada Lovelace <ada@example.test>'),
		('Contact: Bob', ARRAY['owner@example.test'], 'bob@example.test')`)
	f.exec(`INSERT INTO jobs (name, payload, status, run_at) VALUES
		('wp_mail', '{"To":["ada@example.test"],"Subject":"done"}', 'done', now()),
		('wp_mail', '{"To":["owner@example.test"],"ReplyTo":"ada@example.test","Subject":"failed"}', 'failed', now()),
		('wp_mail', '{"To":["ada@example.test"],"Subject":"queued"}', 'queued', now()),
		('wp_mail', '{"To":["bob@example.test"],"Subject":"bob"}', 'done', now())`)
	if g := f.groupsByID()["email-log"]; len(g) != 1 {
		t.Fatalf("e-mail log export: %d rows, want the one answering to Ada", len(g))
	}
	f.runErase()
	if n := f.count(`SELECT count(*) FROM wpmailsmtp_emails_log`); n != 1 {
		t.Errorf("%d log rows left, want Bob's", n)
	}
	var left []string
	rows, err := f.srv.store.Pool.Query(f.ctx, `SELECT payload->>'Subject' FROM jobs WHERE name = 'wp_mail' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		left = append(left, s)
	}
	rows.Close()
	if strings.Join(left, ",") != "queued,bob" {
		t.Errorf("mail jobs left %v, want the queued one and Bob's", left)
	}
}

func TestPrivacySecurityLogsRetained(t *testing.T) {
	f := newPrivacyFixture(t)
	f.exec(`INSERT INTO login_attempts (ip, login, success, pending) VALUES ('203.0.113.9', 'ada', false, false), ('203.0.113.10', 'bob', false, false)`)
	f.exec(`INSERT INTO adminsec_log (event, severity, user_id, username, ip, user_agent, message) VALUES ('login', 'info', $1, 'ada', '203.0.113.9', 'UA', 'signed in'),
		('login', 'info', $2, 'bob', '203.0.113.10', 'UA', 'signed in')`, f.ada, f.bob)
	g := f.groupsByID()
	if len(g["pursae-login-attempts"]) != 1 || len(g["pursae-security-log"]) != 1 {
		t.Fatalf("security export: attempts %d, log %d", len(g["pursae-login-attempts"]), len(g["pursae-security-log"]))
	}
	if strings.Contains(f.exportString(), "203.0.113.10") {
		t.Error("export holds Bob's address")
	}
	res := f.runErase()
	if res.Retained < 2 || !strings.Contains(strings.Join(res.Messages, " "), "Security logs") {
		t.Errorf("security logs should be reported as retained: %+v", res)
	}
	if n := f.count(`SELECT count(*) FROM login_attempts WHERE login IN ('ada', 'bob')`) + f.count(`SELECT count(*) FROM adminsec_log WHERE event = 'login'`); n != 4 {
		t.Errorf("security logs changed: %d rows", n)
	}
}
