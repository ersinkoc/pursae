package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wpgo/internal/auth"
)

// HeadlessGo webhooks: on entry create, update, publish, unpublish and delete (and, optionally,
// schema changes) every enabled webhook subscribed to the event receives a JSON POST, sent in the
// background and retried when it fails (a network error or a non-2xx answer). Each attempt is
// logged with its status code, duration and the start of the response. With a secret, the body is
// signed: X-HeadlessGo-Signature: sha256=<hex HMAC-SHA256 of the body>.

type hgWebhook struct {
	ID        int64
	Name      string
	URL       string
	Events    []string
	Headers   map[string]string
	Secret    string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

var hgWebhookEvents = []struct{ Key, Label string }{
	{"entry.create", "Entry created"}, {"entry.update", "Entry updated"}, {"entry.delete", "Entry deleted"},
	{"entry.publish", "Entry published"}, {"entry.unpublish", "Entry unpublished"}, {"schema.update", "Content type changed"},
}

// hgWebhookDelays are the waits before each attempt: three attempts in all.
var hgWebhookDelays = []time.Duration{0, 5 * time.Second, 30 * time.Second}

var hgWebhookClient = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// hgWebhookWG tracks deliveries in flight (tests wait on it).
var hgWebhookWG sync.WaitGroup

// hgReservedHeaders cannot be set as custom headers.
var hgReservedHeaders = map[string]bool{"content-type": true, "content-length": true, "host": true, "user-agent": true,
	"x-headlessgo-event": true, "x-headlessgo-delivery": true, "x-headlessgo-signature": true}

const hgWebhookCols = `id, name, url, events, headers, secret, enabled, created_at, updated_at`

func hgScanWebhook(row interface{ Scan(...any) error }) (*hgWebhook, error) {
	w := &hgWebhook{}
	var hdr []byte
	if err := row.Scan(&w.ID, &w.Name, &w.URL, &w.Events, &hdr, &w.Secret, &w.Enabled, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(hdr, &w.Headers)
	if w.Headers == nil {
		w.Headers = map[string]string{}
	}
	return w, nil
}

func (s *Server) hgWebhooks(ctx context.Context) []*hgWebhook {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+hgWebhookCols+` FROM hg_webhooks ORDER BY lower(name), id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*hgWebhook
	for rows.Next() {
		if w, err := hgScanWebhook(rows); err == nil {
			out = append(out, w)
		}
	}
	return out
}

func (s *Server) hgWebhookByID(ctx context.Context, id int64) *hgWebhook {
	w, err := hgScanWebhook(s.store.Pool.QueryRow(ctx, `SELECT `+hgWebhookCols+` FROM hg_webhooks WHERE id = $1`, id))
	if err != nil {
		return nil
	}
	return w
}

// hgSaveWebhook validates and stores a webhook (ID 0 creates it).
func (s *Server) hgSaveWebhook(ctx context.Context, w *hgWebhook) error {
	w.Name, w.URL = strings.TrimSpace(w.Name), strings.TrimSpace(w.URL)
	if w.Name == "" {
		return errors.New("The webhook needs a name.")
	}
	if !hgHTTPURL(w.URL) {
		return errors.New("The URL must be a full http:// or https:// address.")
	}
	var events []string
	for _, e := range w.Events {
		for _, k := range hgWebhookEvents {
			if k.Key == e && !containsStr(events, e) {
				events = append(events, e)
			}
		}
	}
	if len(events) == 0 {
		return errors.New("Choose at least one event.")
	}
	w.Events = events
	hdr := map[string]string{}
	for k, v := range w.Headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if hgReservedHeaders[strings.ToLower(k)] || strings.ContainsAny(k, " :\r\n") || strings.ContainsAny(v, "\r\n") {
			return errors.New("The header " + k + " cannot be set.")
		}
		hdr[k] = strings.TrimSpace(v)
	}
	w.Headers = hdr
	raw, _ := json.Marshal(hdr)
	if w.ID == 0 {
		return s.store.Pool.QueryRow(ctx, `INSERT INTO hg_webhooks(name, url, events, headers, secret, enabled) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			w.Name, w.URL, w.Events, raw, w.Secret, w.Enabled).Scan(&w.ID)
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE hg_webhooks SET name = $2, url = $3, events = $4, headers = $5, secret = $6, enabled = $7, updated_at = now() WHERE id = $1`,
		w.ID, w.Name, w.URL, w.Events, raw, w.Secret, w.Enabled)
	return err
}

func (s *Server) hgDeleteWebhook(ctx context.Context, id int64) error {
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM hg_webhooks WHERE id = $1`, id)
	return err
}

// hgEntryPayload is an entry as webhooks receive it: every public scalar attribute of its draft
// (or, for publish events, published) version, without populated relations.
func (s *Server) hgEntryPayload(ctx context.Context, sc *hgSchema, e *hgEntry, status string) map[string]any {
	h := &hgSer{s: s, reg: s.hgReg(ctx), a: &hgAuth{Kind: "token", Token: &hgToken{Kind: "full-access"}}, p: &hgReadParams{Status: status, Locale: e.Locale}}
	data := e.Version(status)
	if data == nil {
		data = e.Data
	}
	out, err := h.object(sc, data, nil, nil, 0)
	if err != nil {
		out = map[string]any{}
	}
	out["id"], out["documentId"] = e.ID, e.DocumentID
	out["createdAt"], out["updatedAt"] = hgTime(e.CreatedAt), hgTime(e.UpdatedAt)
	if e.PublishedAt != nil && status != "draft" {
		out["publishedAt"] = hgTime(*e.PublishedAt)
	} else {
		out["publishedAt"] = nil
	}
	if sc.Localized {
		out["locale"] = e.Locale
	}
	return out
}

