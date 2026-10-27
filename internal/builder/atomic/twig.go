package atomic

// A small Twig 3 interpreter for the atomic element templates: the subset the upstream templates
// use (text with whitespace control, {{ }} output with html autoescaping, set, if/elseif/else, for,
// import/macro, comments; expressions with literals, arrays, hashes, attribute and subscript
// access, filters, tests, macro calls, not/and/or, comparisons, in, ~, + - * /, ternaries).
//
// Whitespace follows Twig's lexer exactly: "{%-"/"{{-"/"{#-" trim all whitespace before the tag,
// "-%}"/"-}}"/"-#}" all whitespace after it, and a block or comment tag eats one newline after it.

import (
	"fmt"
	"strconv"
	"strings"
)

// Markup is a string that is safe for HTML output (Twig\Markup).
type Markup string

// ---- lexer ------------------------------------------------------------------------------------

type tokKind int

const (
	tText tokKind = iota
	tVarStart
	tVarEnd
	tBlockStart
	tBlockEnd
	tName
	tNumber
	tString
	tOp
	tPunct
	tEOF
)

type token struct {
	kind tokKind
	val  string
	num  float64
	line int
}

var twigOps = []string{"starts with", "ends with", "not in", "is not", "same as", "==", "!=", "<=", ">=", "??", "//", "**", "..", "<", ">", "+", "-", "*", "/", "%", "~", "?", ":", "=", "|"}

func lexTwig(src string) ([]token, error) {
	var toks []token
	i := 0
	n := len(src)
	line := 1
	for i < n {
		// text up to the next tag
		j := i
		for j < n {
			if src[j] == '{' && j+1 < n && (src[j+1] == '{' || src[j+1] == '%' || src[j+1] == '#') {
				break
			}
			j++
		}
		text := src[i:j]
		if j < n && j+2 < n && src[j+2] == '-' {
			text = strings.TrimRight(text, " \t\n\r\x00\x0B")
		} else if j < n && j+2 < n && src[j+2] == '~' {
			text = strings.TrimRight(text, " \t\x00\x0B")
		}
		if text != "" {
			toks = append(toks, token{kind: tText, val: text, line: line})
		}
		line += strings.Count(src[i:j], "\n")
		if j >= n {
			break
		}
		kind := src[j+1]
		i = j + 2
		if i < n && (src[i] == '-' || src[i] == '~') {
			i++
		}
		switch kind {
		case '#':
			end := strings.Index(src[i:], "#}")
			if end < 0 {
				return nil, fmt.Errorf("unclosed comment at line %d", line)
			}
			body := src[i : i+end]
			line += strings.Count(body, "\n")
			i += end + 2
			if strings.HasSuffix(body, "-") {
				i = skipWS(src, i, true)
			} else if strings.HasSuffix(body, "~") {
				i = skipWS(src, i, false)
			} else if i < n && src[i] == '\n' {
				i++
			}
		case '{', '%':
			start, endTok := tVarStart, "}}"
			if kind == '%' {
				start, endTok = tBlockStart, "%}"
			}
			toks = append(toks, token{kind: start, line: line})
			// expression tokens until the end delimiter
			for {
				for i < n && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
					if src[i] == '\n' {
						line++
					}
					i++
				}
				if i >= n {
					return nil, fmt.Errorf("unclosed tag at line %d", line)
				}
				trim := byte(0)
				if (src[i] == '-' || src[i] == '~') && strings.HasPrefix(src[i+1:], endTok) {
					trim = src[i]
					i++
				}
				if strings.HasPrefix(src[i:], endTok) {
					i += 2
					if start == tVarStart {
						toks = append(toks, token{kind: tVarEnd, line: line})
					} else {
						toks = append(toks, token{kind: tBlockEnd, line: line})
					}
					switch {
					case trim == '-':
						i = skipWS(src, i, true)
					case trim == '~':
						i = skipWS(src, i, false)
					case start == tBlockStart && i < n && src[i] == '\n':
						i++
					}
					break
				}
				if trim != 0 { // a '-' that was not a trim marker
					i--
				}
				t, adv, err := lexExprToken(src[i:], line)
				if err != nil {
					return nil, err
				}
				toks = append(toks, t)
				i += adv
			}
		}
	}
	toks = append(toks, token{kind: tEOF, line: line})
	return toks, nil
}

func skipWS(src string, i int, newlines bool) int {
	for i < len(src) {
		c := src[i]
		if c == ' ' || c == '\t' || c == 0 || c == '\x0B' || (newlines && (c == '\n' || c == '\r')) {
			i++
			continue
		}
		break
	}
	return i
}

func isNameStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}
func isNameChar(c byte) bool { return isNameStart(c) || c >= '0' && c <= '9' }

