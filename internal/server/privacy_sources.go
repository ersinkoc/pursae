package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/wp"
)

// Personal data exporters and erasers of the built-in plugins that store what visitors and members
// type or leave behind (registered in privacy_registry.go):
//
//   - Pursae Contact Form entries (gontact_entries) and Web Builder form submissions
//     (web_builder_form_submissions, which the code-defined forms share): the submissions that carry
//     the address in one of their fields. Erased by deleting them, like Flamingo's eraser.
//   - Maintenance mode subscribers (maintenance_subscribers): deleted.
//   - Pursae Forum: the member's topics and replies, forum profile fields, likes, points,
//     subscriptions and the reports they filed. Erasure removes the author IP from their posts,
//     deletes subscriptions and the saved draft, and detaches their reports; the posts themselves,
//     likes, points and the account's forum settings stay with the account (bbPress has no eraser;
//     WordPress keeps account-bound content until the account is deleted or reassigned).
//   - Pursae Commerce customer data (billing / shipping fields of the account), saved payment
//     methods and gift cards (WooCommerce's customer data and payment token exporters / erasers). A
//     saved card an open subscription renews with is kept (deleting the row would not stop the
//     renewal, which charges the token stored on the subscription).
//   - Queued jobs: finished wp_mail jobs addressed to the person (copies of the e-mail log) or
//     contact form notifications answering to them, and finished or failed contact form webhook
//     deliveries whose posted data holds the address (copies of the entry, with the visitor's IP).
//   - Security and activity logs of the account: exported, retained (legitimate interest; each has
//     its own retention period).
//
// Everything is matched on the e-mail address (case-insensitive, whole value) and, where rows hang
// off an account, on the id of the user with that address. No other person's rows are touched.
//
// Tables with visitor data that no exporter reads, and why:
//   - analytics_events: a daily-salted visitor hash, no IP, address or account; nothing links a row
//     to a person (Pursae Analytics, docs/analytics-features.md).
//   - gooike_consent_log: the cookie consent proof (random consent id, truncated IP, hashed user
//     agent). It cannot be matched to an address, and it is the record the site must keep to prove
//     consent; it is deleted after the configured retention.
//   - seogo_404, pixelgo_log, goo_webhook_deliveries, hg_webhook_deliveries, wpmailsmtp_debug_events:
//     technical logs with retention or size limits; no address column to match on.
//   - goo_sessions: carts of open sessions, deleted when they expire.
//   - goo_order_notes, goo_bookings, goo_gift_card_activity, goo_coupons.used_by: parts of orders and
//     their accounting, which follow the order erasure setting and the account.
//   - adminsec_challenges, adminsec_lockouts: short-lived sign-in challenges and lockouts.
//   - sessions, app_passwords, oauth_tokens, adminsec_devices, adminsec_factors: the account's
//     credentials; session tokens are exported by the core, all go with the account.
//   - privacy_requests: the request record itself, as WordPress keeps it.

