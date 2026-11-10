package server

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Popups of the Pursae Web Builder (docs/developer/web-builder.md "Popups"). A popup is a Theme
// Builder template of type "popup": its display conditions pick the pages (any number of popups can
// match one page), its PopupSettings (post meta _wb_popup, GET/PUT templates/{id}/popup) say when
// it opens and how often one visitor sees it. wbRenderTheme prints every matching popup hidden
// before </body>, with one inline script that opens them; the markup is the same for every visitor,
// so Pursae Cache keeps serving the page and the choice lives in the visitor's localStorage.

// wbPopup is one popup of the request.
type wbPopup struct {
	id       int64
	settings webbuilder.PopupSettings
	doc      *store.WebBuilderDoc
	filled   wbFilled
}

// wbPopups picks and fills the popups of the request. A previewed popup (?wb_template_preview=<id>)
// is the only one shown, and opens at once.
func (s *Server) wbPopups(c *gin.Context, v *siteView) []*wbPopup {
	if c == nil || v == nil || v.Q == nil || v.Q.Feed != "" || !s.wbOn() {
		return nil
	}
	q := v.Q
	if d, id, ok := s.wbTemplatePreview(c, webbuilder.LocationPopup); ok {
		st := s.wbPopupSettings(c, id)
		st.Trigger, st.Delay, st.Frequency, st.AfterConsent = webbuilder.PopupTriggerLoad, 0, webbuilder.PopupFreqAlways, false
		return []*wbPopup{{id: id, settings: st, doc: d, filled: s.wbFill(c, v, d, false)}}
	}
	if s.wbTemplatePreviewing(c) {
		return nil // previewing another template: no popup over it
	}
	ctx := c.Request.Context()
	idx := s.wbTemplateIndex(ctx)
	if idx.failed || len(idx.cands) == 0 {
		return nil
	}
	lang, def := "", ""
	if l := s.pllCurrent(ctx); l != nil {
		lang = l.Slug
	}
	if len(idx.group) > 0 {
		if d := s.pllDefault(ctx); d != nil {
			def = d.Slug
		}
	}
	usable := idx.candidates(lang, def)
	hits := webbuilder.PickTemplates(webbuilder.LocationPopup, usable, s.wbQueryFacts(c, q, idx.needTerms))
	var out []*wbPopup
	for _, i := range hits {
		if len(out) >= wbPopupMax {
			break
		}
		id := idx.translation(usable[i].ID, lang)
		if e := idx.entry(id); e == nil || !e.HasHTML {
			id = usable[i].ID
		}
		d := idx.doc(ctx, s, id)
		if d == nil {
			continue
		}
		cp := *d
		st := webbuilder.DefaultPopupSettings()
		if e := idx.entry(id); e != nil {
			st = webbuilder.ParsePopup(e.Popup)
		}
		out = append(out, &wbPopup{id: id, settings: st, doc: &cp, filled: s.wbFill(c, v, &cp, false)})
	}
	return out
}

// wbPopupMax caps the popups printed on one page.
const wbPopupMax = 5

func (s *Server) wbPopupSettings(c *gin.Context, id int64) webbuilder.PopupSettings {
	raw, _ := s.store.WebBuilder().TemplatePopup(c.Request.Context(), id)
	return webbuilder.ParsePopup(raw)
}

