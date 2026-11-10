package server

import (
	"bytes"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Per-block core styles (Theme.BlockStyles): like WordPress with should_load_separate_core_block_assets,
// a page carries only the styles of the core blocks it prints (wp-includes/blocks/<name>/style.min.css)
// instead of the whole block library (140 KB, render-blocking). wp_head prints a marker; once the page
// is rendered, the marker becomes one inline <style> with the styles of the blocks whose wp-block-<name>
// class the page contains, minus the blocks the theme's stylesheet already carries.

const blockStylesMarker = "<!--wpgo:block-styles-->\n"

var (
	blockClassRe    = regexp.MustCompile(`wp-block-([a-z0-9]+(?:-[a-z0-9]+)*)`)
	blockStyleNames struct {
		once sync.Once
		set  map[string]bool
	}
	blockStyleCache sync.Map // sorted block list -> CSS
	// blockStyleCacheN counts the cached lists. The list comes from the rendered page, and
	// visitor text is part of it (a search for "wp-block-quote wp-block-code" is echoed by the
	// search title), so every subset of the block names could be a key: past blockStyleCacheMax the
	// cache starts over.
	blockStyleCacheN atomic.Int64
)

const blockStyleCacheMax = 512

// blockStyleDirs lists the blocks that have a style.min.css under wp-includes/blocks.
func (s *Server) blockStyleDirs() map[string]bool {
	blockStyleNames.once.Do(func() {
		blockStyleNames.set = map[string]bool{}
		entries, _ := fs.ReadDir(s.web, "wp-includes/blocks")
		for _, e := range entries {
			if e.IsDir() {
				blockStyleNames.set[e.Name()] = true
			}
		}
	})
	return blockStyleNames.set
}

// pageBlocks finds the blocks a rendered page uses from its wp-block-* class names: the longest
// block name a class starts with (wp-block-post-title-is-layout-flow is post-title).
func (s *Server) pageBlocks(page []byte) []string {
	dirs := s.blockStyleDirs()
	found := map[string]bool{}
	for _, m := range blockClassRe.FindAllSubmatch(page, -1) {
		name := string(m[1])
		for name != "" {
			if dirs[name] {
				found[name] = true
				break
			}
			i := strings.LastIndexByte(name, '-')
			if i < 0 {
				break
			}
			name = name[:i]
		}
	}
	if found["gallery"] {
		found["image"] = true
	}
	out := make([]string, 0, len(found))
	for n := range found {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// blockStylesFor replaces the marker with the styles of the blocks the page uses.
func (s *Server) blockStylesFor(data any, page []byte) []byte {
	skip := map[string]bool{}
	if v, ok := data.(*siteView); ok && v.Theme != nil {
		for _, n := range v.Theme.BlockStyles {
			skip[n] = true
		}
	}
	var names []string
	for _, n := range s.pageBlocks(page) {
		if !skip[n] {
			names = append(names, n)
		}
	}
	css := ""
	if len(names) > 0 {
		key := strings.Join(names, ",")
		if c, ok := blockStyleCache.Load(key); ok && s.cfg.WebDir == "" {
			css = c.(string)
		} else {
			var sb strings.Builder
			for _, n := range names {
				sb.WriteString(s.webFile("wp-includes/blocks/" + n + "/style.min.css"))
			}
			css = sb.String()
			if _, loaded := blockStyleCache.Swap(key, css); !loaded && blockStyleCacheN.Add(1) > blockStyleCacheMax {
				blockStyleCache.Clear()
				blockStyleCacheN.Store(0)
			}
		}
	}
	repl := []byte{}
	if css != "" {
		repl = []byte("<style id='wp-block-styles-inline-css'>\n" + css + "\n</style>\n")
	}
	return bytes.Replace(page, []byte(blockStylesMarker), repl, 1)
}
