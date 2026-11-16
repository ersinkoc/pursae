package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Ctx is what a command's Run receives: the parsed command line, the streams and the output
// helpers.
type Ctx struct {
	Context context.Context
	Prog    string   // program name ("pursae" / "wpgo")
	Cmd     *Command // the command being run
	Args    []string // positional arguments (for Raw commands: every argument after the path)

	// Global parameters, already validated.
	Format string   // --format (Cmd.DefaultFormat or "table" when absent)
	Fields []string // --fields, split on commas; nil when absent
	Field  string   // --field
	Yes    bool     // --yes / -y
	Quiet  bool     // --quiet / -q

	flags map[string]string
	io    IO
	reg   *Registry
}

func (c *Ctx) initGlobals() error {
	c.Format = c.flags["format"]
	if c.Format == "" {
		c.Format = c.Cmd.DefaultFormat
	}
	if c.Format == "" {
		c.Format = "table"
	}
	if !slices.Contains(StandardFormats, c.Format) && !slices.Contains(c.Cmd.Formats, c.Format) {
		allowed := append(append([]string(nil), StandardFormats...), c.Cmd.Formats...)
		return fmt.Errorf("invalid --format %q (allowed: %s)", c.Format, strings.Join(allowed, ", "))
	}
	if v, ok := c.flags["fields"]; ok {
		c.Fields = SplitList(v)
		if len(c.Fields) == 0 {
			return fmt.Errorf("--fields is empty")
		}
	}
	c.Field = c.flags["field"]
	c.Yes = c.Bool("yes")
	c.Quiet = c.Bool("quiet")
	return nil
}

// --- arguments and flags -------------------------------------------------------------

// Arg returns positional argument i, or "" when absent.
func (c *Ctx) Arg(i int) string {
	if i < 0 || i >= len(c.Args) {
		return ""
	}
	return c.Args[i]
}

// Has reports whether flag name was given on the command line.
func (c *Ctx) Has(name string) bool {
	_, ok := c.flags[name]
	return ok
}

// Flag returns the flag's value, its declared Default when absent, or "".
func (c *Ctx) Flag(name string) string {
	if v, ok := c.flags[name]; ok {
		return v
	}
	if c.reg != nil && c.Cmd != nil {
		if f, ok := c.reg.flagSpec(c.Cmd, name); ok {
			return f.Default
		}
	}
	return ""
}

// Lookup returns the flag's value and whether it was given (no Default applied).
func (c *Ctx) Lookup(name string) (string, bool) {
	v, ok := c.flags[name]
	return v, ok
}

// Flags returns a copy of every flag given on the command line (including global ones).
func (c *Ctx) Flags() map[string]string {
	out := make(map[string]string, len(c.flags))
	for k, v := range c.flags {
		out[k] = v
	}
	return out
}

// Bool returns a boolean flag (absent: its Default, else false). Invalid values read as false;
// the parser already rejects them for declared Bool flags.
func (c *Ctx) Bool(name string) bool {
	b, _ := parseBool(c.Flag(name))
	return b
}

// Int returns an integer flag; def when absent or empty. An invalid value is a usage error.
func (c *Ctx) Int(name string, def int) (int, error) {
	v := c.Flag(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, Usagef("--%s expects a whole number, got %q", name, v)
	}
	return n, nil
}

// Int64 is Int for int64 values (IDs).
func (c *Ctx) Int64(name string, def int64) (int64, error) {
	v := c.Flag(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, Usagef("--%s expects a whole number, got %q", name, v)
	}
	return n, nil
}

// List returns a comma-separated flag split into trimmed, non-empty items (nil when absent).
func (c *Ctx) List(name string) []string { return SplitList(c.Flag(name)) }

// ArgInt64 parses positional argument i as an ID.
func (c *Ctx) ArgInt64(i int) (int64, error) { return ParseID(c.Arg(i)) }