// wbPopupHTML is the hidden dialog of a popup.
func (s *Server) wbPopupHTML(p *wbPopup) string {
	st := p.settings
	id := strconv.FormatInt(p.id, 10)
	var b strings.Builder
	b.WriteString(`<div id="wb-popup-` + id + `" class="wb-popup wb-popup-` + st.Position + `" role="dialog" aria-modal="true" aria-label="Dialog" hidden`)
	attrs := [][2]string{{"data-wb-popup", id}, {"data-trigger", st.Trigger}, {"data-delay", strconv.Itoa(st.Delay)},
		{"data-scroll", strconv.Itoa(st.Scroll)}, {"data-frequency", st.Frequency}, {"data-days", strconv.Itoa(st.Days)},
		{"data-version", strconv.Itoa(st.Version)}, {"data-overlay", b2s(st.Overlay)}}
	if st.Trigger == webbuilder.PopupTriggerClick {
		attrs = append(attrs, [2]string{"data-selector", st.Selector})
	}
	if st.AfterConsent && s.gooikeOn() {
		attrs = append(attrs, [2]string{"data-consent", "1"})
	}
	for _, a := range attrs {
		b.WriteString(` ` + a[0] + `="` + html.EscapeString(a[1]) + `"`)
	}
	b.WriteString(` style="--wb-popup-w:` + strconv.Itoa(st.Width) + `px">`)
	b.WriteString(`<div class="wb-popup-backdrop" data-wb-popup-close></div><div class="wb-popup-box" tabindex="-1">`)
	b.WriteString(`<button type="button" class="wb-popup-x" data-wb-popup-close aria-label="Close">&times;</button>`)
	b.WriteString(`<div class="wb-popup-body">` + p.filled.html + `</div></div></div>` + "\n")
	return b.String()
}

