package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

func wbDocCanvasRef(d *store.WebBuilderDoc) bool {
	var ds webbuilder.DocState
	var ps webbuilder.PageSettings
	if len(d.Doc) > 0 {
		_ = json.Unmarshal(d.Doc, &ds)
	}
	if len(d.Page) > 0 {
		_ = json.Unmarshal(d.Page, &ps)
	}
	return webbuilder.EffectivePageLayout(ps, &ds) == webbuilder.PageLayoutCanvas
}

func bigCodeDoc(kind string) json.RawMessage {
	var secs []any
	for i := 0; i < 40; i++ {
		secs = append(secs, map[string]any{"id": "c" + strings.Repeat("x", i), "kind": "code", "content": map[string]string{"html": strings.Repeat(`<div class="row"><p>Lorem ipsum dolor sit amet.</p></div>`, 40)}})
	}
	secs = append(secs, map[string]any{"id": "n", "kind": kind})
	b, _ := json.Marshal(map[string]any{"sections": secs})
	return b
}

// wbDocCanvas gives what reading the doc gives, per revision.
func TestWBDocCanvas(t *testing.T) {
	cases := []*store.WebBuilderDoc{
		{PostID: 9401, Rev: 1, UpdatedAt: time.Unix(1700000000, 1), Doc: bigCodeDoc("nav")},
		{PostID: 9402, Rev: 1, UpdatedAt: time.Unix(1700000000, 2), Doc: bigCodeDoc("hero")},
		{PostID: 9403, Rev: 1, UpdatedAt: time.Unix(1700000000, 3), Doc: bigCodeDoc("nav"), Page: json.RawMessage(`{"layout":"site"}`)},
		{PostID: 9404, Rev: 1, UpdatedAt: time.Unix(1700000000, 4), Doc: bigCodeDoc("hero"), Page: json.RawMessage(`{"layout":"canvas"}`)},
		{PostID: 9405, Doc: json.RawMessage(`{"sections":[{"kind":"footer"}]}`)}, // never stored
		{PostID: 9406, Rev: 2, UpdatedAt: time.Unix(1700000000, 6), Doc: json.RawMessage(`not json`)},
	}
	for i, d := range cases {
		for n := 0; n < 2; n++ {
			if got, want := wbDocCanvas(d), wbDocCanvasRef(d); got != want {
				t.Errorf("case %d: %v, want %v", i, got, want)
			}
		}
	}
	// A new revision is read again.
	d := *cases[0]
	d.Rev, d.UpdatedAt, d.Doc = 2, time.Unix(1700000001, 0), bigCodeDoc("hero")
	if wbDocCanvas(&d) {
		t.Error("new revision: still canvas")
	}
}

func BenchmarkWBDocCanvas(b *testing.B) {
	d := &store.WebBuilderDoc{PostID: 9410, Rev: 1, UpdatedAt: time.Unix(1700000000, 0), Doc: bigCodeDoc("hero")}
	b.Logf("doc %d bytes", len(d.Doc))
	b.Run("unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			wbDocCanvasRef(d)
		}
	})
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			wbDocCanvas(d)
		}
	})
}
