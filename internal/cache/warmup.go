package cache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// WarmupOptions configures [Warmup].
type WarmupOptions struct {
	// Handler, when set, is called in-process (no network). It must include the
	// cache middleware; [Cache.Warm] wraps the application handler for you.
	Handler http.Handler
	// BaseURL resolves relative URLs. Without a Handler, requests are sent over the
	// network with Client.
	BaseURL string
	// Client is used in network mode. Default: a client with a 30s timeout.
	Client *http.Client
	// Concurrency is the number of parallel requests. Default 4.
	Concurrency int
	// RatePerSecond caps the request rate; 0 means unlimited.
	RatePerSecond float64
	// UserAgent is sent with every request. Default "wpgo-cache-warmup/1".
	UserAgent string
	// MobileUserAgent, when set, warms a second (mobile) variant of each URL with
	// this User-Agent; useful with CacheMobileSeparately.
	MobileUserAgent string
	// Progress, when set, is called after each request with the running totals.
	// Calls are serialized.
	Progress func(WarmupProgress)
}

// WarmupProgress counts warmup results.
type WarmupProgress struct {
	Total         int // requests planned
	Done          int // requests finished (any outcome)
	Warmed        int // pages rendered and stored (X-WPGo-Cache: miss)
	AlreadyCached int // pages that were already cached (hit)
	Skipped       int // not cacheable (bypass or non-page response)
	Failed        int // transport errors and 5xx responses
}

// Warm warms the cache by calling app, wrapped in this cache's middleware, for every
// URL in-process. Other options are taken from opts.
func (c *Cache) Warm(ctx context.Context, app http.Handler, urls []string, opts WarmupOptions) (WarmupProgress, error) {
	opts.Handler = c.Middleware(app)
	return Warmup(ctx, urls, opts)
}

// Warmup requests every URL (sitemap URLs, typically) so they are cached. It stops
// early and returns ctx.Err() when ctx is cancelled.
func Warmup(ctx context.Context, urls []string, opts WarmupOptions) (WarmupProgress, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "wpgo-cache-warmup/1"
	}
	if opts.Handler == nil && opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	var base *url.URL
	if opts.BaseURL != "" {
		b, err := url.Parse(opts.BaseURL)
		if err != nil {
			return WarmupProgress{}, err
		}
		base = b
	}

	type job struct{ url, ua string }
	var jobs []job
	for _, u := range urls {
		jobs = append(jobs, job{u, opts.UserAgent})
		if opts.MobileUserAgent != "" {
			jobs = append(jobs, job{u, opts.MobileUserAgent})
		}
	}
	prog := WarmupProgress{Total: len(jobs)}
	if len(jobs) == 0 {
		return prog, nil
	}

	var tick <-chan time.Time
	if opts.RatePerSecond > 0 {
		t := time.NewTicker(time.Duration(float64(time.Second) / opts.RatePerSecond))
		defer t.Stop()
		tick = t.C
	}

	var mu sync.Mutex
	record := func(status string, code int, err error) {
		mu.Lock()
		defer mu.Unlock()
		prog.Done++
		switch {
		case err != nil || code >= 500:
			prog.Failed++
		case status == "miss":
			prog.Warmed++
		case status == "hit":
			prog.AlreadyCached++
		default:
			prog.Skipped++
		}
		if opts.Progress != nil {
			opts.Progress(prog)
		}
	}

	queue := make(chan job)
	var wg sync.WaitGroup
	for range opts.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				status, code, err := warmOne(ctx, j.url, j.ua, base, opts)
				record(status, code, err)
			}
		}()
	}

	var err error
feed:
	for _, j := range jobs {
		if tick != nil {
			select {
			case <-ctx.Done():
				err = ctx.Err()
				break feed
			case <-tick:
			}
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			break feed
		case queue <- j:
		}
	}
	close(queue)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return prog, err
}

// warmOne performs one request and returns the X-WPGo-Cache status and HTTP code.
func warmOne(ctx context.Context, raw, ua string, base *url.URL, opts WarmupOptions) (string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, err
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if opts.Handler == nil && !u.IsAbs() {
		return "", 0, errors.New("cache: warmup URL is relative and no BaseURL is set")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Encoding", "gzip")

	if opts.Handler != nil {
		req.RequestURI = u.RequestURI()
		req.RemoteAddr = "127.0.0.1:0"
		if req.Host == "" {
			req.Host = "localhost"
		}
		if u.Scheme == "https" {
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		w := &discardWriter{header: http.Header{}}
		opts.Handler.ServeHTTP(w, req)
		if w.code == 0 {
			w.code = http.StatusOK
		}
		return w.header.Get(HeaderStatus), w.code, ctx.Err()
	}

	resp, err := opts.Client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return resp.Header.Get(HeaderStatus), resp.StatusCode, err
}

// discardWriter is a minimal in-process ResponseWriter that drops the body.
type discardWriter struct {
	header http.Header
	code   int
}

func (d *discardWriter) Header() http.Header { return d.header }

func (d *discardWriter) WriteHeader(code int) {
	if d.code == 0 && code >= 200 {
		d.code = code
	}
}

func (d *discardWriter) Write(b []byte) (int, error) {
	if d.code == 0 {
		d.code = http.StatusOK
	}
	return len(b), nil
}
