/**
 * Script-injection payloads for the element editor's static frame (security review 2026-11-17,
 * findings 1–2, plus a broad mutation-XSS list). Each payload calls `js`, which in the browser
 * regression (editor-xss.mjs) records the editor's REST nonce if it ever runs. Shared by the
 * unit tests (test/host/elements-neutral.test.ts, jsdom) and the Playwright regression (Chromium).
 */
export type Payload = [name: string, html: (js: string) => string]

/** A JS snippet without spaces or quotes (fits unquoted attributes and URLs). */
export const hitJs = (name: string) => 'top.hit(`' + name + ':`+top.__WEB_BUILDER__.rest.nonce)'

const ent = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

export const PAYLOADS: Payload[] = [
  // the review's proofs
  ['control_script', (j) => `<script>${j}</script>`],
  ['control_img', (j) => `<img src=x onerror="${j}">`],
  ['svg_script', (j) => `<svg><script>${j}</script></svg>`],
  ['math_mtext_script', (j) => `<math><mtext><script>${j}</script></mtext></math>`],
  ['svg_p_breakout', (j) => `<svg><p><script>${j}</script></p></svg>`],
  ['comment_abrupt', (j) => `<!--><script>${j}</script>-->`],
  ['comment_abrupt2', (j) => `<!---><img src=x onerror="${j}">-->`],
  ['comment_bang', (j) => `<!-- x --!><script>${j}</script> -->`],
  ['comment_nested', (j) => `<!--<!--><script>${j}</script>-->`],
  ['comment_bang_open', (j) => `<!--!><script>${j}</script>-->`],
  ['iframe_js', (j) => `<iframe src="javascript:${j}"></iframe>`],
  ['iframe_srcdoc', (j) => `<iframe srcdoc="${ent(`<script>${j}</script>`)}"></iframe>`],
  ['iframe_srcdoc_img', (j) => `<iframe srcdoc="${ent(`<img src=x onerror=${j}>`)}"></iframe>`],
  ['iframe_data_html', (j) => `<iframe src="data:text/html,<script>${j}</script>"></iframe>`],
  ['iframe_same_origin', () => `<iframe src="/wp-admin/"></iframe>`],
  ['dsd', (j) => `<div><template shadowrootmode="open"><img src=x onerror="${j}"></template></div>`],
  ['dsd_script', (j) => `<div><template shadowrootmode="open"><script>${j}</script></template></div>`],
  ['dsd_closed', (j) => `<div><template shadowrootmode="closed"><script>${j}</script></template></div>`],
  ['dsd_legacy', (j) => `<div><template shadowroot="open"><script>${j}</script></template></div>`],
  ['template_script', (j) => `<template><script>${j}</script></template>`],
  ['object_js', (j) => `<object data="javascript:${j}"></object>`],
  ['object_html', (j) => `<object data="data:text/html,<script>${j}</script>"></object>`],
  ['object_same_origin', () => `<object type="text/html" data="/wp-admin/"></object>`],
  ['embed_js', (j) => `<embed src="javascript:${j}">`],
  ['embed_svg', (j) => `<embed src="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' onload='${j}'/>">`],
  ['script_tab_module', (j) => `<script\ttype=module>${j}</script>`],
  ['dup_type', (j) => `<script type="" type="text/x-wb-inert">${j}</script>`],
  ['script_in_select', (j) => `<select><script>${j}</script></select>`],
  ['script_src_data', (j) => `<script src="data:text/javascript,${j}"></script>`],
  ['svg_script_href', (j) => `<svg><script href="data:text/javascript,${j}"></script></svg>`],
  ['svg_script_xlink', (j) => `<svg><script xlink:href="data:text/javascript,${j}"></script></svg>`],
  // raw-text context breaks (mXSS)
  ['noscript_mx', (j) => `<noscript><p title="</noscript><img src=x onerror=${j}>"></noscript>`],
  ['noscript_img', (j) => `<noscript><img src=x onerror=${j}></noscript>`],
  ['textarea_mx', (j) => `<textarea><img title="</textarea><img src=x onerror=${j}>"></textarea>`],
  ['style_mx', (j) => `<style><img title="</style><img src=x onerror=${j}>"></style>`],
  ['title_mx', (j) => `<title><img title="</title><img src=x onerror=${j}>"></title>`],
  ['xmp_mx', (j) => `<xmp><img title="</xmp><img src=x onerror=${j}>"></xmp>`],
  ['noembed_mx', (j) => `<noembed><img title="</noembed><img src=x onerror=${j}>"></noembed>`],
  ['noframes_mx', (j) => `<noframes><img title="</noframes><img src=x onerror=${j}>"></noframes>`],
  ['iframe_text_mx', (j) => `<iframe><img title="</iframe><img src=x onerror=${j}>"></iframe>`],
  ['plaintext', (j) => `<plaintext><img src=x onerror=${j}>`],
  // namespace confusion
  ['svg_style_img', (j) => `<svg><style><img src=x onerror="${j}"></style></svg>`],
  ['svg_style_title', (j) => `<svg><style><a title="</style><img src=x onerror=${j}>"></style></svg>`],
  ['math_style', (j) => `<math><style><img src=x onerror="${j}"></style></math>`],
  ['math_mglyph', (j) => `<math><mtext><table><mglyph><style><!--</style><img title="--&gt;&lt;/mglyph&gt;&lt;img&Tab;src=1&Tab;onerror=${j}&gt;">`],
  ['form_nesting', (j) => `<form><math><mtext></form><form><mglyph><style></math><img src onerror=${j}>`],
  ['svg_p_style', (j) => `<svg></p><style><a id="</style><img src=1 onerror=${j}>">`],
  ['table_svg', (j) => `<table><svg><desc><img src=x onerror=${j}></desc></svg></table>`],
  ['table_td_svg_style', (j) => `<table><tr><td><svg><style><img src=x onerror=${j}></style></svg></td></tr></table>`],
  ['foreign_object', (j) => `<svg><foreignObject><img src=x onerror=${j}></foreignObject></svg>`],
  ['svg_cdata', (j) => `<svg><![CDATA[><img src=x onerror=${j}>]]></svg>`],
  ['math_annotation', (j) => `<math><annotation-xml encoding="text/html"><img src=x onerror=${j}></annotation-xml></math>`],
  // attributes
  ['unquoted', (j) => `<img src=x onerror=${j}>`],
  ['svg_onload', (j) => `<svg onload="${j}"></svg>`],
  ['details_toggle', (j) => `<details open ontoggle=${j}><summary>x</summary></details>`],
  ['input_image', (j) => `<input type=image src=x onerror=${j}>`],
  ['video_source', (j) => `<video><source src=x onerror=${j}></video>`],
  ['body_onload', (j) => `<body onload=${j}>`],
  ['custom_element', (j) => `<x-foo onclick=${j}>x</x-foo>`],
  ['null_attr', (j) => `<img src=x on\u0000error=${j}>`],
  ['svg_animate_begin', (j) => `<svg><animate onbegin=${j} attributeName=x dur=1s></animate></svg>`],
  ['svg_set_handler', (j) => `<svg><rect width=10 height=10><set attributeName="onmouseover" to="${j}"/></rect></svg>`],
  // URLs
  ['href_js', (j) => `<a href="javascript:${j}">x</a>`],
  ['href_upper', (j) => `<a href="JAVASCRIPT:${j}">x</a>`],
  ['href_colon_entity', (j) => `<a href="javascript&colon;${j}">x</a>`],
  ['href_dec_entity', (j) => `<a href="&#106;avascript:${j}">x</a>`],
  ['href_hex_entity', (j) => `<a href="&#x6A;avascript:${j}">x</a>`],
  ['href_tab', (j) => `<a href="java\tscript:${j}">x</a>`],
  ['href_newline', (j) => `<a href="java\nscript:${j}">x</a>`],
  ['href_tab_entity', (j) => `<a href="java&Tab;script:${j}">x</a>`],
  ['href_lead_space', (j) => `<a href="  \u0001javascript:${j}">x</a>`],
  ['href_vbscript', () => `<a href="vbscript:msgbox(1)">x</a>`],
  ['href_data_html', (j) => `<a href="data:text/html,<script>${j}</script>">x</a>`],
  ['area_js', (j) => `<map name=m><area href="javascript:${j}" shape=rect coords=0,0,9,9></map>`],
  ['form_action', (j) => `<form action="javascript:${j}"><button>x</button></form>`],
  ['formaction', (j) => `<form><button formaction="javascript:${j}">x</button></form>`],
  ['iframe_colon_entity', (j) => `<iframe src="javascript&colon;${j}"></iframe>`],
  ['srcset_js', (j) => `<img srcset="javascript:${j} 1x">`],
  ['video_poster', (j) => `<video poster="javascript:${j}"></video>`],
  ['math_href', (j) => `<math href="javascript:${j}"><mtext>x</mtext></math>`],
  ['svg_a_xlink', (j) => `<svg><a xlink:href="javascript:${j}"><text y=20>x</text></a></svg>`],
  ['svg_a_href', (j) => `<svg><a href="javascript:${j}"><text y=20>x</text></a></svg>`],
  ['svg_animate_href', (j) => `<svg><a><animate attributeName="href" values="javascript:${j}"/><text y=20>x</text></a></svg>`],
  ['svg_set_href', (j) => `<svg><a><set attributeName="xlink:href" to="javascript:${j}"/><text y=20>x</text></a></svg>`],
  ['svg_use_data', (j) => `<svg><use href="data:image/svg+xml,<svg id='x' xmlns='http://www.w3.org/2000/svg'><image href='1' onerror='${j}'/></svg>#x"/></svg>`],
  ['a_ping', (j) => `<a href="#" ping="javascript:${j}">x</a>`],
  // documents, heads
  ['meta_refresh', (j) => `<meta http-equiv="refresh" content="0;url=javascript:${j}">`],
  ['meta_refresh_upper', () => `<meta HTTP-EQUIV="Refresh" content="0;url=/wp-admin/">`],
  ['base_js', (j) => `<base href="javascript:${j}//">`],
  ['base_other', () => `<base href="https://evil.example/">`],
  ['link_js', (j) => `<link rel=stylesheet href="javascript:${j}">`],
  // the editor's own markers and attributes
  ['fake_marker', () => `<!--/wb:p1--><div>x</div><!--wb:p1-->`],
  ['fake_editor_attr', (j) => `<base data-wb-editor href="javascript:${j}//"><img data-wb-editor src=x onerror=${j}>`],
]

/** Head markup payloads (content.head). */
export const HEAD_PAYLOADS: Payload[] = [
  ['head_script', (j) => `<script>${j}</script>`],
  ['head_meta_refresh', (j) => `<meta http-equiv="refresh" content="0;url=javascript:${j}">`],
  ['head_base', (j) => `<base href="javascript:${j}//">`],
  ['head_noscript', (j) => `<noscript><img src=x onerror=${j}></noscript>`],
  ['head_link_js', (j) => `<link rel=stylesheet href="javascript:${j}">`],
  ['head_svg_script', (j) => `<svg><script>${j}</script></svg>`],
]