func lexExprToken(s string, line int) (token, int, error) {
	c := s[0]
	switch {
	case c >= '0' && c <= '9':
		j := 0
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '_') {
			j++
		}
		if j+1 < len(s) && s[j] == '.' && s[j+1] >= '0' && s[j+1] <= '9' {
			j++
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
		}
		f, _ := strconv.ParseFloat(strings.ReplaceAll(s[:j], "_", ""), 64)
		return token{kind: tNumber, val: s[:j], num: f, line: line}, j, nil
	case c == '\'' || c == '"':
		var b strings.Builder
		j := 1
		for j < len(s) && s[j] != c {
			if s[j] == '\\' && j+1 < len(s) {
				j++
				switch s[j] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(s[j])
				}
				j++
				continue
			}
			b.WriteByte(s[j])
			j++
		}
		if j >= len(s) {
			return token{}, 0, fmt.Errorf("unclosed string at line %d", line)
		}
		return token{kind: tString, val: b.String(), line: line}, j + 1, nil
	}
	// word operators
	for _, op := range []string{"starts with", "ends with", "not in", "is not", "same as"} {
		if strings.HasPrefix(s, op) && (len(s) == len(op) || !isNameChar(s[len(op)])) {
			return token{kind: tOp, val: op, line: line}, len(op), nil
		}
	}
	if isNameStart(c) {
		j := 1
		for j < len(s) && isNameChar(s[j]) {
			j++
		}
		w := s[:j]
		switch w {
		case "and", "or", "not", "in", "is", "b-and", "b-or", "b-xor", "matches":
			return token{kind: tOp, val: w, line: line}, j, nil
		}
		return token{kind: tName, val: w, line: line}, j, nil
	}
	for _, op := range twigOps {
		if strings.HasPrefix(s, op) && op != "starts with" && op != "ends with" && op != "not in" && op != "is not" && op != "same as" {
			return token{kind: tOp, val: op, line: line}, len(op), nil
		}
	}
	if strings.ContainsRune("()[]{},.", rune(c)) {
		return token{kind: tPunct, val: string(c), line: line}, 1, nil
	}
	return token{}, 0, fmt.Errorf("unexpected character %q at line %d", c, line)
}

// ---- AST --------------------------------------------------------------------------------------

type tNode interface{}

type (
	textNode  struct{ text string }
	printNode struct{ expr tExpr }
	setNode   struct {
		names []string
		exprs []tExpr
	}
	ifNode struct {
		conds  []tExpr
		bodies [][]tNode
		els    []tNode
	}
	forNode struct {
		key, val string
		seq      tExpr
		body     []tNode
		els      []tNode
	}
	importNode struct {
		tpl   tExpr
		alias string
	}
	macroNode struct {
		name     string
		args     []string
		defaults []tExpr
		body     []tNode
	}
)

type tExpr interface{}

type (
	constExpr struct{ v any }
	nameExpr  struct{ name string }
	arrayExpr struct{ items []tExpr }
	hashExpr  struct {
		keys []tExpr
		vals []tExpr
	}
	getAttrExpr struct {
		obj tExpr
		key tExpr
		dot bool // obj.key (vs obj[key])
	}
	callExpr struct { // obj.method(args) (macro calls)
		obj  tExpr
		name string
		args []tExpr
	}
	funcExpr struct {
		name string
		args []tExpr
	}
	filterExpr struct {
		node tExpr
		name string
		args []tExpr
	}
	testExpr struct {
		node tExpr
		name string
		args []tExpr
		not  bool
	}
	unaryExpr struct {
		op   string
		node tExpr
	}
	binaryExpr struct {
		op          string
		left, right tExpr
	}
	condExpr struct{ cond, a, b tExpr }
)

// ---- parser -----------------------------------------------------------------------------------

type tParser struct {
	toks []token
	pos  int
}

func (p *tParser) peek() token { return p.toks[p.pos] }
func (p *tParser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *tParser) is(kind tokKind, val string) bool {
	t := p.peek()
	return t.kind == kind && (val == "" || t.val == val)
}

func (p *tParser) expect(kind tokKind, val string) (token, error) {
	t := p.next()
	if t.kind != kind || (val != "" && t.val != val) {
		return t, fmt.Errorf("line %d: expected %q, got %q", t.line, val, t.val)
	}
	return t, nil
}

// parseBody parses nodes until one of the end tags (returns its name).
func (p *tParser) parseBody(ends ...string) ([]tNode, string, error) {
	var out []tNode
	for {
		t := p.peek()
		switch t.kind {
		case tEOF:
			if len(ends) > 0 {
				return nil, "", fmt.Errorf("unexpected end of template, expected %v", ends)
			}
			return out, "", nil
		case tText:
			p.next()
			out = append(out, &textNode{t.val})
		case tVarStart:
			p.next()
			e, err := p.parseExpr(0)
			if err != nil {
				return nil, "", err
			}
			if _, err := p.expect(tVarEnd, ""); err != nil {
				return nil, "", err
			}
			out = append(out, &printNode{e})
		case tBlockStart:
			p.next()
			name, err := p.expect(tName, "")
			if err != nil {
				return nil, "", err
			}
			for _, e := range ends {
				if name.val == e {
					return out, e, nil
				}
			}
			n, err := p.parseTag(name.val)
			if err != nil {
				return nil, "", err
			}
			if n != nil {
				out = append(out, n)
			}
		default:
			return nil, "", fmt.Errorf("line %d: unexpected token %q", t.line, t.val)
		}
	}
}

