// Package i18n is WordPress Go's gettext layer: it reads the .mo and JSON files of the official
// WordPress language packs (core and plugins), keeps one merged catalog per locale and
// translates admin pages after they are rendered.
package i18n

import (
	"encoding/binary"
	"errors"
	"strings"
)

// ctxSep separates a message context from its msgid (gettext's EOT, like WordPress).
const ctxSep = "\x04"

// entry is one translated message: the singular and plural forms in order.
type entry []string

// moFile is a parsed .mo file.
type moFile struct {
	entries map[string]entry  // key: [context + "\x04"] + msgid
	plurals map[string]string // plural msgid -> key of its entry
	plural  *pluralRule
}

// parseMO reads a GNU gettext .mo file (both byte orders).
func parseMO(b []byte) (*moFile, error) {
	if len(b) < 28 {
		return nil, errors.New("mo: file too short")
	}
	var bo binary.ByteOrder
	switch {
	case binary.LittleEndian.Uint32(b) == 0x950412de:
		bo = binary.LittleEndian
	case binary.BigEndian.Uint32(b) == 0x950412de:
		bo = binary.BigEndian
	default:
		return nil, errors.New("mo: bad magic number")
	}
	n := int(bo.Uint32(b[8:]))
	origOff, transOff := int(bo.Uint32(b[12:])), int(bo.Uint32(b[16:]))
	str := func(table, i int) (string, bool) {
		p := table + i*8
		if p+8 > len(b) {
			return "", false
		}
		l, o := int(bo.Uint32(b[p:])), int(bo.Uint32(b[p+4:]))
		if o < 0 || l < 0 || o+l > len(b) {
			return "", false
		}
		return string(b[o : o+l]), true
	}
	// n comes from the file: a count no table of this size can hold must not size the map (a
	// 28-byte file claiming 2^31 entries would allocate gigabytes before the first read fails).
	hint := n
	if max := (len(b) - 28) / 16; hint > max || hint < 0 {
		hint = max
	}
	f := &moFile{entries: make(map[string]entry, hint), plurals: map[string]string{}}
	for i := 0; i < n; i++ {
		orig, ok1 := str(origOff, i)
		trans, ok2 := str(transOff, i)
		if !ok1 || !ok2 {
			return nil, errors.New("mo: truncated string table")
		}
		if orig == "" {
			f.plural = parseHeaderPlural(trans)
			continue
		}
		// Plural entries store "singular\x00plural"; the key is the singular.
		key := orig
		if i := strings.IndexByte(orig, 0); i >= 0 {
			key = orig[:i]
			f.plurals[orig[i+1:]] = key
		}
		forms := strings.Split(trans, "\x00")
		if len(forms) == 0 || forms[0] == "" {
			continue
		}
		f.entries[key] = forms
	}
	return f, nil
}

// parseHeaderPlural reads the Plural-Forms line of a catalog header.
func parseHeaderPlural(header string) *pluralRule {
	for _, line := range strings.Split(header, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "Plural-Forms") {
			continue
		}
		for _, part := range strings.Split(v, ";") {
			pk, pv, ok := strings.Cut(part, "=")
			if ok && strings.TrimSpace(pk) == "plural" {
				if r, err := compilePlural(strings.TrimSpace(pv)); err == nil {
					return r
				}
			}
		}
	}
	return nil
}
