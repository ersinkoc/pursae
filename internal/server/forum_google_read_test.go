package server

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"testing"

	"wpgo/internal/config"
	"wpgo/internal/settings"
)

type googleFailingBody struct {
	data string
	fail bool
}

func (r *googleFailingBody) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if r.fail && r.data == "" {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func (r *googleFailingBody) Close() error { return nil }

type googleBodyTransport struct{ fail string }

func (x googleBodyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"accounts.google.com","aud":"cid","exp":4102444800,"nonce":"nonce","sub":"id"}`))
	body := `{"access_token":"access","id_token":"a.` + payload + `.b"}`
	if r.URL.String() == forumGoogleUserinfoURL {
		body = `{"sub":"id","email":"a@example.com","email_verified":true}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &googleFailingBody{data: body, fail: x.fail == r.URL.String()}}, nil
}

func TestForumGoogleExchangePropagatesBodyFailures(t *testing.T) {
	oldHTTP := forumGoogleHTTP
	old, exists := settings.Defaults[forumOptions]
	defer func() {
		forumGoogleHTTP = oldHTTP
		if exists {
			settings.Defaults[forumOptions] = old
		} else {
			delete(settings.Defaults, forumOptions)
		}
	}()
	settings.Defaults[forumOptions] = map[string]any{"google_client_id": "cid"}
	s := &Server{settings: settings.New(nil), cfg: config.Config{SiteURL: "https://example.test"}}
	st := &forumOAuthState{N: "nonce"}
	for _, tc := range []struct {
		fail    string
		wantErr bool
	}{{"", false}, {forumGoogleTokenURL, true}, {forumGoogleUserinfoURL, true}} {
		forumGoogleHTTP = &http.Client{Transport: googleBodyTransport{fail: tc.fail}}
		_, e := s.forumGoogleExchange(context.Background(), st, "code")
		if (e != nil) != tc.wantErr || tc.wantErr && !errors.Is(e, io.ErrUnexpectedEOF) {
			t.Fatalf("fail=%q err=%v", tc.fail, e)
		}
	}
}