// privacyUserID is the id of the account with the address, or 0 (a visitor without an account).
func privacyUserID(ctx context.Context, s *Server, email string) int64 {
	var id int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1) ORDER BY id LIMIT 1`, email).Scan(&id)
	return id
}

// privacyDate formats t with the site's date and time format and zone.
func (s *Server) privacyDate(t time.Time) string {
	return wp.FormatDate(t.In(s.loc()), s.settings.String("date_format")+" "+s.settings.String("time_format"))
}

// privacyTableExists reports whether a table a plugin creates on activation is there.
func privacyTableExists(ctx context.Context, s *Server, table string) bool {
	var ok bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&ok)
	return ok
}

// privacyCollect runs sql with args and appends one item per row to g; the group is returned only
// when it has items.
func privacyCollect(ctx context.Context, s *Server, g exportGroup, sql string, args []any, scan func(pgxRows) []exportItem) []exportGroup {
	rows, err := s.store.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil
	}
	for rows.Next() {
		if item := scan(rows); item != nil {
			g.Items = append(g.Items, item)
		}
	}
	rows.Close()
	if len(g.Items) == 0 {
		return nil
	}
	return []exportGroup{g}
}

// jsonFieldsMatch is the SQL condition "one of the fields of the JSON object col is the address $1".
func jsonFieldsMatch(col string) string {
	return `EXISTS (SELECT 1 FROM jsonb_each_text(CASE WHEN jsonb_typeof(` + col + `) = 'object' THEN ` + col + ` ELSE '{}'::jsonb END) kv
		WHERE lower(btrim(kv.value)) = lower($1))`
}

// fieldItems appends the posted fields, sorted by name, as export rows.
func fieldItems(item []exportItem, raw []byte) []exportItem {
	var fields map[string]string
	if json.Unmarshal(raw, &fields) != nil {
		var loose map[string]any
		if json.Unmarshal(raw, &loose) != nil {
			return item
		}
		fields = map[string]string{}
		for k, v := range loose {
			fields[k] = fmt.Sprint(v)
		}
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		item = append(item, exportItem{k, fields[k]})
	}
	return item
}

// ---- Pursae Contact Form entries ---------------------------------------------------------------

func gontactEntriesExporter(ctx context.Context, s *Server, email string) []exportGroup {
	return privacyCollect(ctx, s, exportGroup{ID: "pursae-contact-form-entries", Label: "Contact form submissions",
		Description: "Messages sent through the contact forms of this site."},
		`SELECT id, form_title, posted, ip, user_agent, page_url, created_at FROM gontact_entries WHERE `+jsonFieldsMatch("posted")+` ORDER BY id`,
		[]any{email}, func(r pgxRows) []exportItem {
			var id int64
			var form, ip, ua, page string
			var posted []byte
			var at time.Time
			if r.Scan(&id, &form, &posted, &ip, &ua, &page, &at) != nil {
				return nil
			}
			item := []exportItem{{"Entry ID", strconv.FormatInt(id, 10)}, {"Form", form}, {"Submitted", s.privacyDate(at)},
				{"Page", page}, {"IP Address", ip}, {"User Agent", ua}}
			return fieldItems(item, posted)
		})
}

func gontactEntriesEraser(ctx context.Context, s *Server, email string) eraseResult {
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM gontact_entries WHERE `+jsonFieldsMatch("posted"), email)
	if err != nil {
		return eraseResult{Err: err}
	}
	return eraseResult{Removed: int(tag.RowsAffected())}
}

// ---- Web Builder form submissions (section forms and code-defined forms) ----------------------

func wbSubmissionsExporter(ctx context.Context, s *Server, email string) []exportGroup {
	return privacyCollect(ctx, s, exportGroup{ID: "pursae-web-builder-submissions", Label: "Web Builder form submissions",
		Description: "Messages sent through the forms of Web Builder pages."},
		`SELECT id, form_title, page_url, fields, ip, created_at FROM web_builder_form_submissions WHERE `+jsonFieldsMatch("fields")+` ORDER BY id`,
		[]any{email}, func(r pgxRows) []exportItem {
			var id int64
			var form, page, ip string
			var fields []byte
			var at time.Time
			if r.Scan(&id, &form, &page, &fields, &ip, &at) != nil {
				return nil
			}
			item := []exportItem{{"Submission ID", strconv.FormatInt(id, 10)}, {"Form", form}, {"Submitted", s.privacyDate(at)},
				{"Page", page}, {"IP Address", ip}}
			return fieldItems(item, fields)
		})
}