func (p *tParser) parseTag(name string) (tNode, error) {
	switch name {
	case "set":
		var names []string
		for {
			t, err := p.expect(tName, "")
			if err != nil {
				return nil, err
			}
			names = append(names, t.val)
			if !p.is(tPunct, ",") {
				break
			}
			p.next()
		}
		if _, err := p.expect(tOp, "="); err != nil {
			return nil, err
		}
		var exprs []tExpr
		for {
			e, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, e)
			if !p.is(tPunct, ",") {
				break
			}
			p.next()
		}
		_, err := p.expect(tBlockEnd, "")
		return &setNode{names, exprs}, err
	case "if":
		n := &ifNode{}
		cond, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tBlockEnd, ""); err != nil {
			return nil, err
		}
		for {
			body, end, err := p.parseBody("elseif", "else", "endif")
			if err != nil {
				return nil, err
			}
			n.conds = append(n.conds, cond)
			n.bodies = append(n.bodies, body)
			switch end {
			case "elseif":
				if cond, err = p.parseExpr(0); err != nil {
					return nil, err
				}
				if _, err := p.expect(tBlockEnd, ""); err != nil {
					return nil, err
				}
				continue
			case "else":
				if _, err := p.expect(tBlockEnd, ""); err != nil {
					return nil, err
				}
				if n.els, _, err = p.parseBody("endif"); err != nil {
					return nil, err
				}
			}
			_, err = p.expect(tBlockEnd, "")
			return n, err
		}
	case "for":
		n := &forNode{}
		t, err := p.expect(tName, "")
		if err != nil {
			return nil, err
		}
		n.val = t.val
		if p.is(tPunct, ",") {
			p.next()
			t2, err := p.expect(tName, "")
			if err != nil {
				return nil, err
			}
			n.key, n.val = t.val, t2.val
		}
		if _, err := p.expect(tOp, "in"); err != nil {
			return nil, err
		}
		if n.seq, err = p.parseExpr(0); err != nil {
			return nil, err
		}
		if _, err := p.expect(tBlockEnd, ""); err != nil {
			return nil, err
		}
		body, end, err := p.parseBody("else", "endfor")
		if err != nil {
			return nil, err
		}
		n.body = body
		if end == "else" {
			if _, err := p.expect(tBlockEnd, ""); err != nil {
				return nil, err
			}
			if n.els, _, err = p.parseBody("endfor"); err != nil {
				return nil, err
			}
		}
		_, err = p.expect(tBlockEnd, "")
		return n, err
	case "import":
		tpl, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tName, "as"); err != nil {
			return nil, err
		}
		alias, err := p.expect(tName, "")
		if err != nil {
			return nil, err
		}
		_, err = p.expect(tBlockEnd, "")
		return &importNode{tpl, alias.val}, err
	case "macro":
		t, err := p.expect(tName, "")
		if err != nil {
			return nil, err
		}
		m := &macroNode{name: t.val}
		if _, err := p.expect(tPunct, "("); err != nil {
			return nil, err
		}
		for !p.is(tPunct, ")") {
			a, err := p.expect(tName, "")
			if err != nil {
				return nil, err
			}
			m.args = append(m.args, a.val)
			var def tExpr
			if p.is(tOp, "=") {
				p.next()
				if def, err = p.parseExpr(0); err != nil {
					return nil, err
				}
			}
			m.defaults = append(m.defaults, def)
			if p.is(tPunct, ",") {
				p.next()
			}
		}
		p.next()
		if _, err := p.expect(tBlockEnd, ""); err != nil {
			return nil, err
		}
		if m.body, _, err = p.parseBody("endmacro"); err != nil {
			return nil, err
		}
		for !p.is(tBlockEnd, "") {
			p.next()
		}
		p.next()
		return m, nil
	}
	return nil, fmt.Errorf("unknown tag %q", name)
}

var binPrec = map[string]int{
	"or": 10, "and": 15, "b-or": 16, "b-xor": 17, "b-and": 18,
	"==": 20, "!=": 20, "<": 20, ">": 20, ">=": 20, "<=": 20, "in": 20, "not in": 20, "matches": 20, "starts with": 20, "ends with": 20,
	"..": 25, "+": 30, "-": 30, "~": 40, "*": 60, "/": 60, "//": 60, "%": 60, "is": 100, "is not": 100, "**": 200, "??": 300,
}

