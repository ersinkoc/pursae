package server

import (
	"hash/fnv"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
)

// Admin screens that build their text in the browser (ACF-Go's field group editor, Gontact Form's
// builder, Goocommerce's store screens, SEO-Go's editor panel, inline scripts of the templates)
// get the translations of their scripts' UI strings: wpgo-l10n.js holds them, swaps them into
// the page as the scripts add elements, and translates the message of alert(), confirm() and
// prompt(). Golementor Studio has its own dictionary (gweL10nScript).

// adminJSSources are the scripts whose string literals are UI text; templates contribute the
// literals of their inline scripts and event handler attributes.
var adminJSSources = []string{"admin/assets/js/*.js", "admin/assets/acfgo/*.js", "admin/assets/gontact/*.js",
	"goocommerce/assets/js/admin/*.js", "wp-admin/js/seogo-*.js", "admin/templates/*.html", "admin/templates/*/*.html"}

var (
	adminJSStringsOnce sync.Once
	adminJSStringsList []string
	inlineScriptRe     = regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>|\bon[a-z]+="([^"]*)"`)
	adminL10nCache     sync.Map // *i18n.Catalog -> *adminL10n
	jsPlaceholder      = regexp.MustCompile(`\{[a-z_]+\}`)
)

// jsUIStrings returns the literals of a script that read as UI text (gweJSStrings' rules).
func jsUIStrings(src string, seen map[string]bool) {
	for _, m := range gweJSLiteral.FindAllStringSubmatch(src, -1) {
		lit := m[1] + m[2] + m[3]
		lit = strings.NewReplacer(`\'`, "'", `\"`, `"`, `\n`, "\n").Replace(lit)
		t := strings.TrimSpace(lit)
		// "Maximum rows reached ({max} rows)": a message whose {placeholders} the script fills in
		bare := jsPlaceholder.ReplaceAllString(t, "")
		if t == "" || !gweJSHasWord.MatchString(t) || gweJSNotText.MatchString(bare) || !strings.ContainsAny(t[:1], "ABCDEFGHIJKLMNOPQRSTUVWXYZ“\"(") {
			continue
		}
		seen[t] = true
	}
}

func (s *Server) adminJSStrings() []string {
	adminJSStringsOnce.Do(func() {
		seen := map[string]bool{}
		for _, g := range adminJSSources {
			files, _ := fs.Glob(s.web, g)
			for _, f := range files {
				b, err := fs.ReadFile(s.web, f)
				if err != nil {
					continue
				}
				src := string(b)
				if strings.HasSuffix(f, ".html") {
					var inline strings.Builder
					for _, m := range inlineScriptRe.FindAllStringSubmatch(src, -1) {
						inline.WriteString(m[1] + "\n" + strings.ReplaceAll(m[2], "&#39;", "'") + "\n")
					}
					src = inline.String()
				}
				jsUIStrings(src, seen)
			}
		}
		for k := range seen {
			adminJSStringsList = append(adminJSStringsList, k)
		}
		sort.Strings(adminJSStringsList)
	})
	return adminJSStringsList
}

type adminL10n struct {
	js, ver string
}

// adminL10nFor builds (once per catalog) the dictionary script of a language.
func (s *Server) adminL10nFor(cat *i18n.Catalog) *adminL10n {
	if v, ok := adminL10nCache.Load(cat); ok {
		return v.(*adminL10n)
	}
	dict := map[string]string{}
	for _, lit := range s.adminJSStrings() {
		if tr, ok := cat.Lookup(lit); ok {
			dict[lit] = tr
		} else if i18n.MissHook != nil {
			i18n.MissHook(lit)
		}
	}
	l := &adminL10n{}
	if len(dict) > 0 {
		l.js = "window.WPGOL10n = " + gweJSON(dict) + ";\n" + adminL10nRuntime
		h := fnv.New64a()
		h.Write([]byte(l.js))
		l.ver = cat.Locale + "-" + strconv.FormatUint(h.Sum64(), 36)
	}
	adminL10nCache.Store(cat, l)
	return l
}

// adminL10nTag is the script tag of the dictionary for the page's language ("" when nothing of
// the scripts is translated).
func (s *Server) adminL10nTag(cat *i18n.Catalog) string {
	l := s.adminL10nFor(cat)
	if l.js == "" {
		return ""
	}
	return `<script src="/wp-admin/wpgo-l10n.js?ver=` + l.ver + `"></script>`
}

// adminL10nScript serves wpgo-l10n.js for the current user's language.
func (s *Server) adminL10nScript(c *gin.Context) {
	l := s.adminL10nFor(s.catalog(c))
	c.Header("Cache-Control", "private, max-age=31536000")
	c.Data(http.StatusOK, "application/javascript; charset=utf-8", []byte(l.js))
}

const adminL10nRuntime = `(function(){var D=window.WPGOL10n||{};var A=['title','placeholder','aria-label','alt','data-title'];
function tr(s){if(!s)return s;var t=s.trim();if(!t)return s;var v=D[t];return v?s.replace(t,v):s;}
window.wpgo__=tr;
function skip(e){return !e||/^(SCRIPT|STYLE|TEXTAREA|CODE|PRE|IFRAME|OPTION)$/.test(e.tagName)&&!(e.tagName==='OPTION'&&e.hasAttribute('value'))||e.isContentEditable||(e.closest&&e.closest('[translate="no"],.editor-styles-wrapper,.wp-block-post-content'));}
function walk(n){if(n.nodeType===3){if(!skip(n.parentElement)){var v=tr(n.nodeValue);if(v!==n.nodeValue)n.nodeValue=v;}return;}
if(n.nodeType!==1||skip(n))return;
for(var i=0;i<A.length;i++){var a=n.getAttribute(A[i]);if(a){var t=tr(a);if(t!==a)n.setAttribute(A[i],t);}}
if(n.tagName==='INPUT'&&/^(button|submit|reset)$/i.test(n.type)&&n.value){var b=tr(n.value);if(b!==n.value)n.value=b;}
for(var c=n.firstChild;c;c=c.nextSibling)walk(c);}
['alert','confirm','prompt'].forEach(function(f){var o=window[f];if(!o)return;window[f]=function(m){var a=[].slice.call(arguments);if(typeof m==='string')a[0]=tr(m);return o.apply(window,a);};});
function start(){walk(document.body);new MutationObserver(function(ms){for(var i=0;i<ms.length;i++){var m=ms[i];
if(m.type==='characterData'){walk(m.target);}else{for(var j=0;j<m.addedNodes.length;j++)walk(m.addedNodes[j]);}}})
.observe(document.body,{childList:true,subtree:true,characterData:true});}
if(document.body)start();else document.addEventListener('DOMContentLoaded',start);})();`