func wbSubmissionsEraser(ctx context.Context, s *Server, email string) eraseResult {
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM web_builder_form_submissions WHERE `+jsonFieldsMatch("fields"), email)
	if err != nil {
		return eraseResult{Err: err}
	}
	return eraseResult{Removed: int(tag.RowsAffected())}
}

// ---- Maintenance mode subscribers --------------------------------------------------------------

func maintSubscribersExporter(ctx context.Context, s *Server, email string) []exportGroup {
	if !privacyTableExists(ctx, s, "maintenance_subscribers") {
		return nil
	}
	return privacyCollect(ctx, s, exportGroup{ID: "pursae-maintenance-subscribers", Label: "Launch notification sign-up",
		Description: "The address left on the maintenance page to hear when the site opens."},
		`SELECT email, consent, pending, notified_at, created_at FROM maintenance_subscribers WHERE lower(email) = lower($1) ORDER BY id`,
		[]any{email}, func(r pgxRows) []exportItem {
			var addr string
			var consent, pending bool
			var notified *time.Time
			var at time.Time
			if r.Scan(&addr, &consent, &pending, &notified, &at) != nil {
				return nil
			}
			yesNo := func(b bool) string {
				if b {
					return "Yes"
				}
				return "No"
			}
			sent := "No"
			if notified != nil {
				sent = s.privacyDate(*notified)
			}
			return []exportItem{{"Email", addr}, {"Subscribed", s.privacyDate(at)}, {"Consent given", yesNo(consent)},
				{"Confirmed", yesNo(!pending)}, {"Notified", sent}}
		})
}

func maintSubscribersEraser(ctx context.Context, s *Server, email string) eraseResult {
	if !privacyTableExists(ctx, s, "maintenance_subscribers") {
		return eraseResult{}
	}
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM maintenance_subscribers WHERE lower(email) = lower($1)`, email)
	if err != nil {
		return eraseResult{Err: err}
	}
	return eraseResult{Removed: int(tag.RowsAffected())}
}

// ---- Pursae Forum ------------------------------------------------------------------------------

// forumPrivacyMeta are the users.meta keys the forum keeps for a member, with export labels.
var forumPrivacyMeta = [][2]string{
	{forumUMetaGoogleEmail, "Linked Google address"}, {forumUMetaEmailVerified, "Verified e-mail address"},
	{forumUMetaRules, "Community rules accepted"}, {forumUMetaDigest, "Weekly digest"}, {forumUMetaBadges, "Badges"},
	{forumUMetaAvatar, "Avatar attachment ID"}, {forumUMetaDraft, "Saved topic draft"},
}