// hgEmit sends an entry event to the subscribed webhooks.
func (s *Server) hgEmit(ctx context.Context, event string, sc *hgSchema, e *hgEntry) {
	status := "draft"
	if event == "entry.publish" || !sc.DraftAndPublish || event == "entry.delete" && e.Published != nil {
		status = "published"
	}
	s.hgEmitPayload(ctx, event, map[string]any{"model": sc.SingularName, "uid": sc.UID, "entry": s.hgEntryPayload(ctx, sc, e, status)})
}

// hgEmitSchema sends schema.update when the setting asks for it.
func (s *Server) hgEmitSchema(ctx context.Context, action string, sc *hgSchema) {
	if !s.hgLoad().SchemaWebhooks {
		return
	}
	s.hgEmitPayload(ctx, "schema.update", map[string]any{"action": action, "uid": sc.UID, "schema": sc})
}

func (s *Server) hgEmitPayload(ctx context.Context, event string, extra map[string]any) {
	hooks := s.hgWebhooks(ctx)
	for _, w := range hooks {
		if !w.Enabled || !containsStr(w.Events, event) {
			continue
		}
		payload := map[string]any{"event": event, "createdAt": hgTime(time.Now())}
		for k, v := range extra {
			payload[k] = v
		}
		body, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		hgWebhookWG.Add(1)
		go func(w *hgWebhook) {
			defer hgWebhookWG.Done()
			s.hgDeliver(context.Background(), w, event, body)
		}(w)
	}
}

// hgDelivery is one logged attempt.
type hgDelivery struct {
	ID         int64
	WebhookID  int64
	DeliveryID string
	Event      string
	Attempt    int
	StatusCode int
	DurationMS int
	Success    bool
	Error      string
	Response   string
	CreatedAt  time.Time
}

// hgDeliver sends one event with retries.
func (s *Server) hgDeliver(ctx context.Context, w *hgWebhook, event string, body []byte) {
	id := auth.RandomToken(12)
	for i, d := range hgWebhookDelays {
		if d > 0 {
			time.Sleep(d)
		}
		res := s.hgAttempt(ctx, w, event, id, body, i+1)
		if res.Success {
			return
		}
	}
}

// hgAttempt posts the body once and logs the result.
func (s *Server) hgAttempt(ctx context.Context, w *hgWebhook, event, id string, body []byte, attempt int) hgDelivery {
	res := hgDelivery{WebhookID: w.ID, DeliveryID: id, Event: event, Attempt: attempt}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		res.Error = err.Error()
	} else {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "HeadlessGo-Webhook/"+hgVersion)
		for k, v := range w.Headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("X-HeadlessGo-Event", event)
		req.Header.Set("X-HeadlessGo-Delivery", id)
		if w.Secret != "" {
			req.Header.Set("X-HeadlessGo-Signature", "sha256="+hgSign(w.Secret, body))
		}
		start := time.Now()
		resp, err := hgWebhookClient.Do(req)
		res.DurationMS = int(time.Since(start).Milliseconds())
		if err != nil {
			res.Error = err.Error()
		} else {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			res.StatusCode = resp.StatusCode
			res.Response = strings.ToValidUTF8(string(snippet), "")
			if !utf8.ValidString(res.Response) {
				res.Response = ""
			}
			res.Success = resp.StatusCode >= 200 && resp.StatusCode < 300
		}
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO hg_webhook_deliveries(webhook_id, delivery_id, event, attempt, status_code, duration_ms, success, error, response)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, w.ID, id, event, attempt, res.StatusCode, res.DurationMS, res.Success, res.Error, res.Response); err != nil {
		log.Printf("headlessgo: webhook log: %v", err)
	}
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM hg_webhook_deliveries WHERE webhook_id = $1 AND id NOT IN (
		SELECT id FROM hg_webhook_deliveries WHERE webhook_id = $1 ORDER BY id DESC LIMIT 200)`, w.ID)
	return res
}

func hgSign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// hgTestWebhook sends a test event once, synchronously.
func (s *Server) hgTestWebhook(ctx context.Context, w *hgWebhook) hgDelivery {
	body, _ := json.Marshal(map[string]any{"event": "trigger-test", "createdAt": hgTime(time.Now())})
	return s.hgAttempt(ctx, w, "trigger-test", auth.RandomToken(12), body, 1)
}

func (s *Server) hgDeliveries(ctx context.Context, webhookID int64, limit int) []hgDelivery {
	q := `SELECT id, webhook_id, delivery_id, event, attempt, status_code, duration_ms, success, error, response, created_at FROM hg_webhook_deliveries`
	args := []any{}
	if webhookID > 0 {
		q += ` WHERE webhook_id = $1`
		args = append(args, webhookID)
	}
	q += ` ORDER BY id DESC LIMIT ` + itoa64(int64(limit))
	rows, err := s.store.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []hgDelivery
	for rows.Next() {
		var d hgDelivery
		if rows.Scan(&d.ID, &d.WebhookID, &d.DeliveryID, &d.Event, &d.Attempt, &d.StatusCode, &d.DurationMS, &d.Success, &d.Error, &d.Response, &d.CreatedAt) == nil {
			out = append(out, d)
		}
	}
	return out
}
