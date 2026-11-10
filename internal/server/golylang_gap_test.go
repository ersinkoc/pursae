package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wpgo/internal/caps"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

func TestPllTranslatorCaps(t *testing.T) {
	mk := func(c ...string) *store.User {
		g := caps.Grants{}
		for _, x := range c {
			g[x] = true
		}
		return &store.User{ID: 7, Caps: g}
	}
	cases := []struct {
		name  string
		u     *store.User
		de    bool
		fr    bool
		any   bool
		langs int
	}{
		{"nobody", mk("edit_posts"), false, false, false, 0},
		{"admin", mk("manage_options"), true, true, true, 2},
		{"all languages", mk(pllCapManageTranslations), true, true, true, 2},
		{"german only", mk("translate_de"), true, false, true, 1},
	}
	langs := []*pllLanguage{{Slug: "de"}, {Slug: "fr"}}
	for _, c := range cases {
		if got := pllCanTranslate(c.u, "de"); got != c.de {
			t.Errorf("%s: de %v", c.name, got)
		}
		if got := pllCanTranslate(c.u, "fr"); got != c.fr {
			t.Errorf("%s: fr %v", c.name, got)
		}
		if got := pllCanTranslateAny(c.u); got != c.any {
			t.Errorf("%s: any %v", c.name, got)
		}
		if got := len(pllTranslatableLangs(c.u, langs)); got != c.langs {
			t.Errorf("%s: langs %d", c.name, got)
		}
	}
	if pllCanTranslate(nil, "de") || pllCanTranslateAny(nil) || pllCanTranslateAny(mk("translate_")) {
		t.Error("nil user or an empty language suffix may translate")
	}
}

func TestPllReformatDate(t *testing.T) {
	s := &Server{}
	tbl := &pllFrontTable{dateSrc: "F j, Y", dateDst: "j. F Y", timeSrc: "g:i a", timeDst: "H:i"}
	l := &pllLanguage{Locale: "de_DE"}
	if got, ok := s.pllReformatDate(tbl, "October 5, 2026", l); !ok || got != "5. October 2026" {
		t.Errorf("date: %q %v", got, ok)
	}
	if got, ok := s.pllReformatDate(tbl, "3:04 pm", l); !ok || got != "15:04" {
		t.Errorf("time: %q %v", got, ok)
	}
	if _, ok := s.pllReformatDate(tbl, "Hello", l); ok {
		t.Error("prose reformatted")
	}
	if _, ok := s.pllReformatDate(&pllFrontTable{}, "October 5, 2026", l); ok {
		t.Error("untranslated format reformatted")
	}
	_ = wp.FormatDate(time.Now(), "Y")
}

func TestPllMTFillKeepsPartialProgress(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")

	var calls atomic.Int32
	deepl := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 { // the second batch hits the quota
			http.Error(w, `{"message":"Quota exceeded"}`, http.StatusTooManyRequests)
			return
		}
		var in struct {
			Text []string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var tr []map[string]string
		for _, s := range in.Text {
			tr = append(tr, map[string]string{"text": s}) // identical to the source (a brand name)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"translations": tr})
	}))
	defer deepl.Close()
	oldBase, oldClient, oldBatch := pllMTDeepLFreeBase, pllMTClient, pllMTFillBatch
	pllMTDeepLFreeBase, pllMTClient, pllMTFillBatch = deepl.URL, deepl.Client(), 1
	defer func() { pllMTDeepLFreeBase, pllMTClient, pllMTFillBatch = oldBase, oldClient, oldBatch }()

	o := srv.pllOpts()
	o.MTProvider, o.MTKey, o.MT = "deepl", "abc-123:fx", true
	_ = srv.pllSaveOpts(context.Background(), o)
	if !srv.pllMTEnabled() {
		t.Skip("machine translation could not be enabled directly")
	}
	l := srv.pllLang(context.Background(), "fr")
	n, err := srv.pllMTFillStrings(context.Background(), l)
	if err == nil || n != 1 {
		t.Fatalf("n=%d err=%v, want 1 saved before the failure", n, err)
	}
	saved := 0
	for _, v := range srv.pllStringMap(context.Background(), "fr") {
		if strings.TrimSpace(v) != "" {
			saved++
		}
	}
	if saved != 1 {
		t.Fatalf("%d translations saved, want 1 (identical-to-source kept)", saved)
	}
}

func TestPllCPTSlugBases(t *testing.T) {
	acfgoRegistry.Lock()
	old := acfgoRegistry.types
	acfgoRegistry.types = map[string]*acfgoPostTypeDef{
		"portfolio": {Name: "portfolio", Slug: "work", Public: true, HasArchive: true, ArchiveSlug: "works"},
		"nested":    {Name: "nested", Slug: "a/b", Public: true},
		"private":   {Name: "private", Slug: "secret"},
	}
	acfgoRegistry.Unlock()
	defer func() { acfgoRegistry.Lock(); acfgoRegistry.types = old; acfgoRegistry.Unlock() }()
	got := pllCPTBasesFor([]string{"portfolio", "nested", "private", "unknown"})
	want := [][2]string{{"portfolio", "work"}, {"portfolio_archive", "works"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}