func forumPrivacyExporter(ctx context.Context, s *Server, email string) []exportGroup {
	uid := privacyUserID(ctx, s, email)
	if uid == 0 {
		return nil
	}
	args := []any{uid}
	var groups []exportGroup
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-forum-posts", Label: "Forum topics and replies",
		Description: "Topics and replies the member wrote in the forum."},
		`SELECT id, type, slug, title, content, status, created_at, coalesce(meta->>'`+forumMetaIP+`', '') FROM posts
		WHERE author_id = $1 AND type IN ('topic', 'reply') ORDER BY id`, args, func(r pgxRows) []exportItem {
			var id int64
			var typ, slug, title, content, status, ip string
			var at time.Time
			if r.Scan(&id, &typ, &slug, &title, &content, &status, &at, &ip) != nil {
				return nil
			}
			kind, link := "Reply", s.forumURL(ctx, "r", strconv.FormatInt(id, 10))
			if typ == "topic" {
				kind, link = "Topic", s.forumURL(ctx, "t", strconv.FormatInt(id, 10), slug)
			}
			return []exportItem{{"Type", kind}, {"Title", title}, {"Content", content}, {"Status", status},
				{"Date", s.privacyDate(at)}, {"IP Address", ip}, {"URL", link}}
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-forum-profile", Label: "Forum profile",
		Description: "Forum settings of the member's account."},
		`SELECT nicename, coalesce(meta, '{}') FROM users WHERE id = $1`, args, func(r pgxRows) []exportItem {
			var nicename string
			var meta map[string]any
			if r.Scan(&nicename, &meta) != nil {
				return nil
			}
			item := []exportItem{{"Profile URL", s.forumProfileURL(ctx, nicename)}}
			for _, k := range forumPrivacyMeta {
				v, ok := meta[k[0]]
				if !ok || v == nil {
					continue
				}
				val, isStr := v.(string)
				if !isStr {
					b, _ := json.Marshal(v)
					val = string(b)
				}
				if val != "" {
					item = append(item, exportItem{k[1], val})
				}
			}
			return item
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-forum-subscriptions", Label: "Forum subscriptions",
		Description: "Topics the member gets an e-mail about when someone replies."},
		`SELECT f.topic_id, coalesce(p.title, ''), f.created_at FROM forum_subscriptions f LEFT JOIN posts p ON p.id = f.topic_id
		WHERE f.user_id = $1 ORDER BY f.created_at, f.topic_id`, args, func(r pgxRows) []exportItem {
			var id int64
			var title string
			var at time.Time
			if r.Scan(&id, &title, &at) != nil {
				return nil
			}
			return []exportItem{{"Topic ID", strconv.FormatInt(id, 10)}, {"Topic", title}, {"Subscribed", s.privacyDate(at)}}
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-forum-likes", Label: "Forum likes",
		Description: "Posts the member liked."},
		`SELECT post_id, created_at FROM forum_likes WHERE user_id = $1 ORDER BY created_at, post_id`, args, func(r pgxRows) []exportItem {
			var id int64
			var at time.Time
			if r.Scan(&id, &at) != nil {
				return nil
			}
			return []exportItem{{"Post ID", strconv.FormatInt(id, 10)}, {"Liked", s.privacyDate(at)}}
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-forum-reports", Label: "Forum reports",
		Description: "Posts the member reported to the moderators."},
		`SELECT post_id, reason, note, status, created_at FROM forum_reports WHERE user_id = $1 ORDER BY id`, args, func(r pgxRows) []exportItem {
			var id int64
			var reason, note, status string
			var at time.Time
			if r.Scan(&id, &reason, &note, &status, &at) != nil {
				return nil
			}
			return []exportItem{{"Post ID", strconv.FormatInt(id, 10)}, {"Reason", reason}, {"Note", note}, {"Status", status}, {"Reported", s.privacyDate(at)}}
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-forum-points", Label: "Forum points",
		Description: "Points the member earned in the forum."},
		`SELECT kind, points, coalesce(post_id, 0), note, created_at FROM forum_points WHERE user_id = $1 ORDER BY id`, args, func(r pgxRows) []exportItem {
			var kind, note string
			var points int
			var post int64
			var at time.Time
			if r.Scan(&kind, &points, &post, &note, &at) != nil {
				return nil
			}
			return []exportItem{{"Event", kind}, {"Points", strconv.Itoa(points)}, {"Post ID", strconv.FormatInt(post, 10)}, {"Note", note}, {"Date", s.privacyDate(at)}}
		})...)
	return groups
}

