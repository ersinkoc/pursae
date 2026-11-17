package webbuilder

import (
	"strings"

	"golang.org/x/net/html"
)

// AddLocalClasses gives every element of src whose data-wbc id is a key of classes that class
// (appended to its class attribute, or a new class attribute). Only those opening tags change;
// every other byte of src stays as written.
func AddLocalClasses(src string, classes map[string]string) string {
	if len(classes) == 0 || !strings.Contains(src, AttrCodeID+"=") {
		return src
	}
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	pos, last := 0, 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		start := pos
		pos += len(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		_, more := z.TagName()
		id, found := "", false
		for more {
			var k, v []byte
			k, v, more = z.TagAttr()
			if string(k) == AttrCodeID {
				id, found = string(v), true
				break
			}
		}
		cls, ok := classes[id]
		if !found || !ok || pos > len(src) {
			continue
		}
		b.WriteString(src[last:start])
		b.WriteString(tagWithClass(src[start:pos], cls))
		last = pos
	}
	if last == 0 {
		return src
	}
	b.WriteString(src[last:])
	return b.String()
}

// tagWithClass adds cls to the class attribute of an opening tag's source (the attribute keeps its
// quoting; an unquoted value gets double quotes), or adds class="cls" after the tag name.
func tagWithClass(tag, cls string) string {
	for _, a := range scanAttrs(tag) {
		if !strings.EqualFold(a.name, "class") {
			continue
		}
		if a.valStart < 0 { // class with no value
			return tag[:a.nameEnd] + `="` + cls + `"` + tag[a.nameEnd:]
		}
		val := tag[a.valStart:a.valEnd]
		next := strings.TrimRight(val, " \t\n\r\f")
		if strings.TrimSpace(next) != "" {
			next += " "
		}
		next += cls
		if a.quote == 0 {
			return tag[:a.valStart] + `"` + next + `"` + tag[a.valEnd:]
		}
		return tag[:a.valStart] + next + tag[a.valEnd:]
	}
	i := 1
	for i < len(tag) && !isCSSSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
		i++
	}
	return tag[:i] + ` class="` + cls + `"` + tag[i:]
}

type rawAttr struct {
	name             string
	nameEnd          int
	valStart, valEnd int // -1: no value
	quote            byte
}

// scanAttrs lists the attributes of an opening tag's source the way the HTML tokenizer reads them.
func scanAttrs(tag string) []rawAttr {
	var out []rawAttr
	n := len(tag)
	i := 1
	for i < n && !isCSSSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
		i++
	}
	for i < n {
		for i < n && (isCSSSpace(tag[i]) || tag[i] == '/') {
			i++
		}
		if i >= n || tag[i] == '>' {
			break
		}
		ns := i
		i++ // a name may start with "="
		for i < n && !isCSSSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' && tag[i] != '=' {
			i++
		}
		a := rawAttr{name: tag[ns:i], nameEnd: i, valStart: -1, valEnd: -1}
		j := i
		for j < n && isCSSSpace(tag[j]) {
			j++
		}
		if j < n && tag[j] == '=' {
			j++
			for j < n && isCSSSpace(tag[j]) {
				j++
			}
			if j < n && (tag[j] == '"' || tag[j] == '\'') {
				q := tag[j]
				k := strings.IndexByte(tag[j+1:], q)
				if k < 0 {
					k = n - j - 1
				}
				a.quote, a.valStart, a.valEnd = q, j+1, j+1+k
				i = a.valEnd + 1
			} else {
				vs := j
				for j < n && !isCSSSpace(tag[j]) && tag[j] != '>' {
					j++
				}
				a.valStart, a.valEnd = vs, j
				i = j
			}
		}
		out = append(out, a)
	}
	return out
}
