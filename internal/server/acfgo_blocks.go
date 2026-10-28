package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/blocks"
)

// ACF Blocks: a folder with a block.json whose "acf" key names the render template, like ACF 6's
// block.json registration. ACF-Go reads <data>/acf-blocks/<name>/ and the active theme package's
// blocks/<name>/ (or acf-blocks/<name>/); the template is a Go html/template (render.html) that
// gets .Block, .Fields (formatted values), .IsPreview and .Obj (for get_field "x" .Obj). Values
// are kept in the block's "data" attribute (name → value, _name → field key), as ACF does.

type acfgoBlockDef struct {
	Name, Title, Description, Category, Icon, Mode string
	Keywords                                       []string
	Supports                                       map[string]any
	Dir, Template                                  string
	tmpl                                           *template.Template
}

type acfgoBlockCache struct {
	mu   sync.Mutex
	list []*acfgoBlockDef
	at   time.Time
}

var acfgoBlockCaches sync.Map // *Server → *acfgoBlockCache

// acfgoBlockDirs are the folders blocks are loaded from.
func (s *Server) acfgoBlockDirs() []string {
	dirs := []string{filepath.Join(filepath.Dir(filepath.Clean(s.cfg.UploadsDir)), "acf-blocks")}
	if t := s.activeTheme(); t != nil && t.Package != nil && t.Package.Dir != "" {
		dirs = append(dirs, filepath.Join(t.Package.Dir, "blocks"), filepath.Join(t.Package.Dir, "acf-blocks"))
	}
	return dirs
}

// acfgoBlocks lists the ACF blocks (rescanned every few seconds) and registers their renderers.
func (s *Server) acfgoBlocks() []*acfgoBlockDef {
	v, _ := acfgoBlockCaches.LoadOrStore(s, &acfgoBlockCache{})
	bc := v.(*acfgoBlockCache)
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.list != nil && time.Since(bc.at) < 5*time.Second {
		return bc.list
	}
	var out []*acfgoBlockDef
	seen := map[string]bool{}
	for _, dir := range s.acfgoBlockDirs() {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "block.json"))
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			acf, ok := m["acf"].(map[string]any)
			if !ok {
				continue
			}
			name := acfgoStr(m["name"])
			if name == "" || seen[name] {
				continue
			}
			if !strings.Contains(name, "/") {
				name = "acf/" + name
			}
			seen[name] = true
			def := &acfgoBlockDef{Name: name, Title: defaultStr(acfgoStr(m["title"]), name), Description: acfgoStr(m["description"]),
				Category: defaultStr(acfgoStr(m["category"]), "common"), Mode: defaultStr(acfgoStr(acf["mode"]), "preview"),
				Keywords: acfgoStrList(m["keywords"]), Dir: filepath.Dir(f), Template: defaultStr(acfgoStr(acf["renderTemplate"]), "render.html")}
			switch ic := m["icon"].(type) {
			case string:
				def.Icon = ic
			case map[string]any:
				def.Icon = acfgoStr(ic["src"])
			}
			def.Supports, _ = m["supports"].(map[string]any)
			src, err := os.ReadFile(filepath.Join(def.Dir, filepath.Base(def.Template)))
			if err == nil {
				funcs := template.FuncMap{}
				if s.tmpl != nil {
					funcs = s.tmpl.funcs()
				}
				for k, fn := range s.acfgoTemplateFuncs() {
					funcs[k] = fn
				}
				if t, err := template.New(def.Name).Funcs(funcs).Parse(string(src)); err == nil {
					def.tmpl = t
				} else {
					log.Printf("acf-go: block %s template: %v", def.Name, err)
				}
			}
			out = append(out, def)
			blocks.RegisterDynamic(def.Name, acfgoRenderDynamic)
		}
	}
	if out == nil {
		out = []*acfgoBlockDef{}
	}
	bc.list, bc.at = out, time.Now()
	return out
}

func (s *Server) acfgoBlock(name string) *acfgoBlockDef {
	for _, b := range s.acfgoBlocks() {
		if b.Name == name {
			return b
		}
	}
	return nil
}

// acfgoRenderDynamic renders an ACF block on the front end (nothing while ACF-Go is off).
func acfgoRenderDynamic(ctx context.Context, b *blocks.Block, _ string) string {
	rc := renderFrom(ctx)
	if rc == nil || rc.s == nil || !rc.s.acfgoOn() {
		return ""
	}
	data, _ := b.Attrs["data"].(map[string]any)
	return rc.s.acfgoRenderBlock(ctx, b.Name(), data, b.Attrs, false)
}