func forumPrivacyEraser(ctx context.Context, s *Server, email string) eraseResult {
	var res eraseResult
	uid := privacyUserID(ctx, s, email)
	if uid == 0 {
		return res
	}
	exec := func(sql string) int {
		if res.Err != nil {
			return 0
		}
		tag, err := s.store.Pool.Exec(ctx, sql, uid)
		if err != nil {
			res.Err = err
			return 0
		}
		return int(tag.RowsAffected())
	}
	// The author IP is moderation data, not content: it goes (WordPress anonymizes comment IPs too).
	res.Removed += exec(`UPDATE posts SET meta = meta - '` + forumMetaIP + `' WHERE author_id = $1 AND type IN ('topic', 'reply') AND meta ? '` + forumMetaIP + `'`)
	res.Removed += exec(`DELETE FROM forum_subscriptions WHERE user_id = $1`)
	// A report stays in the moderation queue, without its reporter or the reporter's note.
	res.Removed += exec(`UPDATE forum_reports SET user_id = NULL, note = '' WHERE user_id = $1`)
	res.Removed += exec(`UPDATE users SET meta = meta - '` + forumUMetaDraft + `' WHERE id = $1 AND meta ? '` + forumUMetaDraft + `'`)
	if res.Err != nil {
		return res
	}
	var posts, engagement int
	if err := s.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM posts WHERE author_id = $1 AND type IN ('topic', 'reply')),
		(SELECT count(*) FROM forum_likes WHERE user_id = $1) + (SELECT count(*) FROM forum_points WHERE user_id = $1)`, uid).Scan(&posts, &engagement); err != nil {
		res.Err = err
		return res
	}
	if posts > 0 {
		res.Retained += posts
		res.Messages = append(res.Messages, "Forum topics and replies stay published under the member's account; they are removed or reassigned when the account is deleted.")
	}
	if engagement > 0 {
		res.Retained += engagement
		res.Messages = append(res.Messages, "Forum likes and points belong to the member's account and are removed when the account is deleted.")
	}
	return res
}

// ---- Pursae Commerce: customer data, payment methods, gift cards ------------------------------

// gooCustomerMetaKeys is the SQL list of the account's billing and shipping fields.
const gooCustomerMetaKeys = `ARRAY(SELECT k FROM jsonb_object_keys(coalesce(meta, '{}')) k WHERE k LIKE 'billing\_%' OR k LIKE 'shipping\_%')`

func gooCustomerDataExporter(ctx context.Context, s *Server, email string) []exportGroup {
	uid := privacyUserID(ctx, s, email)
	if uid == 0 {
		return nil
	}
	var groups []exportGroup
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "woocommerce-customer-data", Label: "Customer Data",
		Description: "User&#8217;s WooCommerce customer data."},
		`SELECT coalesce(meta, '{}') FROM users WHERE id = $1`, []any{uid}, func(r pgxRows) []exportItem {
			var meta map[string]any
			if r.Scan(&meta) != nil {
				return nil
			}
			var keys []string
			for k, v := range meta {
				if str, ok := v.(string); ok && str != "" && (strings.HasPrefix(k, "billing_") || strings.HasPrefix(k, "shipping_")) {
					keys = append(keys, k)
				}
			}
			if len(keys) == 0 {
				return nil
			}
			sort.Strings(keys)
			var item []exportItem
			for _, k := range keys {
				item = append(item, exportItem{gooCustomerFieldLabel(k), meta[k].(string)})
			}
			return item
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "woocommerce-customer-tokens", Label: "Payment Tokens",
		Description: "User&#8217;s saved payment methods."},
		`SELECT gateway_id, type, brand, last4, exp_month, exp_year, created_at FROM goo_payment_tokens WHERE user_id = $1 ORDER BY id`,
		[]any{uid}, func(r pgxRows) []exportItem {
			var gateway, typ, brand, last4 string
			var month, year int
			var at time.Time
			if r.Scan(&gateway, &typ, &brand, &last4, &month, &year, &at) != nil {
				return nil
			}
			return []exportItem{{"Payment gateway", gateway}, {"Type", typ}, {"Card", strings.TrimSpace(brand + " ending in " + last4)},
				{"Expires", fmt.Sprintf("%02d/%d", month, year)}, {"Saved", s.privacyDate(at)}}
		})...)
	return groups
}

// gooCustomerFieldLabel turns billing_first_name into "Billing First Name".
func gooCustomerFieldLabel(k string) string {
	parts := strings.Split(k, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func gooCustomerDataEraser(ctx context.Context, s *Server, email string) eraseResult {
	var res eraseResult
	uid := privacyUserID(ctx, s, email)
	if uid == 0 {
		return res
	}
	tag, err := s.store.Pool.Exec(ctx, `UPDATE users SET meta = meta - `+gooCustomerMetaKeys+` WHERE id = $1 AND cardinality(`+gooCustomerMetaKeys+`) > 0`, uid)
	if err != nil {
		return eraseResult{Err: err}
	}
	res.Removed += int(tag.RowsAffected())
	// WooCommerce's eraser removes saved payment methods (the gateway keeps its own customer record).
	// A card an open subscription renews with stays: the renewal charges the token stored on the
	// subscription, so deleting the row would only hide a card that is still being charged.
	tag, err = s.store.Pool.Exec(ctx, `DELETE FROM goo_payment_tokens t WHERE user_id = $1 AND NOT `+gooTokenInUse, uid)
	if err != nil {
		res.Err = err
		return res
	}
	res.Removed += int(tag.RowsAffected())
	var kept int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_payment_tokens t WHERE user_id = $1 AND `+gooTokenInUse, uid).Scan(&kept); err != nil {
		res.Err = err
		return res
	}
	if kept > 0 {
		res.Retained += kept
		res.Messages = append(res.Messages, "Saved payment methods used by active subscriptions have been retained; they are removed when the subscription ends or the customer changes its payment method.")
	}
	return res
}

