// Package blocks parses and renders serialized block content, the format the
// block editor saves: HTML with <!-- wp:name {"attrs"} --> comment delimiters.
//
// The parser is a port of @wordpress/block-serialization-default-parser (and its
// PHP twin WP_Block_Parser) so a Go parse produces the same tree the editor does.
// Offsets are byte offsets; the tree is identical because delimiters are ASCII.
package blocks

import (
	"encoding/json"
	"strings"
)

// Block is one node of the parsed tree. BlockName is nil for freeform HTML
// between blocks. InnerContent interleaves HTML chunks with nil placeholders,
// one per inner block, in document order.
type Block struct {
	BlockName    *string        `json:"blockName"`
	Attrs        map[string]any `json:"attrs"`
	InnerBlocks  []*Block       `json:"innerBlocks"`
	InnerHTML    string         `json:"innerHTML"`
	InnerContent []*string      `json:"innerContent"`
}

// Name returns the block name or "" for freeform content.
func (b *Block) Name() string {
	if b.BlockName == nil {
		return ""
	}
	return *b.BlockName
}

func newBlock(name string, attrs map[string]any) *Block {
	if attrs == nil {
		attrs = map[string]any{}
	}
	n := name
	return &Block{BlockName: &n, Attrs: attrs, InnerBlocks: []*Block{}, InnerContent: []*string{}}
}

func freeform(html string) *Block {
	h := html
	return &Block{Attrs: map[string]any{}, InnerBlocks: []*Block{}, InnerHTML: html, InnerContent: []*string{&h}}
}

func (b *Block) appendHTML(html string) {
	b.InnerHTML += html
	h := html
	b.InnerContent = append(b.InnerContent, &h)
}

// HasBlocks reports whether content contains at least one block delimiter (has_blocks()).
func HasBlocks(content string) bool {
	return strings.Contains(content, "<!-- wp:")
}

type frame struct {
	block            *Block
	tokenStart       int
	tokenLength      int
	prevOffset       int
	leadingHTMLStart int // -1 when there is none
}

type tokenType int

const (
	noMoreTokens tokenType = iota
	voidBlock
	blockOpener
	blockCloser
)

type parser struct {
	doc    string
	offset int
	output []*Block
	stack  []*frame
	scan   int // where the tokenizer resumes
}

// Parse returns the top-level blocks of a document.
func Parse(doc string) []*Block {
	p := &parser{doc: doc}
	for p.proceed() {
	}
	if p.output == nil {
		return []*Block{}
	}
	return p.output
}

func (p *parser) proceed() bool {
	depth := len(p.stack)
	typ, name, attrs, start, length := p.nextToken()
	leading := -1
	if start > p.offset {
		leading = p.offset
	}

	switch typ {
	case noMoreTokens:
		if depth == 0 {
			p.addFreeform(0)
			return false
		}
		if depth == 1 {
			p.addBlockFromStack(-1)
			return false
		}
		for len(p.stack) > 0 {
			p.addBlockFromStack(-1)
		}
		return false

	case voidBlock:
		if depth == 0 {
			if leading != -1 {
				p.output = append(p.output, freeform(p.doc[leading:start]))
			}
			p.output = append(p.output, newBlock(name, attrs))
			p.offset = start + length
			return true
		}
		p.addInnerBlock(newBlock(name, attrs), start, length, -1)
		p.offset = start + length
		return true

	case blockOpener:
		p.stack = append(p.stack, &frame{
			block:            newBlock(name, attrs),
			tokenStart:       start,
			tokenLength:      length,
			prevOffset:       start + length,
			leadingHTMLStart: leading,
		})
		p.offset = start + length
		return true

	case blockCloser:
		if depth == 0 {
			// Closer without opener: treat the rest as freeform, like the reference parser.
			p.addFreeform(0)
			return false
		}
		if depth == 1 {
			p.addBlockFromStack(start)
			p.offset = start + length
			return true
		}
		top := p.stack[len(p.stack)-1]
		p.stack = p.stack[:len(p.stack)-1]
		top.block.appendHTML(p.doc[top.prevOffset:start])
		top.prevOffset = start + length
		p.addInnerBlock(top.block, top.tokenStart, top.tokenLength, start+length)
		p.offset = start + length
		return true
	}
	p.addFreeform(0)
	return false
}

func (p *parser) addFreeform(length int) {
	if length == 0 {
		length = len(p.doc) - p.offset
	}
	if length <= 0 {
		return
	}
	p.output = append(p.output, freeform(p.doc[p.offset:p.offset+length]))
}

