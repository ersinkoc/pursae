package wp

import (
	"regexp"
	"strings"
)

// safeStyleCSS is the default safe_style_css list of safecss_filter_attr() (wp-includes/kses.php).
var safeStyleCSS = map[string]bool{}

func init() {
	for _, p := range strings.Fields(`
		background background-color background-image background-position background-repeat background-size
		background-attachment background-blend-mode
		border border-radius border-width border-color border-style
		border-right border-right-color border-right-style border-right-width
		border-bottom border-bottom-color border-bottom-left-radius border-bottom-right-radius border-bottom-style border-bottom-width
		border-left border-left-color border-left-style border-left-width
		border-top border-top-color border-top-left-radius border-top-right-radius border-top-style border-top-width
		border-spacing border-collapse caption-side
		columns column-count column-fill column-gap column-rule column-span column-width
		display
		color filter font font-family font-size font-style font-variant font-weight letter-spacing line-height
		text-align text-decoration text-indent text-transform white-space
		height min-height max-height width min-width max-width
		margin margin-right margin-bottom margin-left margin-top
		margin-block-start margin-block-end margin-inline-start margin-inline-end
		padding padding-right padding-bottom padding-left padding-top
		padding-block-start padding-block-end padding-inline-start padding-inline-end
		flex flex-basis flex-direction flex-flow flex-grow flex-shrink flex-wrap
		gap row-gap
		grid-template-columns grid-auto-columns grid-column-start grid-column-end grid-column grid-column-gap
		grid-template-rows grid-auto-rows grid-row-start grid-row-end grid-row grid-row-gap grid-gap
		justify-content justify-items justify-self align-content align-items align-self
		clear cursor direction float list-style-type object-fit object-position opacity overflow vertical-align writing-mode
		position top right bottom left z-index box-shadow aspect-ratio container-type
		fill fill-opacity fill-rule
		stroke stroke-dasharray stroke-dashoffset stroke-linecap stroke-linejoin stroke-miterlimit stroke-opacity stroke-width
		color-interpolation color-interpolation-filters paint-order stop-color stop-opacity flood-color flood-opacity lighting-color
		marker marker-end marker-mid marker-start
		clip-path clip-rule mask mask-type
		cx cy r rx ry x y d
		alignment-baseline baseline-shift dominant-baseline glyph-orientation-horizontal glyph-orientation-vertical
		text-anchor unicode-bidi word-spacing font-size-adjust font-stretch
		color-rendering image-rendering shape-rendering text-rendering vector-effect
		transform transform-origin pointer-events visibility`) {
		safeStyleCSS[p] = true
	}
}

// cssURLDataTypes are the properties whose values may hold url().
var cssURLDataTypes = map[string]bool{"background": true, "background-image": true, "cursor": true, "filter": true,
	"list-style": true, "list-style-image": true, "clip-path": true, "fill": true, "marker": true, "marker-end": true,
	"marker-mid": true, "marker-start": true, "mask": true, "stroke": true}

var (
	cssCustomVarRe = regexp.MustCompile(`^--[a-zA-Z0-9-_]+$`)
	cssURLRe       = regexp.MustCompile(`url\([^)]+\)`)
	cssURLPartsRe  = regexp.MustCompile(`^url\(\s*(['"]?)(.*?)['"]?\s*\)$`)
	cssGradientRe  = regexp.MustCompile(`(?:repeating-)?(?:linear|radial|conic)-gradient\((?:[^()]|\([^()]*\))*\)`)
	cssFuncNameRe  = regexp.MustCompile(`\b(?:var|calc|min|max|minmax|clamp|repeat|matrix|matrix3d|perspective|rotate|rotate3d|rotateX|rotateY|rotateZ|scale|scale3d|scaleX|scaleY|scaleZ|skew|skewX|skewY|translate|translate3d|translateX|translateY|translateZ|circle|ellipse|inset|path|polygon|rect|shape|xywh)\(`)
	cssUnsafeRe    = regexp.MustCompile(`[\\(&=}]|/\*`)
)

// SafeCSS is safecss_filter_attr(): it keeps the declarations of an inline style whose property is
// in WordPress' safe_style_css list (or a custom property) and whose value holds no \ ( & } = or
// comment outside url() with an allowed protocol, gradients and value functions such as var() and calc().
func SafeCSS(css string) string {
	css = strings.NewReplacer("\x00", "", "\n", "", "\r", "", "\t", "").Replace(css)
	var out []string
	for _, item := range strings.Split(strings.TrimSpace(css), ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		test := item
		found, urlAttr, gradientAttr := false, false, false
		value := ""
		if !strings.Contains(item, ":") {
			found = true
		} else {
			prop, val, _ := strings.Cut(item, ":")
			prop = strings.TrimSpace(prop)
			value = val
			if safeStyleCSS[prop] {
				found = true
				urlAttr = cssURLDataTypes[prop]
				gradientAttr = prop == "background" || prop == "background-image"
			}
			if cssCustomVarRe.MatchString(prop) {
				found = true
				v := strings.TrimSpace(val)
				urlAttr = strings.HasPrefix(v, "url(")
				gradientAttr = strings.Contains(v, "-gradient(")
			}
		}
		if found && urlAttr {
			for _, m := range cssURLRe.FindAllString(value, -1) {
				parts := cssURLPartsRe.FindStringSubmatch(m)
				if parts == nil || strings.TrimSpace(parts[2]) == "" {
					found = false
					break
				}
				u := strings.TrimSpace(parts[2])
				if s, ok := urlScheme(u); ok && !allowedProtocol(s) {
					found = false
					break
				}
				test = strings.ReplaceAll(test, m, "")
			}
		}
		if found && gradientAttr {
			for _, m := range cssGradientRe.FindAllString(test, -1) {
				test = strings.ReplaceAll(test, m, "")
			}
		}
		if !found {
			continue
		}
		var ok bool
		if test, ok = stripCSSFunctions(test); !ok {
			continue
		}
		if cssUnsafeRe.MatchString(test) {
			continue
		}
		out = append(out, item)
	}
	return strings.Join(out, ";")
}

// stripCSSFunctions removes var(...), calc(...) and the other value functions safecss_filter_attr()
// allows, including nested balanced parentheses. ok is false when parentheses do not balance.
func stripCSSFunctions(s string) (string, bool) {
	for {
		loc := cssFuncNameRe.FindStringIndex(s)
		if loc == nil {
			return s, true
		}
		depth, end := 0, -1
		for i := loc[1] - 1; i < len(s); i++ {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				end = i
				break
			}
		}
		if end < 0 {
			return s, false
		}
		s = s[:loc[0]] + s[end+1:]
	}
}

func allowedProtocol(scheme string) bool {
	for _, p := range AllowedProtocols {
		if strings.EqualFold(p, scheme) {
			return true
		}
	}
	return false
}