// gooTokenInUse is "the saved payment method t is the renewal token of an open subscription of $1".
const gooTokenInUse = `EXISTS (SELECT 1 FROM goo_subscriptions sub WHERE sub.customer_id = $1 AND sub.status IN ` + gooOpenSubscriptionStatuses + `
	AND sub.meta->>'_payment_token' = t.token)`

// gooGiftCardMatch is the gift cards the person bought ($1 sender e-mail) or received ($1 recipient,
// or $2 the account that redeemed it).
const gooGiftCardMatch = `(lower(sender_email) = lower($1) OR lower(recipient) = lower($1) OR ($2 <> 0 AND redeemed_by = $2))`

// gooGiftCardOpen is a card that still holds money (a disabled one may be enabled again).
const gooGiftCardOpen = `(status IN ('active', 'disabled') AND balance > 0 AND (expires IS NULL OR expires >= current_date))`

func gooGiftCardsExporter(ctx context.Context, s *Server, email string) []exportGroup {
	uid := privacyUserID(ctx, s, email)
	return privacyCollect(ctx, s, exportGroup{ID: "woocommerce-gift-cards", Label: "Gift cards",
		Description: "Gift cards bought by or sent to the address."},
		`SELECT code, balance::text, currency, status, recipient, recipient_name, sender, sender_email, message, created,
		lower(sender_email) = lower($1) OR lower(recipient) = lower($1) FROM goo_gift_cards
		WHERE `+gooGiftCardMatch+` ORDER BY id`, []any{email, uid}, func(r pgxRows) []exportItem {
			var code, balance, currency, status, to, toName, from, fromEmail, msg string
			var at time.Time
			var party bool
			if r.Scan(&code, &balance, &currency, &status, &to, &toName, &from, &fromEmail, &msg, &at, &party) != nil {
				return nil
			}
			// The code is a bearer credential: the export shows its end only.
			if len(code) > 4 {
				code = "…" + code[len(code)-4:]
			}
			item := []exportItem{{"Card", code}, {"Balance", balance + " " + currency}, {"Status", status}}
			// A card the person only redeemed: the sender, the recipient and their message are
			// other people's data.
			if party {
				item = append(item, exportItem{"Recipient", to}, exportItem{"Recipient name", toName}, exportItem{"Sender", from},
					exportItem{"Sender email", fromEmail}, exportItem{"Message", msg})
			}
			return append(item, exportItem{"Created", s.privacyDate(at)})
		})
}