func (p *parser) addInnerBlock(b *Block, tokenStart, tokenLength, lastOffset int) {
	parent := p.stack[len(p.stack)-1]
	parent.block.InnerBlocks = append(parent.block.InnerBlocks, b)
	if html := p.doc[parent.prevOffset:tokenStart]; html != "" {
		parent.block.appendHTML(html)
	}
	parent.block.InnerContent = append(parent.block.InnerContent, nil)
	if lastOffset >= 0 {
		parent.prevOffset = lastOffset
	} else {
		parent.prevOffset = tokenStart + tokenLength
	}
}

func (p *parser) addBlockFromStack(endOffset int) {
	f := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	var html string
	if endOffset >= 0 {
		html = p.doc[f.prevOffset:endOffset]
	} else {
		html = p.doc[f.prevOffset:]
	}
	if html != "" {
		f.block.appendHTML(html)
	}
	if f.leadingHTMLStart != -1 {
		p.output = append(p.output, freeform(p.doc[f.leadingHTMLStart:f.tokenStart]))
	}
	p.output = append(p.output, f.block)
}

// nextToken finds the next block delimiter at or after p.scan. It implements the
// reference tokenizer regex without lookaheads:
//
//	<!--\s+(\/)?wp:([a-z][a-z0-9_-]*\/)?([a-z][a-z0-9_-]*)\s+({…}\s+)?(\/)?-->
func (p *parser) nextToken() (tokenType, string, map[string]any, int, int) {
	doc := p.doc
	for {
		i := strings.Index(doc[p.scan:], "<!--")
		if i < 0 {
			p.scan = len(doc)
			return noMoreTokens, "", nil, 0, 0
		}
		start := p.scan + i
		typ, name, attrs, end, ok := matchDelimiter(doc, start)
		if !ok {
			p.scan = start + 4
			continue
		}
		p.scan = end
		return typ, name, attrs, start, end - start
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isNameStart(c byte) bool { return c >= 'a' && c <= 'z' }
func isNameChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

func readName(doc string, i int) (string, int) {
	if i >= len(doc) || !isNameStart(doc[i]) {
		return "", i
	}
	j := i + 1
	for j < len(doc) && isNameChar(doc[j]) {
		j++
	}
	return doc[i:j], j
}

func matchDelimiter(doc string, start int) (tokenType, string, map[string]any, int, bool) {
	i := start + 4
	ws := i
	for i < len(doc) && isSpace(doc[i]) {
		i++
	}
	if i == ws {
		return 0, "", nil, 0, false
	}
	closer := false
	if i < len(doc) && doc[i] == '/' {
		closer = true
		i++
	}
	if !strings.HasPrefix(doc[i:], "wp:") {
		return 0, "", nil, 0, false
	}
	i += 3

	first, j := readName(doc, i)
	if first == "" {
		return 0, "", nil, 0, false
	}
	namespace, name := "core/", first
	if j < len(doc) && doc[j] == '/' {
		second, k := readName(doc, j+1)
		if second == "" {
			return 0, "", nil, 0, false
		}
		namespace, name, j = first+"/", second, k
	}
	i = j

	ws = i
	for i < len(doc) && isSpace(doc[i]) {
		i++
	}
	if i == ws {
		return 0, "", nil, 0, false
	}

	var attrs map[string]any
	hasAttrs := false
	if i < len(doc) && doc[i] == '{' {
		// Lazy match up to the first "}" + whitespace + optional "/" + "-->".
		search := i
		for {
			k := strings.Index(doc[search:], "-->")
			if k < 0 {
				break
			}
			end := search + k
			e := end
			if e > i && doc[e-1] == '/' {
				e--
			}
			w := e
			for w > i && isSpace(doc[w-1]) {
				w--
			}
			if w < e && w-1 >= i && doc[w-1] == '}' {
				hasAttrs = true
				var parsed map[string]any
				if json.Unmarshal([]byte(doc[i:w]), &parsed) == nil {
					attrs = parsed
				}
				isVoid := e < end
				return finishToken(closer, isVoid, hasAttrs, namespace+name, attrs, end+3)
			}
			search = end + 3
		}
		// No closing "}" sequence: the attrs group is optional, fall through and try "-->" directly.
	}

	isVoid := false
	if i < len(doc) && doc[i] == '/' {
		isVoid = true
		i++
	}
	if !strings.HasPrefix(doc[i:], "-->") {
		return 0, "", nil, 0, false
	}
	return finishToken(closer, isVoid, false, namespace+name, nil, i+3)
}

func finishToken(closer, isVoid, hasAttrs bool, name string, attrs map[string]any, end int) (tokenType, string, map[string]any, int, bool) {
	if attrs == nil {
		attrs = map[string]any{}
	}
	switch {
	case isVoid:
		return voidBlock, name, attrs, end, true
	case closer:
		return blockCloser, name, nil, end, true
	default:
		return blockOpener, name, attrs, end, true
	}
}