func (p *tParser) parseExpr(minPrec int) (tExpr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tOp {
			break
		}
		prec, ok := binPrec[t.val]
		if !ok || prec < minPrec {
			break
		}
		p.next()
		if t.val == "is" || t.val == "is not" {
			left, err = p.parseTest(left, t.val == "is not")
			if err != nil {
				return nil, err
			}
			continue
		}
		next := prec + 1
		if t.val == "**" || t.val == "??" {
			next = prec
		}
		right, err := p.parseExpr(next)
		if err != nil {
			return nil, err
		}
		left = &binaryExpr{t.val, left, right}
	}
	if minPrec == 0 && p.is(tOp, "?") {
		p.next()
		if p.is(tOp, ":") { // a ?: b
			p.next()
			b, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			return &condExpr{left, left, b}, nil
		}
		a, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		var b tExpr = &constExpr{nil}
		if p.is(tOp, ":") {
			p.next()
			if b, err = p.parseExpr(0); err != nil {
				return nil, err
			}
		}
		return &condExpr{left, a, b}, nil
	}
	return left, nil
}

func (p *tParser) parseTest(node tExpr, not bool) (tExpr, error) {
	t := p.next()
	name := t.val
	if t.kind == tOp && t.val == "not" { // "is not" lexed as two words in some spacing
		not = !not
		t = p.next()
		name = t.val
	}
	if t.kind == tOp && t.val == "same as" {
		name = "same as"
	} else if name == "same" || name == "divisible" {
		w := p.next()
		name += " " + w.val
	}
	te := &testExpr{node: node, name: name, not: not}
	if p.is(tPunct, "(") {
		args, err := p.parseArgs()
		if err != nil {
			return nil, err
		}
		te.args = args
	}
	return te, nil
}

func (p *tParser) parseUnary() (tExpr, error) {
	t := p.peek()
	if t.kind == tOp && (t.val == "not" || t.val == "-" || t.val == "+") {
		p.next()
		prec := 500
		if t.val == "not" {
			prec = 50
		}
		e, err := p.parseExpr(prec)
		if err != nil {
			return nil, err
		}
		return &unaryExpr{t.val, e}, nil
	}
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return p.parsePostfix(e)
}

func (p *tParser) parseArgs() ([]tExpr, error) {
	if _, err := p.expect(tPunct, "("); err != nil {
		return nil, err
	}
	var args []tExpr
	for !p.is(tPunct, ")") {
		// named arguments (name = value) are passed positionally
		if p.peek().kind == tName && p.pos+1 < len(p.toks) && p.toks[p.pos+1].kind == tOp && p.toks[p.pos+1].val == "=" {
			p.next()
			p.next()
		}
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, e)
		if p.is(tPunct, ",") {
			p.next()
		} else if !p.is(tPunct, ")") {
			return nil, fmt.Errorf("line %d: expected , or )", p.peek().line)
		}
	}
	p.next()
	return args, nil
}

func (p *tParser) parsePrimary() (tExpr, error) {
	t := p.next()
	switch t.kind {
	case tNumber:
		return &constExpr{t.num}, nil
	case tString:
		return &constExpr{t.val}, nil
	case tName:
		switch t.val {
		case "true", "TRUE":
			return &constExpr{true}, nil
		case "false", "FALSE":
			return &constExpr{false}, nil
		case "null", "NULL", "none", "NONE":
			return &constExpr{nil}, nil
		}
		if p.is(tPunct, "(") {
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			return &funcExpr{t.val, args}, nil
		}
		return &nameExpr{t.val}, nil
	case tPunct:
		switch t.val {
		case "(":
			e, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			_, err = p.expect(tPunct, ")")
			return e, err
		case "[":
			a := &arrayExpr{}
			for !p.is(tPunct, "]") {
				e, err := p.parseExpr(0)
				if err != nil {
					return nil, err
				}
				a.items = append(a.items, e)
				if p.is(tPunct, ",") {
					p.next()
				}
			}
			p.next()
			return a, nil
		case "{":
			h := &hashExpr{}
			for !p.is(tPunct, "}") {
				var k tExpr
				kt := p.peek()
				switch {
				case kt.kind == tString || kt.kind == tNumber:
					p.next()
					if kt.kind == tString {
						k = &constExpr{kt.val}
					} else {
						k = &constExpr{phpNumber(kt.num)}
					}
				case kt.kind == tName:
					p.next()
					k = &constExpr{kt.val}
				case kt.kind == tPunct && kt.val == "(":
					p.next()
					e, err := p.parseExpr(0)
					if err != nil {
						return nil, err
					}
					p.next()
					k = e
				default:
					return nil, fmt.Errorf("line %d: bad hash key", kt.line)
				}
				if _, err := p.expect(tOp, ":"); err != nil {
					return nil, err
				}
				v, err := p.parseExpr(0)
				if err != nil {
					return nil, err
				}
				h.keys = append(h.keys, k)
				h.vals = append(h.vals, v)
				if p.is(tPunct, ",") {
					p.next()
				}
			}
			p.next()
			return h, nil
		}
	case tOp:
		if t.val == "-" {
			e, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			return &unaryExpr{"-", e}, nil
		}
	}
	return nil, fmt.Errorf("line %d: unexpected %q", t.line, t.val)
}