func gooGiftCardsEraser(ctx context.Context, s *Server, email string) eraseResult {
	var res eraseResult
	uid := privacyUserID(ctx, s, email)
	args := []any{email, uid}
	// Only the person's side of a card is cleared; the other party's name and address stay. The
	// message goes with the sender or the recipient (their correspondence), not with a redeemer.
	tag, err := s.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET
		sender = CASE WHEN lower(sender_email) = lower($1) THEN '' ELSE sender END,
		sender_email = CASE WHEN lower(sender_email) = lower($1) THEN '' ELSE sender_email END,
		recipient_name = CASE WHEN lower(recipient) = lower($1) THEN '' ELSE recipient_name END,
		recipient = CASE WHEN lower(recipient) = lower($1) THEN '' ELSE recipient END,
		redeemed_by = CASE WHEN $2 <> 0 AND redeemed_by = $2 THEN 0 ELSE redeemed_by END,
		message = CASE WHEN lower(sender_email) = lower($1) OR lower(recipient) = lower($1) THEN '' ELSE message END
		WHERE `+gooGiftCardMatch+` AND NOT `+gooGiftCardOpen, args...)
	if err != nil {
		return eraseResult{Err: err}
	}
	res.Removed += int(tag.RowsAffected())
	var open int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_gift_cards WHERE `+gooGiftCardMatch+` AND `+gooGiftCardOpen, args...).Scan(&open); err != nil {
		res.Err = err
		return res
	}
	if open > 0 {
		res.Retained += open
		res.Messages = append(res.Messages, "Gift cards with a remaining balance keep their sender and recipient until they are used up or expire.")
	}
	return res
}

// ---- Queued jobs -------------------------------------------------------------------------------

// mailJobMatch is a wp_mail job (a mail.Message payload) sent to the address $1, or a contact form
// notification answering to it (Kind gontactVisitorReplyKind; see smtpLogMatch for why other mail
// is not matched by Reply-To).
const mailJobMatch = `(name = 'wp_mail' AND (EXISTS (SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(payload->'To') = 'array'
	THEN payload->'To' ELSE '[]'::jsonb END) a WHERE lower(a) = lower($1))
	OR (payload->>'Kind' = '` + gontactVisitorReplyKind + `' AND (lower(coalesce(payload->>'ReplyTo', '')) = lower($1)
		OR position(lower('<' || $1 || '>') IN lower(coalesce(payload->>'ReplyTo', ''))) > 0))))`

// webhookJobMatch is a contact form webhook delivery whose posted data has a field that is the
// address $1 (the same whole-value match as the entries).
var webhookJobMatch = `(name = '` + gontactWebhookJob + `' AND CASE WHEN pg_input_is_valid(payload->>'body', 'jsonb')
	THEN ` + jsonFieldsMatch(`((payload->>'body')::jsonb->'posted_data')`) + ` ELSE false END)`

