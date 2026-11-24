package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// TimeLayout is how time.Time values are shown in table and CSV output (JSON and YAML use
// RFC 3339).
const TimeLayout = "2006-01-02 15:04:05"

type writeOpts struct{ single bool }

type writeOpt func(*writeOpts)

// singleObject makes JSON/YAML print rows[0] as one object instead of a list.
func singleObject() writeOpt { return func(o *writeOpts) { o.single = true } }

// Write renders rows with the given columns in format "table", "json", "csv" or "yaml". Columns
// keep the order of fields; a field missing from a row prints as empty (null in JSON/YAML).
func Write(w io.Writer, format string, rows []Row, fields []string, opts ...writeOpt) error {
	var o writeOpts
	for _, f := range opts {
		f(&o)
	}
	switch format {
	case "table", "":
		return writeTable(w, rows, fields)
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write(fields); err != nil {
			return err
		}
		for _, r := range rows {
			rec := make([]string, len(fields))
			for i, f := range fields {
				rec[i] = csvCell(Stringify(lookupField(r, f)))
			}
			if err := cw.Write(rec); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	case "json":
		var buf bytes.Buffer
		if o.single {
			if len(rows) == 0 {
				buf.WriteString("{}")
			} else if err := writeJSONObject(&buf, rows[0], fields); err != nil {
				return err
			}
		} else {
			buf.WriteByte('[')
			for i, r := range rows {
				if i > 0 {
					buf.WriteByte(',')
				}
				if err := writeJSONObject(&buf, r, fields); err != nil {
					return err
				}
			}
			buf.WriteByte(']')
		}
		buf.WriteByte('\n')
		_, err := w.Write(buf.Bytes())
		return err
	case "yaml":
		if o.single && (len(rows) == 0 || len(fields) == 0) {
			_, err := io.WriteString(w, "{}\n")
			return err
		}
		if len(rows) == 0 {
			_, err := io.WriteString(w, "[]\n")
			return err
		}
		out := outputWriter{w: w}
		if o.single {
			writeYAMLRow(&out, rows[0], fields, 0, false)
		} else {
			for _, r := range rows {
				writeYAMLRow(&out, r, fields, 0, true)
			}
		}
		return out.err
	}
	return fmt.Errorf("unsupported format %q", format)
}

// --- terminal safety ---------------------------------------------------------------

// Sanitize makes s safe to write to a terminal: C0 control characters other than TAB and LF
// (CR only as part of CRLF), DEL and the C1 controls U+0080–U+009F are replaced by a visible
// escape (\x1b, \u009b), so text stored by visitors (comment authors, user agents …) cannot
// send escape sequences (OSC 52 clipboard writes, title changes, cursor movement) to the
// operator's terminal. Human-readable output (table, CSV, plain values, --field, messages)
// goes through it; JSON and YAML escape control characters themselves and are left exact.
func Sanitize(s string) string {
	clean := true
	for _, r := range s {
		if unsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i, r := range s {
		switch {
		case r == '\r' && i+1 < len(s) && s[i+1] == '\n':
			b.WriteRune(r)
		case r < 0x80 && unsafeRune(r):
			fmt.Fprintf(&b, "\\x%02x", r)
		case unsafeRune(r):
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unsafeRune reports a control character Sanitize escapes (CR is decided by its context).
func unsafeRune(r rune) bool {
	if r == '\t' || r == '\n' {
		return false
	}
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// csvCell sanitizes a CSV cell and neutralises spreadsheet formulas: a cell starting with =, +,
// -, @ (or TAB/CR) that is not a plain number gets a leading single quote, as OWASP recommends,
// so opening the export in a spreadsheet does not evaluate visitor-supplied text.
func csvCell(s string) string {
	s = Sanitize(s)
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		if !isNumber(s) {
			return "'" + s
		}
	}
	return s
}

// --- values --------------------------------------------------------------------------

// Stringify renders a value for table/CSV/plain output: nil is empty, scalars print plainly,
// times use TimeLayout, string slices are comma-joined and anything else is compact JSON.
func Stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x)
	case float32:
		return formatFloat(float64(x))
	case float64:
		return formatFloat(x)
	case time.Time:
		if x.IsZero() {
			return ""
		}
		return x.Format(TimeLayout)
	case *time.Time:
		if x == nil || x.IsZero() {
			return ""
		}
		return x.Format(TimeLayout)
	case []string:
		return strings.Join(x, ",")
	case json.Number:
		return x.String()
	case error:
		return x.Error()
	case fmt.Stringer:
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return ""
		}
		return x.String()
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return ""
		}
		return Stringify(rv.Elem().Interface())
	}
	b, err := marshalJSON(v, false)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func formatFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// marshalJSON encodes without HTML escaping.
func marshalJSON(v any, _ bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func writeJSONObject(buf *bytes.Buffer, r Row, fields []string) error {
	buf.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := marshalJSON(f, false)
		buf.Write(k)
		buf.WriteByte(':')
		v, err := marshalJSON(lookupField(r, f), false)
		if err != nil {
			return fmt.Errorf("field %s: %w", f, err)
		}
		buf.Write(v)
	}
	buf.WriteByte('}')
	return nil
}

// lookupField reads a field: exact key first, then a case-insensitive match.
func lookupField(r Row, f string) any {
	v, _ := lookupFieldOK(r, f)
	return v
}

func lookupFieldOK(r Row, f string) (any, bool) {
	if v, ok := r[f]; ok {
		return v, true
	}
	for k, v := range r {
		if strings.EqualFold(k, f) {
			return v, true
		}
	}
	return nil, false
}

func allKeys(rows []Row) []string {
	seen := map[string]bool{}
	var keys []string
	for _, r := range rows {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// --- table ---------------------------------------------------------------------------

// writeTable draws a WP-CLI style ASCII table. Nothing is printed for zero rows. Newlines and
// tabs inside cells are shown as spaces so the grid stays intact.
func writeTable(w io.Writer, rows []Row, fields []string) error {
	if len(rows) == 0 || len(fields) == 0 {
		return nil
	}
	cells := make([][]string, len(rows))
	width := make([]int, len(fields))
	for i, f := range fields {
		width[i] = utf8.RuneCountInString(f)
	}
	for ri, r := range rows {
		cells[ri] = make([]string, len(fields))
		for i, f := range fields {
			s := Sanitize(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(Stringify(lookupField(r, f))))
			cells[ri][i] = s
			if n := utf8.RuneCountInString(s); n > width[i] {
				width[i] = n
			}
		}
	}
	var b strings.Builder
	sep := func() {
		b.WriteByte('+')
		for _, n := range width {
			b.WriteString(strings.Repeat("-", n+2))
			b.WriteByte('+')
		}
		b.WriteByte('\n')
	}
	line := func(vals []string) {
		b.WriteByte('|')
		for i, v := range vals {
			b.WriteByte(' ')
			b.WriteString(v)
			b.WriteString(strings.Repeat(" ", width[i]-utf8.RuneCountInString(v)+1))
			b.WriteByte('|')
		}
		b.WriteByte('\n')
	}
	sep()
	line(fields)
	sep()
	for _, r := range cells {
		line(r)
	}
	sep()
	_, err := io.WriteString(w, b.String())
	return err
}

// outputWriter remembers the first error while the YAML helpers write nested values.
type outputWriter struct {
	w   io.Writer
	err error
}

func (out *outputWriter) Write(p []byte) (int, error) {
	if out.err != nil {
		return 0, out.err
	}
	n, err := out.w.Write(p)
	out.err = err
	return n, err
}

// --- YAML ----------------------------------------------------------------------------

func writeYAMLRow(w io.Writer, r Row, fields []string, indent int, listItem bool) {
	for i, f := range fields {
		prefix := strings.Repeat("  ", indent)
		if listItem {
			if i == 0 {
				prefix += "- "
			} else {
				prefix += "  "
			}
		}
		writeYAMLKey(w, prefix, f, lookupField(r, f), indent+boolInt(listItem))
	}
	if len(fields) == 0 && listItem {
		fmt.Fprintln(w, strings.Repeat("  ", indent)+"- {}")
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// writeYAMLKey writes "key: value" (scalars inline, collections as nested blocks).
func writeYAMLKey(w io.Writer, prefix, key string, v any, indent int) {
	v = normalize(v)
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			fmt.Fprintf(w, "%s%s: {}\n", prefix, yamlString(key))
			return
		}
		fmt.Fprintf(w, "%s%s:\n", prefix, yamlString(key))
		writeYAMLValue(w, x, indent+1)
	case []any:
		if len(x) == 0 {
			fmt.Fprintf(w, "%s%s: []\n", prefix, yamlString(key))
			return
		}
		fmt.Fprintf(w, "%s%s:\n", prefix, yamlString(key))
		writeYAMLValue(w, x, indent+1)
	default:
		fmt.Fprintf(w, "%s%s: %s\n", prefix, yamlString(key), yamlScalar(x))
	}
}

// writeYAMLValue writes v as a block at the given indent (two spaces per level).
func writeYAMLValue(w io.Writer, v any, indent int) {
	v = normalize(v)
	pad := strings.Repeat("  ", indent)
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			fmt.Fprintln(w, pad+"{}")
			return
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeYAMLKey(w, pad, k, x[k], indent)
		}
	case []any:
		if len(x) == 0 {
			fmt.Fprintln(w, pad+"[]")
			return
		}
		for _, e := range x {
			e = normalize(e)
			switch ex := e.(type) {
			case map[string]any:
				if len(ex) == 0 {
					fmt.Fprintln(w, pad+"- {}")
					continue
				}
				keys := make([]string, 0, len(ex))
				for k := range ex {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for i, k := range keys {
					p := pad + "  "
					if i == 0 {
						p = pad + "- "
					}
					writeYAMLKey(w, p, k, ex[k], indent+1)
				}
			case []any:
				b, _ := marshalJSON(ex, false) // nested list: JSON flow style is valid YAML
				fmt.Fprintln(w, pad+"- "+string(b))
			default:
				fmt.Fprintln(w, pad+"- "+yamlScalar(ex))
			}
		}
	default:
		fmt.Fprintln(w, pad+yamlScalar(x))
	}
}

// normalize turns structs, typed maps and typed slices into map[string]any / []any through a
// JSON round trip, so YAML sees the same shape JSON would.
func normalize(v any) any {
	switch v.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
		float32, float64, time.Time, map[string]any, []any, json.Number:
		return v
	}
	b, err := marshalJSON(v, false)
	if err != nil {
		return fmt.Sprint(v)
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return string(b)
	}
	return out
}

func yamlScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return yamlString(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case time.Time:
		return yamlString(x.Format(time.RFC3339))
	case json.Number:
		return x.String()
	case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return Stringify(x)
	}
	return yamlString(Stringify(v))
}

// yamlString quotes s (JSON style, which YAML accepts) unless it is a safe plain scalar.
func yamlString(s string) string {
	if yamlPlainOK(s) {
		return s
	}
	b, _ := marshalJSON(s, false)
	return string(b)
}

func yamlPlainOK(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	switch strings.ToLower(s) {
	case "null", "~", "true", "false", "yes", "no", "on", "off", "y", "n", ".inf", "+.inf", "-.inf", ".nan":
		return false
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return false
	}
	if _, err := strconv.ParseUint(strings.TrimPrefix(s, "+"), 0, 64); err == nil {
		return false
	}
	if strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@`") {
		return false
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