func (p *tParser) parsePostfix(e tExpr) (tExpr, error) {
	for {
		switch {
		case p.is(tPunct, "."):
			p.next()
			t := p.next()
			var key string
			switch t.kind {
			case tName, tString:
				key = t.val
			case tNumber:
				key = phpNumber(t.num)
			default:
				return nil, fmt.Errorf("line %d: bad attribute", t.line)
			}
			if p.is(tPunct, "(") {
				args, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				e = &callExpr{e, key, args}
				continue
			}
			e = &getAttrExpr{e, &constExpr{key}, true}
		case p.is(tPunct, "["):
			p.next()
			k, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tPunct, "]"); err != nil {
				return nil, err
			}
			e = &getAttrExpr{e, k, false}
		case p.is(tOp, "|"):
			p.next()
			t, err := p.expect(tName, "")
			if err != nil {
				return nil, err
			}
			f := &filterExpr{node: e, name: t.val}
			if p.is(tPunct, "(") {
				if f.args, err = p.parseArgs(); err != nil {
					return nil, err
				}
			}
			e = f
		default:
			return e, nil
		}
	}
}

// ---- templates --------------------------------------------------------------------------------

// Template is a parsed Twig template.
type Template struct {
	name   string
	body   []tNode
	macros map[string]*macroNode
}