// mailJobsEraser deletes the finished jobs of the address: mail (copies of messages the e-mail log
// covers) and contact form webhook deliveries (copies of the entry, with the visitor's IP; a failed
// one is never purged by the scheduler). Queued ones are left to run: among them may be this very
// request's notices.
func mailJobsEraser(ctx context.Context, s *Server, email string) eraseResult {
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM jobs WHERE status IN ('done', 'failed') AND (`+mailJobMatch+` OR `+webhookJobMatch+`)`, email)
	if err != nil {
		return eraseResult{Err: err}
	}
	return eraseResult{Removed: int(tag.RowsAffected())}
}

// ---- Security and activity logs ----------------------------------------------------------------

// privacyLogLimit caps each log in an export (newest first).
const privacyLogLimit = 1000

func securityLogExporter(ctx context.Context, s *Server, email string) []exportGroup {
	uid := privacyUserID(ctx, s, email)
	var login string
	if uid != 0 {
		_ = s.store.Pool.QueryRow(ctx, `SELECT login FROM users WHERE id = $1`, uid).Scan(&login)
	}
	lim := strconv.Itoa(privacyLogLimit)
	var groups []exportGroup
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-login-attempts", Label: "Login attempts",
		Description: "Sign-in attempts made with the address or the account's username."},
		`SELECT ip, success, attempted_at FROM login_attempts WHERE lower(login) = lower($1) OR ($2 <> '' AND lower(login) = lower($2))
		ORDER BY attempted_at DESC LIMIT `+lim, []any{email, login}, func(r pgxRows) []exportItem {
			var ip string
			var ok bool
			var at time.Time
			if r.Scan(&ip, &ok, &at) != nil {
				return nil
			}
			result := "Failed"
			if ok {
				result = "Succeeded"
			}
			return []exportItem{{"Date", s.privacyDate(at)}, {"IP Address", ip}, {"Result", result}}
		})...)
	if uid == 0 {
		return groups
	}
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-security-log", Label: "Security log",
		Description: "Security events of the account."},
		`SELECT created_at, event, ip, user_agent, message FROM adminsec_log WHERE user_id = $1 OR ($2 <> '' AND lower(username) = lower($2))
		ORDER BY id DESC LIMIT `+lim, []any{uid, login}, func(r pgxRows) []exportItem {
			var event, ip, ua, msg string
			var at time.Time
			if r.Scan(&at, &event, &ip, &ua, &msg) != nil {
				return nil
			}
			return []exportItem{{"Date", s.privacyDate(at)}, {"Event", event}, {"IP Address", ip}, {"User Agent", ua}, {"Message", msg}}
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-known-ips", Label: "Known sign-in addresses",
		Description: "IP addresses the account signed in from."},
		`SELECT ip, first_seen, last_seen FROM adminsec_known_ips WHERE user_id = $1 ORDER BY last_seen DESC LIMIT `+lim, []any{uid}, func(r pgxRows) []exportItem {
			var ip string
			var first, last time.Time
			if r.Scan(&ip, &first, &last) != nil {
				return nil
			}
			return []exportItem{{"IP Address", ip}, {"First seen", s.privacyDate(first)}, {"Last seen", s.privacyDate(last)}}
		})...)
	groups = append(groups, privacyCollect(ctx, s, exportGroup{ID: "pursae-ai-activity", Label: "AI connector activity",
		Description: "Requests AI clients made with the account."},
		`SELECT created_at, client, tool, ip FROM mcp_activity WHERE user_id = $1 ORDER BY id DESC LIMIT `+lim, []any{uid}, func(r pgxRows) []exportItem {
			var client, tool, ip string
			var at time.Time
			if r.Scan(&at, &client, &tool, &ip) != nil {
				return nil
			}
			return []exportItem{{"Date", s.privacyDate(at)}, {"Client", client}, {"Tool", tool}, {"IP Address", ip}}
		})...)
	return groups
}

// securityLogEraser keeps the logs: they protect the site and the account (legitimate interest) and
// each is deleted after its own retention period (login attempts after a day, the security log and
// AI activity after their configured periods).
func securityLogEraser(ctx context.Context, s *Server, email string) eraseResult {
	var res eraseResult
	uid := privacyUserID(ctx, s, email)
	var login string
	if uid != 0 {
		_ = s.store.Pool.QueryRow(ctx, `SELECT login FROM users WHERE id = $1`, uid).Scan(&login)
	}
	var n int
	if err := s.store.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM login_attempts WHERE lower(login) = lower($1) OR ($3 <> '' AND lower(login) = lower($3))) +
		(SELECT count(*) FROM adminsec_log WHERE $2 <> 0 AND (user_id = $2 OR ($3 <> '' AND lower(username) = lower($3)))) +
		(SELECT count(*) FROM adminsec_known_ips WHERE $2 <> 0 AND user_id = $2) +
		(SELECT count(*) FROM mcp_activity WHERE $2 <> 0 AND user_id = $2)`, email, uid, login).Scan(&n); err != nil {
		res.Err = err
		return res
	}
	if n > 0 {
		res.Retained = n
		res.Messages = append(res.Messages, "Security logs (login attempts, security events, known sign-in addresses, AI connector activity) are kept for the site's security and deleted after their retention period.")
	}
	return res
}