func b2s(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

const wbPopupCSS = `<style id="wb-popups">
.wb-popup[hidden]{display:none}.wb-popup{position:fixed;inset:0;z-index:100000;display:flex;align-items:center;justify-content:center;padding:16px}
.wb-popup-top{align-items:flex-start}.wb-popup-bottom{align-items:flex-end}
.wb-popup-backdrop{position:absolute;inset:0;background:rgba(0,0,0,.55)}
.wb-popup-box{position:relative;box-sizing:border-box;width:min(100%,var(--wb-popup-w,640px));max-height:100%;overflow:auto;background:#fff;color:#1d2327;border-radius:8px;box-shadow:0 20px 60px rgba(0,0,0,.35);outline:0}
.wb-popup-x{position:absolute;top:6px;right:8px;z-index:1;width:36px;height:36px;border:0;background:transparent;color:inherit;font:28px/1 system-ui,sans-serif;cursor:pointer}
.wb-popup-open{overflow:hidden}
</style>
`

// wbPopupJS opens the popups on their trigger and remembers the visitor's choice. Values come from
// data attributes the server validated (PopupSettings); the click selector is #id or .class only.
const wbPopupJS = `<script id="wb-popup-js">(function(){
var D=document,H=D.documentElement,K='wb-popup:',list=D.querySelectorAll('[data-wb-popup]');
function st(s){try{return window[s]}catch(e){return null}}
function seen(el){var f=el.getAttribute('data-frequency'),k=K+el.getAttribute('data-wb-popup'),v=el.getAttribute('data-version');
if(f==='always')return false;try{if(f==='session'){var a=st('sessionStorage');return !!a&&a.getItem(k)===v}
var l=st('localStorage');if(!l)return false;var raw=l.getItem(k);if(!raw)return false;var o=JSON.parse(raw);if(o.v!==v)return false;
if(f==='once')return true;return Date.now()-o.t<(+el.getAttribute('data-days'))*864e5}catch(e){return false}}
function mark(el){var f=el.getAttribute('data-frequency'),k=K+el.getAttribute('data-wb-popup'),v=el.getAttribute('data-version');
try{if(f==='session'){var a=st('sessionStorage');if(a)a.setItem(k,v)}else if(f!=='always'){var l=st('localStorage');if(l)l.setItem(k,JSON.stringify({v:v,t:Date.now()}))}}catch(e){}}
function open(el){if(!el.hidden)return;el.hidden=false;el._wbPrev=D.activeElement;H.classList.add('wb-popup-open');
var box=el.querySelector('.wb-popup-box');if(box)box.focus();mark(el)}
function close(el){if(el.hidden)return;el.hidden=true;if(!D.querySelector('[data-wb-popup]:not([hidden])'))H.classList.remove('wb-popup-open');
if(el._wbPrev&&el._wbPrev.focus)try{el._wbPrev.focus()}catch(e){}}
function consent(el,cb){var re=/(^|; )gooike_consent=/;if(!el.getAttribute('data-consent')||re.test(D.cookie))return cb();
var t=setInterval(function(){if(re.test(D.cookie)){clearInterval(t);cb()}},500)}
function auto(el,fn){if(seen(el))return;consent(el,function(){if(!seen(el))fn()})}
Array.prototype.forEach.call(list,function(el){
el.addEventListener('click',function(e){var t=e.target;if(t&&t.closest){var x=t.closest('[data-wb-popup-close]');if(x&&(x.className!=='wb-popup-backdrop'||el.getAttribute('data-overlay')==='1')){close(el)}}});
var tr=el.getAttribute('data-trigger'),delay=(+el.getAttribute('data-delay')||0)*1000;
if(tr==='load'){auto(el,function(){setTimeout(function(){open(el)},delay)})}
else if(tr==='scroll'){var n=+el.getAttribute('data-scroll')||50,on=function(){var h=H.scrollHeight-window.innerHeight;
if(h<=0||(window.pageYOffset/h)*100>=n){window.removeEventListener('scroll',on);auto(el,function(){open(el)})}};window.addEventListener('scroll',on,{passive:true});on()}
else if(tr==='exit'){if(window.matchMedia&&window.matchMedia('(hover:none)').matches){auto(el,function(){setTimeout(function(){open(el)},delay||30000)})}
else{var out=function(e){if(e.clientY<=0&&!e.relatedTarget){D.removeEventListener('mouseout',out);auto(el,function(){open(el)})}};D.addEventListener('mouseout',out)}}
else if(tr==='click'){var sel=el.getAttribute('data-selector');D.addEventListener('click',function(e){var t=e.target;
if(sel&&t&&t.closest&&t.closest(sel)){e.preventDefault();open(el)}})}});
D.addEventListener('keydown',function(e){var el=D.querySelector('[data-wb-popup]:not([hidden])');if(!el)return;
if(e.key==='Escape'||e.key==='Esc'){close(el);return}
if(e.key==='Tab'){var f=el.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select,textarea,[tabindex]:not([tabindex="-1"])');
if(!f.length){e.preventDefault();return}var a=f[0],z=f[f.length-1];
if(e.shiftKey&&(D.activeElement===a||!el.contains(D.activeElement))){e.preventDefault();z.focus()}
else if(!e.shiftKey&&(D.activeElement===z||!el.contains(D.activeElement))){e.preventDefault();a.focus()}}});
})();</script>
`

// --- REST: GET/PUT templates/{id}/popup ----------------------------------------------------------

// wbPopupTemplate loads the {id} template and answers 422 unless it is a popup.
func (s *Server) wbPopupTemplate(c *gin.Context, r *restRequest) *store.WBTemplate {
	_, t := s.wbTemplateFor(c, r)
	if t == nil {
		return nil
	}
	if t.Type != webbuilder.TemplatePopup {
		wbBadTemplate(c, "Only a popup template has popup settings.")
		return nil
	}
	return t
}

// wbRESTPopupGet is GET templates/{id}/popup → PopupSettings (the defaults when none is stored).
func (s *Server) wbRESTPopupGet(c *gin.Context, r *restRequest) {
	t := s.wbPopupTemplate(c, r)
	if t == nil {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, s.wbPopupSettings(c, t.ID))
}

// wbRESTPopupPut is PUT templates/{id}/popup: a full PopupSettings object; 422 on an invalid value.
func (s *Server) wbRESTPopupPut(c *gin.Context, r *restRequest) {
	t := s.wbPopupTemplate(c, r)
	if t == nil {
		return
	}
	raw, ok := wbBody(c, r, 4<<10)
	if !ok {
		return
	}
	in := webbuilder.DefaultPopupSettings()
	if !wbDecode(c, raw, &in) {
		return
	}
	in.Selector = strings.TrimSpace(in.Selector)
	if msg := webbuilder.ValidatePopup(in); msg != "" {
		wbBadTemplate(c, msg)
		return
	}
	b, _ := json.Marshal(in)
	if err := s.store.WebBuilder().SetTemplatePopup(c, t.ID, string(b)); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the popup settings.")
		return
	}
	s.wbTemplatesChanged(c)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, in)
}