// acfgoRenderBlock executes a block's template.
func (s *Server) acfgoRenderBlock(ctx context.Context, name string, data, attrs map[string]any, preview bool) string {
	def := s.acfgoBlock(name)
	if def == nil || def.tmpl == nil {
		if preview {
			return `<p class="acf-block-preview-missing">` + esc(name) + `: render template missing.</p>`
		}
		return ""
	}
	o := &acfObject{Kind: "block", Prefix: name, meta: orEmptyMap(data)}
	fields := map[string]any{}
	for _, g := range s.acfgoGroupsFor(ctx, &acfScreen{Block: name}) {
		for _, f := range g.Fields {
			s.acfgoLoadInto(ctx, o, fields, f, "", true)
		}
	}
	block := map[string]any{"name": name, "title": def.Title, "id": acfgoStr(attrs["id"]), "className": acfgoStr(attrs["className"]),
		"align": acfgoStr(attrs["align"]), "data": data}
	var buf bytes.Buffer
	if err := def.tmpl.Execute(&buf, map[string]any{"Block": block, "Fields": fields, "IsPreview": preview, "Obj": o}); err != nil {
		log.Printf("acf-go: render %s: %v", name, err)
		return ""
	}
	return buf.String()
}

// acfgoBlockValue is get_field on a block (the "Obj" of a block template).
func (s *Server) acfgoBlockValue(ctx context.Context, o *acfObject, selector string, format bool) any {
	f := s.acfgoFind(ctx, o, selector)
	if f == nil {
		v, _ := s.acfgoGet(o, selector)
		return v
	}
	return s.acfgoLoad(ctx, o, f, "", format)
}

// acfgoBlockEditorScript registers the blocks in Gotenberg (an inline script before acfgo-blocks).
func (s *Server) acfgoBlockEditorScript(c *gin.Context) {
	list := s.acfgoBlocks()
	if len(list) == 0 {
		return
	}
	type jsBlock struct {
		Name        string         `json:"name"`
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Category    string         `json:"category"`
		Icon        string         `json:"icon"`
		Keywords    []string       `json:"keywords"`
		Mode        string         `json:"mode"`
		Supports    map[string]any `json:"supports"`
	}
	var out []jsBlock
	for _, b := range list {
		out = append(out, jsBlock{b.Name, b.Title, b.Description, b.Category, b.Icon, b.Keywords, b.Mode, b.Supports})
	}
	data, _ := json.Marshal(map[string]any{"blocks": out, "nonce": s.nonce(c, "acfgo-block")})
	a := s.pageAssets(c)
	a.Script("acfgo-blocks").Style("acfgo")
	a.AddBefore("acfgo-blocks", "var acfgoBlocks = "+string(data)+";")
}

// acfgoBlockFormAjax returns the field form of a block (Edit mode).
func (s *Server) acfgoBlockFormAjax(c *gin.Context) {
	if !s.checkNonceValue(c, c.PostForm("nonce"), "acfgo-block") || !currentUser(c).Can("edit_posts", nil) {
		c.JSON(http.StatusForbidden, gin.H{"success": false})
		return
	}
	ctx := c.Request.Context()
	name := c.PostForm("name")
	var data map[string]any
	_ = json.Unmarshal([]byte(c.PostForm("data")), &data)
	groups := s.acfgoGroupsFor(ctx, &acfScreen{Block: name, User: currentUser(c)})
	o := &acfObject{Kind: "block", Prefix: name, meta: orEmptyMap(data)}
	html, _, _ := s.acfgoBoxes(ctx, groups, o, "form", 0)
	if len(groups) == 0 {
		html = `<p class="acf-block-no-fields">This block contains no editable fields. Assign a field group to the block with the Block location rule.</p>`
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"form": html}})
}

// acfgoBlockPreviewAjax stores the posted fields into the block's data and renders the preview.
func (s *Server) acfgoBlockPreviewAjax(c *gin.Context) {
	if !s.checkNonceValue(c, c.PostForm("nonce"), "acfgo-block") || !currentUser(c).Can("edit_posts", nil) {
		c.JSON(http.StatusForbidden, gin.H{"success": false})
		return
	}
	ctx := c.Request.Context()
	name := c.PostForm("name")
	var data map[string]any
	_ = json.Unmarshal([]byte(c.PostForm("data")), &data)
	o := &acfObject{Kind: "block", Prefix: name, meta: orEmptyMap(data)}
	if in := acfgoParseForm(c.Request.PostForm, "acf"); len(in) > 0 {
		_ = s.acfgoSaveInput(ctx, o, in, currentUser(c).Can("unfiltered_html", nil))
	}
	var attrs map[string]any
	_ = json.Unmarshal([]byte(c.PostForm("attributes")), &attrs)
	html := s.acfgoRenderBlock(ctx, name, o.meta, orEmptyMap(attrs), true)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"data": o.meta, "html": html}})
}
