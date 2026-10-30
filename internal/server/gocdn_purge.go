package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Purging. Every change that moves URLs (settings, a finished bulk job, the Purge screen) empties
// WPGo Cache through purgeAll. With Cloudflare configured (zone ID + an API token with the
// "Zone › Cache Purge" permission), the Purge screen purges the zone too, and with "purge
// automatically" every automatic purge of GoCDN and every deleted offloaded file is sent to
// Cloudflare's purge_cache API.

var gocdnZoneID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// gocdnPurge empties the page cache; withCF also purges Cloudflare (in the background) when
// automatic Cloudflare purging is on.
func (s *Server) gocdnPurge(ctx context.Context, withCF bool) {
	s.purgeAll(ctx)
	if !withCF {
		return
	}
	st := s.gocdnLoad()
	if st.CF && st.CFAuto && s.gocdnOn() {
		go func() {
			if err := s.gocdnCloudflarePurge(context.Background(), st, nil); err != nil {
				slog.Warn("gocdn: Cloudflare purge", "err", err)
			}
		}()
	}
}

// gocdnCloudflarePurge purges the listed URLs (30 per request) or, with none, the whole zone.
func (s *Server) gocdnCloudflarePurge(ctx context.Context, st gocdnSettings, urls []string) error {
	token := s.gocdnReveal(st.CFToken)
	if !gocdnZoneID.MatchString(st.CFZone) || token == "" {
		return errors.New("Cloudflare is not configured: a zone ID (32 hex characters) and an API token are needed")
	}
	if len(urls) == 0 {
		return gocdnCFCall(ctx, st.CFZone, token, map[string]any{"purge_everything": true})
	}
	for len(urls) > 0 {
		n := min(30, len(urls))
		if err := gocdnCFCall(ctx, st.CFZone, token, map[string]any{"files": urls[:n]}); err != nil {
			return err
		}
		urls = urls[n:]
	}
	return nil
}

func gocdnCFCall(ctx context.Context, zone, token string, payload map[string]any) error {
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gocdnCFBase+"/zones/"+url.PathEscape(zone)+"/purge_cache", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gocdnHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var res struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(data, &res)
	if resp.StatusCode/100 == 2 && res.Success {
		return nil
	}
	var msgs []string
	for _, e := range res.Errors {
		msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
	}
	if len(msgs) == 0 {
		msgs = append(msgs, resp.Status)
	}
	return errors.New("Cloudflare: " + strings.Join(msgs, "; "))
}