// ParseTemplate parses Twig source.
func ParseTemplate(name, src string) (*Template, error) {
	toks, err := lexTwig(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	p := &tParser{toks: toks}
	body, _, err := p.parseBody()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	t := &Template{name: name, macros: map[string]*macroNode{}}
	for _, n := range body {
		if m, ok := n.(*macroNode); ok {
			t.macros[m.name] = m
			continue
		}
		t.body = append(t.body, n)
	}
	return t, nil
}

// TwigEnv holds named templates and the escaper callbacks.
type TwigEnv struct {
	templates map[string]*Template
	// Escapers maps an escape strategy to its function (full_url, html_tag, ...).
	Escapers map[string]func(string) string
}

// NewTwigEnv parses the given templates.
func NewTwigEnv(sources map[string]string) (*TwigEnv, error) {
	env := &TwigEnv{templates: map[string]*Template{}, Escapers: map[string]func(string) string{}}
	for name, src := range sources {
		t, err := ParseTemplate(name, src)
		if err != nil {
			return nil, err
		}
		env.templates[name] = t
	}
	return env, nil
}

// Has reports whether a template exists.
func (env *TwigEnv) Has(name string) bool { _, ok := env.templates[name]; return ok }

// Render executes a template with the given context.
func (env *TwigEnv) Render(name string, ctx *Map) (string, error) {
	t, ok := env.templates[name]
	if !ok {
		return "", fmt.Errorf("template %q not found", name)
	}
	r := &twigRun{env: env, tpl: t}
	var b strings.Builder
	scope := map[string]any{}
	for _, k := range ctx.Keys() {
		scope[k] = ctx.Get(k)
	}
	if err := r.exec(&b, t.body, scope); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return b.String(), nil
}

// macroSet is the value of an imported template alias.
type macroSet struct{ tpl *Template }

type twigRun struct {
	env *TwigEnv
	tpl *Template
}

func (r *twigRun) exec(b *strings.Builder, nodes []tNode, scope map[string]any) error {
	for _, n := range nodes {
		switch x := n.(type) {
		case *textNode:
			b.WriteString(x.text)
		case *printNode:
			v, err := r.eval(x.expr, scope)
			if err != nil {
				return err
			}
			if m, ok := v.(Markup); ok || isSafe(x.expr) {
				if ok {
					b.WriteString(string(m))
				} else {
					b.WriteString(twigString(v))
				}
				continue
			}
			b.WriteString(escapeHTML(twigString(v)))
		case *setNode:
			vals := make([]any, len(x.exprs))
			for i, e := range x.exprs {
				v, err := r.eval(e, scope)
				if err != nil {
					return err
				}
				vals[i] = v
			}
			for i, name := range x.names {
				if i < len(vals) {
					scope[name] = vals[i]
				}
			}
		case *ifNode:
			done := false
			for i, c := range x.conds {
				v, err := r.eval(c, scope)
				if err != nil {
					return err
				}
				if twigBool(v) {
					if err := r.exec(b, x.bodies[i], scope); err != nil {
						return err
					}
					done = true
					break
				}
			}
			if !done && x.els != nil {
				if err := r.exec(b, x.els, scope); err != nil {
					return err
				}
			}
		case *forNode:
			seq, err := r.eval(x.seq, scope)
			if err != nil {
				return err
			}
			var keys []any
			var vals []any
			switch s := seq.(type) {
			case []any:
				for i, v := range s {
					keys = append(keys, float64(i))
					vals = append(vals, v)
				}
			case *Map:
				for _, k := range s.Keys() {
					keys = append(keys, k)
					vals = append(vals, s.Get(k))
				}
			}
			if len(vals) == 0 {
				if x.els != nil {
					if err := r.exec(b, x.els, scope); err != nil {
						return err
					}
				}
				continue
			}
			// Twig restores the outer variables after the loop.
			saved := map[string]any{}
			for _, name := range []string{x.key, x.val, "loop"} {
				if v, ok := scope[name]; ok {
					saved[name] = v
				}
			}
			for i := range vals {
				if x.key != "" {
					scope[x.key] = keys[i]
				}
				scope[x.val] = vals[i]
				scope["loop"] = MapOf("index", float64(i+1), "index0", float64(i), "first", i == 0, "last", i == len(vals)-1, "length", float64(len(vals)))
				if err := r.exec(b, x.body, scope); err != nil {
					return err
				}
			}
			for _, name := range []string{x.key, x.val, "loop"} {
				if name == "" {
					continue
				}
				if v, ok := saved[name]; ok {
					scope[name] = v
				} else {
					delete(scope, name)
				}
			}
		case *importNode:
			if n, ok := x.tpl.(*nameExpr); ok && n.name == "_self" {
				scope[x.alias] = &macroSet{r.tpl}
				continue
			}
			v, err := r.eval(x.tpl, scope)
			if err != nil {
				return err
			}
			t, ok := r.env.templates[twigString(v)]
			if !ok {
				return fmt.Errorf("template %q not found", twigString(v))
			}
			scope[x.alias] = &macroSet{t}
		case *macroNode:
			r.tpl.macros[x.name] = x
		}
	}
	return nil
}

// isSafe mirrors Twig's static safety analysis for the html strategy.
func isSafe(e tExpr) bool {
	switch x := e.(type) {
	case *constExpr:
		return true
	case *condExpr:
		return isSafe(x.a) && isSafe(x.b)
	case *callExpr:
		return true // macro calls return Markup
	case *filterExpr:
		switch x.name {
		case "raw", "e", "escape":
			return true
		case "default":
			return isSafe(x.node) && (len(x.args) == 0 || isSafe(x.args[0]))
		}
	}
	return false
}

func (r *twigRun) eval(e tExpr, scope map[string]any) (any, error) {
	switch x := e.(type) {
	case *constExpr:
		return x.v, nil
	case *nameExpr:
		if x.name == "_self" {
			return &macroSet{r.tpl}, nil
		}
		return scope[x.name], nil
	case *arrayExpr:
		out := make([]any, 0, len(x.items))
		for _, it := range x.items {
			v, err := r.eval(it, scope)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case *hashExpr:
		o := NewMap()
		for i := range x.keys {
			k, err := r.eval(x.keys[i], scope)
			if err != nil {
				return nil, err
			}
			v, err := r.eval(x.vals[i], scope)
			if err != nil {
				return nil, err
			}
			o.Set(twigString(k), v)
		}
		return o, nil
	case *getAttrExpr:
		obj, err := r.eval(x.obj, scope)
		if err != nil {
			return nil, err
		}
		k, err := r.eval(x.key, scope)
		if err != nil {
			return nil, err
		}
		v, _ := attrOf(obj, k)
		return v, nil
	case *callExpr:
		obj, err := r.eval(x.obj, scope)
		if err != nil {
			return nil, err
		}
		ms, ok := obj.(*macroSet)
		if !ok {
			return nil, nil
		}
		m, ok := ms.tpl.macros[x.name]
		if !ok {
			return nil, fmt.Errorf("macro %q not found", x.name)
		}
		args := make([]any, len(x.args))
		for i, a := range x.args {
			if args[i], err = r.eval(a, scope); err != nil {
				return nil, err
			}
		}
		return r.callMacro(ms.tpl, m, args)
	case *funcExpr:
		args := make([]any, len(x.args))
		for i, a := range x.args {
			v, err := r.eval(a, scope)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		return twigFunc(x.name, args)
	case *filterExpr:
		if x.name == "default" {
			v, err := r.evalLenient(x.node, scope)
			if err != nil {
				return nil, err
			}
			if v == nil || twigEmpty(v) {
				if len(x.args) == 0 {
					return "", nil
				}
				return r.eval(x.args[0], scope)
			}
			return v, nil
		}
		v, err := r.eval(x.node, scope)
		if err != nil {
			return nil, err
		}
		args := make([]any, len(x.args))
		for i, a := range x.args {
			if args[i], err = r.eval(a, scope); err != nil {
				return nil, err
			}
		}
		return r.filter(x.name, v, args)
	case *testExpr:
		var res bool
		switch x.name {
		case "defined":
			res = r.defined(x.node, scope)
		default:
			v, err := r.eval(x.node, scope)
			if err != nil {
				return nil, err
			}
			args := make([]any, len(x.args))
			for i, a := range x.args {
				if args[i], err = r.eval(a, scope); err != nil {
					return nil, err
				}
			}
			switch x.name {
			case "empty":
				res = twigEmpty(v)
			case "null", "none":
				res = v == nil
			case "same as":
				res = len(args) > 0 && strictEq(v, args[0])
			case "iterable":
				res = isArray(v)
			case "odd":
				res = int(toFloat(v))%2 != 0
			case "even":
				res = int(toFloat(v))%2 == 0
			default:
				return nil, fmt.Errorf("unknown test %q", x.name)
			}
		}
		if x.not {
			res = !res
		}
		return res, nil
	case *unaryExpr:
		v, err := r.eval(x.node, scope)
		if err != nil {
			return nil, err
		}
		switch x.op {
		case "not":
			return !twigBool(v), nil
		case "-":
			return -toFloat(v), nil
		}
		return toFloat(v), nil
	case *binaryExpr:
		switch x.op {
		case "and":
			l, err := r.eval(x.left, scope)
			if err != nil || !twigBool(l) {
				return false, err
			}
			rv, err := r.eval(x.right, scope)
			return twigBool(rv), err
		case "or":
			l, err := r.eval(x.left, scope)
			if err != nil {
				return nil, err
			}
			if twigBool(l) {
				return true, nil
			}
			rv, err := r.eval(x.right, scope)
			return twigBool(rv), err
		case "??":
			l, err := r.evalLenient(x.left, scope)
			if err != nil {
				return nil, err
			}
			if l != nil {
				return l, nil
			}
			return r.eval(x.right, scope)
		}
		l, err := r.eval(x.left, scope)
		if err != nil {
			return nil, err
		}
		rv, err := r.eval(x.right, scope)
		if err != nil {
			return nil, err
		}
		return binaryOp(x.op, l, rv)
	case *condExpr:
		c, err := r.eval(x.cond, scope)
		if err != nil {
			return nil, err
		}
		if twigBool(c) {
			return r.eval(x.a, scope)
		}
		return r.eval(x.b, scope)
	}
	return nil, fmt.Errorf("unsupported expression %T", e)
}

// evalLenient evaluates without failing on undefined variables (non-strict mode never fails).
func (r *twigRun) evalLenient(e tExpr, scope map[string]any) (any, error) { return r.eval(e, scope) }

func (r *twigRun) defined(e tExpr, scope map[string]any) bool {
	switch x := e.(type) {
	case *nameExpr:
		_, ok := scope[x.name]
		return ok
	case *getAttrExpr:
		if !r.defined(x.obj, scope) {
			return false
		}
		obj, err := r.eval(x.obj, scope)
		if err != nil {
			return false
		}
		k, err := r.eval(x.key, scope)
		if err != nil {
			return false
		}
		_, ok := attrOf(obj, k)
		return ok
	case *constExpr:
		return true
	}
	v, err := r.eval(e, scope)
	return err == nil && v != nil
}

func (r *twigRun) callMacro(tpl *Template, m *macroNode, args []any) (any, error) {
	scope := map[string]any{}
	for i, name := range m.args {
		if i < len(args) {
			scope[name] = args[i]
		} else if m.defaults[i] != nil {
			v, err := r.eval(m.defaults[i], scope)
			if err != nil {
				return nil, err
			}
			scope[name] = v
		} else {
			scope[name] = nil
		}
	}
	sub := &twigRun{env: r.env, tpl: tpl}
	var b strings.Builder
	if err := sub.exec(&b, m.body, scope); err != nil {
		return nil, err
	}
	return Markup(b.String()), nil
}

// attrOf reads obj.key / obj[key] (and whether it exists).
func attrOf(obj any, k any) (any, bool) {
	switch o := obj.(type) {
	case *Map:
		return o.Lookup(twigString(k))
	case []any:
		i := int(toFloat(k))
		if isNumeric(k) && i >= 0 && i < len(o) {
			return o[i], true
		}
	}
	return nil, false
}

func binaryOp(op string, l, r any) (any, error) {
	switch op {
	case "==":
		return looseEq(l, r), nil
	case "!=":
		return !looseEq(l, r), nil
	case "<", ">", "<=", ">=":
		ln, lok := numberOf(l)
		rn, rok := numberOf(r)
		if !lok || !rok {
			ls, rs := twigString(l), twigString(r)
			switch op {
			case "<":
				return ls < rs, nil
			case ">":
				return ls > rs, nil
			case "<=":
				return ls <= rs, nil
			}
			return ls >= rs, nil
		}
		if l == nil {
			ln = 0
		}
		switch op {
		case "<":
			return ln < rn, nil
		case ">":
			return ln > rn, nil
		case "<=":
			return ln <= rn, nil
		}
		return ln >= rn, nil
	case "~":
		return twigString(l) + twigString(r), nil
	case "+":
		return toFloat(l) + toFloat(r), nil
	case "-":
		return toFloat(l) - toFloat(r), nil
	case "*":
		return toFloat(l) * toFloat(r), nil
	case "/":
		if toFloat(r) == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return toFloat(l) / toFloat(r), nil
	case "in", "not in":
		found := false
		switch h := r.(type) {
		case string:
			found = strings.Contains(h, twigString(l))
		default:
			for _, v := range listOf(r) {
				if looseEq(l, v) {
					found = true
					break
				}
			}
		}
		if op == "not in" {
			return !found, nil
		}
		return found, nil
	case "starts with":
		return strings.HasPrefix(twigString(l), twigString(r)), nil
	case "ends with":
		return strings.HasSuffix(twigString(l), twigString(r)), nil
	}
	return nil, fmt.Errorf("unsupported operator %q", op)
}

func twigFunc(name string, args []any) (any, error) {
	switch name {
	case "range":
		if len(args) >= 2 {
			var out []any
			for i := toFloat(args[0]); i <= toFloat(args[1]); i++ {
				out = append(out, i)
			}
			return out, nil
		}
	case "attribute":
		if len(args) >= 2 {
			v, _ := attrOf(args[0], args[1])
			return v, nil
		}
	}
	return nil, fmt.Errorf("unknown function %q", name)
}

func (r *twigRun) filter(name string, v any, args []any) (any, error) {
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	switch name {
	case "raw":
		if m, ok := v.(Markup); ok {
			return m, nil
		}
		return Markup(twigString(v)), nil
	case "e", "escape":
		strategy := "html"
		if s, ok := arg(0).(string); ok && s != "" {
			strategy = s
		}
		s := twigString(v)
		switch strategy {
		case "html":
			if m, ok := v.(Markup); ok {
				return m, nil
			}
			return Markup(escapeHTML(s)), nil
		case "html_attr":
			return Markup(escapeHTMLAttr(s)), nil
		case "js":
			return Markup(escapeHTMLAttr(s)), nil
		case "url":
			return Markup(urlEncode(s)), nil
		}
		if f := r.env.Escapers[strategy]; f != nil {
			return Markup(f(s)), nil
		}
		return nil, fmt.Errorf("unknown escape strategy %q", strategy)
	case "join":
		sep := twigString(arg(0))
		var parts []string
		for _, x := range listOf(v) {
			parts = append(parts, twigString(x))
		}
		return strings.Join(parts, sep), nil
	case "merge":
		return arrayMerge(v, arg(0)), nil
	case "json_encode":
		return phpJSONEncode(v), nil
	case "striptags":
		return stripTags(twigString(v), twigString(arg(0))), nil
	case "trim":
		chars := " \t\n\r\x00\x0B"
		if c, ok := arg(0).(string); ok {
			chars = c
		}
		return strings.Trim(twigString(v), chars), nil
	case "lower":
		return strings.ToLower(twigString(v)), nil
	case "upper":
		return strings.ToUpper(twigString(v)), nil
	case "length":
		switch x := v.(type) {
		case []any:
			return float64(len(x)), nil
		case *Map:
			return float64(x.Len()), nil
		}
		return float64(len([]rune(twigString(v)))), nil
	case "keys":
		var out []any
		if m, ok := v.(*Map); ok {
			for _, k := range m.Keys() {
				out = append(out, k)
			}
		} else {
			for i := range listOf(v) {
				out = append(out, float64(i))
			}
		}
		return out, nil
	case "first":
		if l := listOf(v); len(l) > 0 {
			return l[0], nil
		}
		return nil, nil
	case "last":
		if l := listOf(v); len(l) > 0 {
			return l[len(l)-1], nil
		}
		return nil, nil
	case "replace":
		s := twigString(v)
		if m := asMap(arg(0)); m != nil {
			for _, k := range m.Keys() {
				s = strings.ReplaceAll(s, k, twigString(m.Get(k)))
			}
		}
		return s, nil
	case "nl2br":
		return strings.ReplaceAll(twigString(v), "\n", "<br />\n"), nil
	case "abs":
		f := toFloat(v)
		if f < 0 {
			f = -f
		}
		return f, nil
	}
	return nil, fmt.Errorf("unknown filter %q", name)
}

// twigString converts a value for output (Twig: PHP string conversion, arrays print "Array").
func twigString(v any) string { return phpString(v) }

// twigBool mirrors Twig's boolean conversion (PHP truthiness).
func twigBool(v any) bool { return !phpEmpty(v) }

// twigEmpty mirrors the "empty" test: ”, null, false, [] and empty countables (0 is not empty).
func twigEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case Markup:
		return x == ""
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case *Map:
		return x.Len() == 0
	}
	return false
}

// arrayMerge mirrors PHP array_merge for two arrays.
func arrayMerge(a, b any) any {
	al, aList := a.([]any)
	bl, bList := b.([]any)
	if (aList || a == nil) && (bList || b == nil) {
		out := append([]any{}, al...)
		return append(out, bl...)
	}
	out := NewMap()
	n := 0
	add := func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				out.Set(strconv.Itoa(n), e)
				n++
			}
		case *Map:
			for _, k := range x.Keys() {
				if _, err := strconv.Atoi(k); err == nil {
					out.Set(strconv.Itoa(n), x.Get(k))
					n++
					continue
				}
				out.Set(k, x.Get(k))
			}
		}
	}
	add(a)
	add(b)
	return out
}
