package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Pursae Multi Language: XLIFF 1.2 export and import of content.
//
// Export (posts list › Bulk actions › "Export for translation (XLIFF)…"): one <file> per selected
// post, original="post-<id>", with a <trans-unit> per text the translation engine would send to
// machine translation: the title, the excerpt, the HTML between block delimiters, or the text
// units of a Web Builder page (golylang_translate.go). HTML units carry datatype="html" and are
// written as escaped text, so any CAT tool shows them.
//
// Import (Languages › Translations): every <file> is applied to the translation of its post in
// the file's target language, creating it as a draft when it does not exist. The <source> of a
// unit is matched against the current source text: units whose source changed since the export,
// and units with an empty <target>, keep the source text.

const pllXLIFFMaxUpload = 16 << 20

type pllXLIFFUnit struct {
	ID     string
	Source string
	HTML   bool
}

// pllXLIFFUnits lists the translation units of a post, in the order the engine asks for them.
func (s *Server) pllXLIFFUnits(ctx context.Context, p *store.Post) ([]pllXLIFFUnit, error) {
	var units []pllXLIFFUnit
	names := []string{"title", "excerpt"}
	rec := func(texts []string, asHTML bool) ([]string, error) {
		for _, t := range texts {
			id := "u" + strconv.Itoa(len(units)+1)
			if len(units) < len(names) {
				id = names[len(units)]
			}
			units = append(units, pllXLIFFUnit{ID: id, Source: t, HTML: asHTML})
		}
		return texts, nil
	}
	if _, err := s.pllTranslatePlan(ctx, p, p.ID, rec); err != nil {
		return nil, err
	}
	out := units[:0]
	for _, u := range units {
		if strings.TrimSpace(u.Source) != "" {
			out = append(out, u)
		}
	}
	return out, nil
}

// pllXLIFFExport downloads the XLIFF file of posts for the target language l.
func (s *Server) pllXLIFFExport(c *gin.Context, posts []*store.Post, l *pllLanguage) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">` + "\n")
	lb := s.links(c)
	n := 0
	for _, p := range posts {
		from := s.pllLang(c, s.pllObjLang(c, pllPost, p.ID))
		if from == nil || from.Slug == l.Slug {
			continue
		}
		units, err := s.pllXLIFFUnits(c, p)
		if err != nil || len(units) == 0 {
			continue
		}
		n++
		fmt.Fprintf(&b, ` <file original="post-%d" source-language="%s" target-language="%s" datatype="html" product-name="Pursae Multi Language" product-version="%s">`+"\n",
			p.ID, xmlEsc(from.W3C()), xmlEsc(l.W3C()), xmlEsc(Version))
		fmt.Fprintf(&b, "  <header><note>%s (%s) %s</note></header>\n  <body>\n", xmlEsc(p.Title), xmlEsc(p.Type), xmlEsc(lb.Permalink(p)))
		for _, u := range units {
			dt := "plaintext"
			if u.HTML {
				dt = "html"
			}
			fmt.Fprintf(&b, `   <trans-unit id="%s" datatype="%s" xml:space="preserve"><source>%s</source><target state="needs-translation"></target></trans-unit>`+"\n",
				u.ID, dt, xmlEsc(u.Source))
		}
		b.WriteString("  </body>\n </file>\n")
	}
	if n == 0 {
		c.Redirect(http.StatusFound, "/wp-admin/edit?post_type="+url.QueryEscape(posts[0].Type)+"&pll_error="+
			url.QueryEscape("Nothing to export: the selected items are already in "+l.Name+" or have no text."))
		return
	}
	b.WriteString("</xliff>\n")
	c.Header("Content-Disposition", `attachment; filename="translation-`+strings.ReplaceAll(l.Slug, `"`, "")+`.xliff"`)
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/x-xliff+xml; charset=UTF-8", b.Bytes())
}

// xliffText is the text of a <source> or <target>: character data, with the text inside inline
// elements (<g>, <ph>, <mrk>) kept and their tags dropped.
type xliffText string

func (t *xliffText) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case xml.CharData:
			b.Write(v)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	*t = xliffText(b.String())
	return nil
}

type xliffUnit struct {
	ID     string    `xml:"id,attr"`
	Source xliffText `xml:"source"`
	Target xliffText `xml:"target"`
}

type xliffGroup struct {
	Units  []xliffUnit  `xml:"trans-unit"`
	Groups []xliffGroup `xml:"group"`
}

type xliffFile struct {
	Original string     `xml:"original,attr"`
	Source   string     `xml:"source-language,attr"`
	Target   string     `xml:"target-language,attr"`
	Body     xliffGroup `xml:"body"`
}

type xliffDoc struct {
	XMLName xml.Name    `xml:"xliff"`
	Version string      `xml:"version,attr"`
	Files   []xliffFile `xml:"file"`
}

func (g *xliffGroup) all() []xliffUnit {
	out := append([]xliffUnit(nil), g.Units...)
	for i := range g.Groups {
		out = append(out, g.Groups[i].all()...)
	}
	return out
}

// parseXLIFF reads an XLIFF 1.x document.
func parseXLIFF(r io.Reader) (*xliffDoc, error) {
	doc := &xliffDoc{}
	dec := xml.NewDecoder(r)
	dec.Strict = true
	if err := dec.Decode(doc); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(doc.Version, "1.") {
		return nil, fmt.Errorf("XLIFF version %q is not supported (1.2 is)", doc.Version)
	}
	return doc, nil
}