// ArgIDs parses positional arguments from index i on as IDs.
func (c *Ctx) ArgIDs(from int) ([]int64, error) {
	var out []int64
	for _, a := range c.Args[min(from, len(c.Args)):] {
		id, err := ParseID(a)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// ParseID parses a positive integer ID; the error is a usage error.
func ParseID(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, Usagef("%q is not a valid ID", s)
	}
	return n, nil
}

// SplitList splits a comma-separated value into trimmed, non-empty items.
func SplitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ReadInput returns the contents of a positional "file" argument: "-" reads stdin, anything
// else is read through readFile (os.ReadFile in practice, injected to keep this package pure).
func (c *Ctx) ReadInput(arg string, readFile func(string) ([]byte, error)) ([]byte, error) {
	if arg == "-" {
		return io.ReadAll(c.io.In)
	}
	return readFile(arg)
}

// --- streams and messages ------------------------------------------------------------

// Stdin, Stdout and Stderr expose the streams.
func (c *Ctx) Stdin() io.Reader  { return c.io.In }
func (c *Ctx) Stdout() io.Writer { return c.io.Out }
func (c *Ctx) Stderr() io.Writer { return c.io.Err }

// Interactive reports whether stdin is a terminal.
func (c *Ctx) Interactive() bool { return c.io.Interactive }

// Println writes a line to stdout (always, even with --quiet: it is the command's output).
func (c *Ctx) Println(a ...any) { io.WriteString(c.io.Out, Sanitize(fmt.Sprintln(a...))) }

// Printf writes formatted output to stdout.
func (c *Ctx) Printf(format string, a ...any) {
	io.WriteString(c.io.Out, Sanitize(fmt.Sprintf(format, a...)))
}

// Log writes an informational line to stdout unless --quiet.
func (c *Ctx) Log(format string, a ...any) {
	if !c.Quiet {
		io.WriteString(c.io.Out, Sanitize(fmt.Sprintf(format+"\n", a...)))
	}
}

// Success writes "Success: ..." to stdout unless --quiet.
func (c *Ctx) Success(format string, a ...any) {
	if !c.Quiet {
		io.WriteString(c.io.Out, Sanitize(fmt.Sprintf("Success: "+format+"\n", a...)))
	}
}

// Warning writes "Warning: ..." to stderr unless --quiet.
func (c *Ctx) Warning(format string, a ...any) {
	if !c.Quiet {
		io.WriteString(c.io.Err, Sanitize(fmt.Sprintf("Warning: "+format+"\n", a...)))
	}
}

// Confirm asks before a destructive operation. With --yes it returns nil at once. In a
// terminal it prompts "<question> [y/n] " and returns ErrAborted unless the answer is y/yes.
// Not in a terminal (scripts, pipes) it refuses with an error telling to pass --yes.
func (c *Ctx) Confirm(format string, a ...any) error {
	if c.Yes {
		return nil
	}
	q := Sanitize(fmt.Sprintf(format, a...))
	if !c.io.Interactive {
		return &ExitError{Code: ExitFailure, Msg: q + " Refusing to continue without confirmation: pass --yes."}
	}
	fmt.Fprintf(c.io.Err, "%s [y/n] ", q)
	line, _ := bufio.NewReader(c.io.In).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return ErrAborted
}

// --- output --------------------------------------------------------------------------

// Row is one record for Print / PrintItem.
type Row = map[string]any

// View describes how a list is printed.
type View struct {
	// Fields are the default columns, in order, used when --fields is absent. Empty: every key
	// of the rows, sorted.
	Fields []string
	// ID is the field printed by --format=ids (default: "ID", else the first field).
	ID string
}

// Print writes rows in the requested --format, honouring --fields and --field.
func (c *Ctx) Print(rows []Row, v View) error {
	if err := checkFields(rows, v.Fields, c.Field, c.Fields); err != nil {
		return err
	}
	fields := c.Fields
	if len(fields) == 0 {
		fields = v.Fields
	}
	if len(fields) == 0 {
		fields = allKeys(rows)
	}
	if c.Field != "" {
		for _, r := range rows {
			fmt.Fprintln(c.io.Out, Sanitize(Stringify(lookupField(r, c.Field))))
		}
		return nil
	}
	switch c.Format {
	case "ids":
		id := v.ID
		if id == "" {
			id = "ID"
			if len(rows) > 0 {
				if _, ok := lookupFieldOK(rows[0], "ID"); !ok && len(fields) > 0 {
					id = fields[0]
				}
			}
		}
		parts := make([]string, 0, len(rows))
		for _, r := range rows {
			parts = append(parts, Sanitize(Stringify(lookupField(r, id))))
		}
		fmt.Fprintln(c.io.Out, strings.Join(parts, " "))
		return nil
	case "count":
		fmt.Fprintln(c.io.Out, len(rows))
		return nil
	}
	return Write(c.io.Out, c.Format, rows, fields)
}

// PrintItem writes one record (a "get" command). Table and CSV print Field/Value pairs; JSON
// and YAML print an object; --field prints the raw value.
func (c *Ctx) PrintItem(item Row, fields []string) error {
	if err := checkFields([]Row{item}, fields, "", c.Fields); err != nil {
		return err
	}
	if len(c.Fields) > 0 {
		fields = c.Fields
	}
	if len(fields) == 0 {
		fields = allKeys([]Row{item})
	}
	if c.Field != "" {
		v, ok := lookupFieldOK(item, c.Field)
		if !ok {
			return Errorf("invalid field: %s", c.Field)
		}
		fmt.Fprintln(c.io.Out, Sanitize(Stringify(v)))
		return nil
	}
	switch c.Format {
	case "json", "yaml":
		return Write(c.io.Out, c.Format, []Row{item}, fields, singleObject())
	case "ids", "count":
		return Usagef("--format=%s is for lists", c.Format)
	}
	pairs := make([]Row, 0, len(fields))
	for _, f := range fields {
		pairs = append(pairs, Row{"Field": f, "Value": lookupField(item, f)})
	}
	return Write(c.io.Out, c.Format, pairs, []string{"Field", "Value"})
}

// PrintValue writes a single value: JSON for --format=json, YAML for yaml, otherwise the plain
// string (maps and slices as JSON).
func (c *Ctx) PrintValue(v any) error {
	switch c.Format {
	case "json":
		b, err := marshalJSON(v, true)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.io.Out, string(b))
		return nil
	case "yaml":
		writeYAMLValue(c.io.Out, v, 0)
		return nil
	}
	fmt.Fprintln(c.io.Out, Sanitize(Stringify(v)))
	return nil
}

// checkFields rejects a --field or --fields entry that names no field: not a key of any row
// (case-insensitive) nor one of the view's declared fields. With no rows there is nothing to
// check against beyond the declared fields, so unknown names pass (an empty list stays empty).
func checkFields(rows []Row, declared []string, field string, fields []string) error {
	if len(rows) == 0 {
		return nil
	}
	known := func(f string) bool {
		for _, d := range declared {
			if strings.EqualFold(d, f) {
				return true
			}
		}
		for _, r := range rows {
			if _, ok := lookupFieldOK(r, f); ok {
				return true
			}
		}
		return false
	}
	if field != "" && !known(field) {
		return Errorf("invalid field: %s", field)
	}
	for _, f := range fields {
		if !known(f) {
			return Errorf("invalid field: %s", f)
		}
	}
	return nil
}
