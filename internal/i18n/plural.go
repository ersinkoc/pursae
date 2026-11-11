package i18n

import (
	"errors"
	"strconv"
)

// pluralRule is a compiled gettext Plural-Forms expression (a C expression over n).
type pluralRule struct{ eval func(n int64) int64 }

func (r *pluralRule) index(n int64) int {
	if r == nil {
		if n == 1 {
			return 0
		}
		return 1
	}
	return int(r.eval(n))
}

// compilePlural parses the C subset gettext allows: n, integers, ! * / % + - < <= > >= == !=
// && || ?: and parentheses.
func compilePlural(src string) (*pluralRule, error) {
	// Real rules are under 200 bytes; the parser recurses per parenthesis and operator, so a
	// header of thousands of "(" would otherwise exhaust the stack (a fatal error, not a panic).
	if len(src) > maxPluralExpr {
		return nil, errors.New("plural: expression too long")
	}
	p := &plParser{src: src}
	f, err := p.ternary()
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.pos != len(p.src) {
		return nil, errors.New("plural: trailing input")
	}
	return &pluralRule{eval: f}, nil
}

// maxPluralExpr bounds a Plural-Forms expression.
const maxPluralExpr = 1024

type plFn func(n int64) int64

type plParser struct {
	src string
	pos int
}

func (p *plParser) skip() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *plParser) eat(tok string) bool {
	p.skip()
	if len(p.src)-p.pos >= len(tok) && p.src[p.pos:p.pos+len(tok)] == tok {
		p.pos += len(tok)
		return true
	}
	return false
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (p *plParser) ternary() (plFn, error) {
	c, err := p.or()
	if err != nil {
		return nil, err
	}
	if !p.eat("?") {
		return c, nil
	}
	a, err := p.ternary()
	if err != nil {
		return nil, err
	}
	if !p.eat(":") {
		return nil, errors.New("plural: missing ':'")
	}
	b, err := p.ternary()
	if err != nil {
		return nil, err
	}
	return func(n int64) int64 {
		if c(n) != 0 {
			return a(n)
		}
		return b(n)
	}, nil
}

func (p *plParser) or() (plFn, error) {
	l, err := p.and()
	for err == nil && p.eat("||") {
		var r plFn
		if r, err = p.and(); err == nil {
			ll := l
			l = func(n int64) int64 { return b2i(ll(n) != 0 || r(n) != 0) }
		}
	}
	return l, err
}

func (p *plParser) and() (plFn, error) {
	l, err := p.cmp()
	for err == nil && p.eat("&&") {
		var r plFn
		if r, err = p.cmp(); err == nil {
			ll := l
			l = func(n int64) int64 { return b2i(ll(n) != 0 && r(n) != 0) }
		}
	}
	return l, err
}

func (p *plParser) cmp() (plFn, error) {
	l, err := p.relational()
	for err == nil {
		var op string
		if p.eat("==") {
			op = "=="
		} else if p.eat("!=") {
			op = "!="
		} else {
			return l, nil
		}
		var r plFn
		if r, err = p.relational(); err != nil {
			break
		}
		ll := l
		if op == "==" {
			l = func(n int64) int64 { return b2i(ll(n) == r(n)) }
		} else {
			l = func(n int64) int64 { return b2i(ll(n) != r(n)) }
		}
	}
	return l, err
}

func (p *plParser) relational() (plFn, error) {
	l, err := p.add()
	for err == nil {
		var op string
		for _, o := range []string{"<=", ">=", "<", ">"} {
			if p.eat(o) {
				op = o
				break
			}
		}
		if op == "" {
			return l, nil
		}
		var r plFn
		if r, err = p.add(); err != nil {
			break
		}
		ll := l
		switch op {
		case "<=":
			l = func(n int64) int64 { return b2i(ll(n) <= r(n)) }
		case ">=":
			l = func(n int64) int64 { return b2i(ll(n) >= r(n)) }
		case "<":
			l = func(n int64) int64 { return b2i(ll(n) < r(n)) }
		case ">":
			l = func(n int64) int64 { return b2i(ll(n) > r(n)) }
		}
	}
	return l, err
}

func (p *plParser) add() (plFn, error) {
	l, err := p.mul()
	for err == nil {
		var op byte
		switch {
		case p.eat("+"):
			op = '+'
		case p.eat("-"):
			op = '-'
		default:
			return l, nil
		}
		var r plFn
		if r, err = p.mul(); err != nil {
			break
		}
		ll := l
		if op == '+' {
			l = func(n int64) int64 { return ll(n) + r(n) }
		} else {
			l = func(n int64) int64 { return ll(n) - r(n) }
		}
	}
	return l, err
}

func (p *plParser) mul() (plFn, error) {
	l, err := p.unary()
	for err == nil {
		var op byte
		switch {
		case p.eat("*"):
			op = '*'
		case p.eat("/"):
			op = '/'
		case p.eat("%"):
			op = '%'
		default:
			return l, nil
		}
		var r plFn
		if r, err = p.unary(); err != nil {
			break
		}
		ll := l
		switch op {
		case '*':
			l = func(n int64) int64 { return ll(n) * r(n) }
		case '/':
			l = func(n int64) int64 {
				if d := r(n); d != 0 {
					return ll(n) / d
				}
				return 0
			}
		case '%':
			l = func(n int64) int64 {
				if d := r(n); d != 0 {
					return ll(n) % d
				}
				return 0
			}
		}
	}
	return l, err
}

func (p *plParser) unary() (plFn, error) {
	if p.eat("+") {
		return p.unary()
	}
	if p.eat("-") {
		f, err := p.unary()
		if err != nil {
			return nil, err
		}
		return func(n int64) int64 { return -f(n) }, nil
	}
	if p.eat("!") {
		f, err := p.unary()
		if err != nil {
			return nil, err
		}
		return func(n int64) int64 { return b2i(f(n) == 0) }, nil
	}
	if p.eat("(") {
		f, err := p.ternary()
		if err != nil {
			return nil, err
		}
		if !p.eat(")") {
			return nil, errors.New("plural: missing ')'")
		}
		return f, nil
	}
	if p.eat("n") {
		return func(n int64) int64 { return n }, nil
	}
	p.skip()
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	if start == p.pos {
		return nil, errors.New("plural: unexpected input")
	}
	v, err := strconv.ParseInt(p.src[start:p.pos], 10, 64)
	if err != nil {
		return nil, err
	}
	return func(int64) int64 { return v }, nil
}