// pllXLIFFLang finds the site language of an XLIFF language code (fr-FR, fr_FR, fr).
func (s *Server) pllXLIFFLang(ctx context.Context, code string) *pllLanguage {
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "_", "-"))
	if code == "" {
		return nil
	}
	var base *pllLanguage
	for _, l := range s.pllLanguages(ctx) {
		if strings.ToLower(l.W3C()) == code || strings.ToLower(strings.ReplaceAll(l.Locale, "_", "-")) == code || strings.ToLower(l.Slug) == code {
			return l
		}
		if b, _, _ := strings.Cut(code, "-"); b == strings.ToLower(l.Slug) && base == nil {
			base = l
		}
	}
	return base
}

// pllXLIFFTr is the pllTr of one <file>: a source text becomes its <target>, anything else stays.
func pllXLIFFTr(units []xliffUnit) (pllTr, int) {
	dict := map[string]string{}
	for _, u := range units {
		src, tgt := strings.TrimSpace(string(u.Source)), strings.TrimSpace(string(u.Target))
		if src != "" && tgt != "" {
			dict[src] = tgt
		}
	}
	return func(texts []string, _ bool) ([]string, error) {
		out := make([]string, len(texts))
		for i, t := range texts {
			out[i] = t
			trim := strings.TrimSpace(t)
			if tr, ok := dict[trim]; ok && trim != "" {
				lead := t[:strings.Index(t, trim)]
				out[i] = lead + tr + t[len(lead)+len(trim):]
			}
		}
		return out, nil
	}, len(dict)
}

// pllXLIFFApply applies a parsed XLIFF document as user u. fallback is the target language of
// files that name none. It returns how many posts were translated and what was skipped.
func (s *Server) pllXLIFFApply(ctx context.Context, doc *xliffDoc, u *store.User, fallback *pllLanguage) (done int, skipped []string) {
	skip := func(f *xliffFile, why string) { skipped = append(skipped, f.Original+": "+why) }
	for i := range doc.Files {
		f := &doc.Files[i]
		id, err := strconv.ParseInt(strings.TrimPrefix(f.Original, "post-"), 10, 64)
		if err != nil || !strings.HasPrefix(f.Original, "post-") {
			skip(f, "not a file exported by Pursae Multi Language")
			continue
		}
		src, err := s.store.GetPost(ctx, id)
		if err != nil || src.Status == "trash" || !s.pllTranslatedType(src.Type) {
			skip(f, "the source no longer exists")
			continue
		}
		l := s.pllXLIFFLang(ctx, f.Target)
		if l == nil {
			l = fallback
		}
		if l == nil {
			skip(f, "the target language "+f.Target+" is not one of the site's languages")
			continue
		}
		if !u.Can("edit_post", src) || !u.Can("edit_"+restBaseFor(src.Type), nil) {
			skip(f, "you are not allowed to translate this item")
			continue
		}
		tr, n := pllXLIFFTr(f.Body.all())
		if n == 0 {
			skip(f, "the file holds no translation")
			continue
		}
		dst, _, err := s.pllNewTranslation(ctx, src, l, u.ID)
		if err != nil || dst == nil {
			skip(f, "the translation could not be created")
			continue
		}
		if dst.ID == src.ID {
			skip(f, "the source is already in "+l.Name)
			continue
		}
		if !u.Can("edit_post", dst) {
			skip(f, "you are not allowed to edit the existing translation")
			continue
		}
		if err := s.pllTranslateFrom(ctx, src, dst, tr); err != nil {
			skip(f, err.Error())
			continue
		}
		if fresh, err := s.store.GetPost(ctx, dst.ID); err == nil {
			s.afterPostSave(ctx, fresh)
		}
		done++
	}
	return done, skipped
}

// pllXLIFFImport handles the "Import content translations" form of Languages › Translations.
func (s *Server) pllXLIFFImport(c *gin.Context) {
	if !s.checkNonce(c, "pll-import-export") {
		s.denyNonce(c)
		return
	}
	back := "/wp-admin/admin?page=mlang_strings"
	fail := func(msg string) { c.Redirect(http.StatusFound, back+"&pll_msg=import_error&err="+urlQueryEscape(msg)) }
	fh, err := c.FormFile("xliff")
	if err != nil {
		fail("Choose an XLIFF file to import.")
		return
	}
	f, err := fh.Open()
	if err != nil {
		fail(err.Error())
		return
	}
	defer f.Close()
	doc, err := parseXLIFF(io.LimitReader(f, pllXLIFFMaxUpload))
	if err != nil {
		fail("This file is not a valid XLIFF 1.2 file: " + err.Error())
		return
	}
	done, skipped := s.pllXLIFFApply(c, doc, currentUser(c), nil)
	if len(skipped) > 0 {
		msg := strconv.Itoa(done) + " items translated from the file. Skipped: " + strings.Join(skipped, "; ")
		if len(msg) > 1500 {
			msg = msg[:1500] + "…"
		}
		fail(msg)
		return
	}
	c.Redirect(http.StatusFound, back+"&pll_msg=xliff_imported&n="+strconv.Itoa(done))
}
