// Package cli is a small, dependency-free framework for WP-CLI style command lines:
//
//	pursae <noun> [<noun>...] <verb> [<args>...] [--flag=value] [--flag value] [--bool-flag]
//
// Commands are registered on a [Registry] under a space-separated path ("post list",
// "post meta get", "import"). The registry parses arguments and flags, validates them against
// the command's declared [Arg] and [Flag] lists, handles the global parameters (--format,
// --fields, --field, --yes, --quiet, --help), renders help (top level, per noun, per command)
// with "did you mean" suggestions, and formats list output as table, JSON, CSV, YAML, ids or
// count through [Ctx.Print] and [Ctx.PrintItem].
//
// The package knows nothing about the application: commands reach their dependencies through
// closures (the server package passes its lazily connected environment to each register
// function).
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Exit codes returned by [Registry.Run].
const (
	ExitOK      = 0 // success
	ExitFailure = 1 // the command failed (or, for checks such as "exists", answered no)
	ExitUsage   = 2 // bad command line: unknown command or flag, missing argument, invalid value
)

// Standard output formats accepted by every command through --format.
var StandardFormats = []string{"table", "json", "csv", "yaml", "ids", "count"}

// Arg declares a positional argument.
type Arg struct {
	Name     string // shown as <name>
	Usage    string
	Optional bool // shown as [<name>]
	Repeat   bool // last argument only: takes one (or, when Optional, zero) or more values; shown as <name>...
}

// Flag declares a command flag (without the leading "--").
type Flag struct {
	Name     string
	Usage    string
	Bool     bool     // a switch: --name, --name=true|false, --no-name; never consumes the next argument
	Default  string   // shown in help and returned by Ctx.Flag when the flag is absent
	Required bool     // the command refuses to run without it
	Values   []string // allowed values (empty: anything)
	Hidden   bool     // accepted but not listed in help
}

// Command is one runnable command (or, with Run nil and External true, a help-only entry for a
// command handled outside the registry, e.g. `serve`).
type Command struct {
	// Path is the space-separated command path, e.g. "post meta get".
	Path     string
	Short    string   // one line, shown in lists
	Long     string   // paragraphs shown in the command's help
	Args     []Arg    // positional arguments, in order
	Flags    []Flag   // command flags
	Examples []string // full example command lines (without the program name prefix is fine: "post list --format=ids")
	// Formats lists extra --format values beyond StandardFormats (e.g. "plaintext", "var").
	Formats []string
	// DefaultFormat is the --format used when none is given (default "table").
	DefaultFormat string
	// AnyFlags accepts flags that are not declared: "--x=v" sets x to v, a bare "--x" sets it
	// to "true" (it never consumes the next argument). For pass-through commands like
	// `post create --anything=...`.
	AnyFlags bool
	// Raw passes every argument after the path to Run untouched in Ctx.Args: no flag parsing,
	// no global flags, no argument validation (only --help/-h as the first argument shows help).
	Raw bool
	// Synopsis replaces the generated synopsis after the path (for Raw and External commands,
	// whose arguments the registry does not parse), e.g. "-pub <key.pub> <package.zip>".
	Synopsis string
	// External marks a help-only entry for a command main handles itself.
	External bool
	Hidden   bool // not listed in help indexes (still runnable and documented)
	Run      func(c *Ctx) error
}

// IO carries the streams a command uses.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	// Interactive reports that In is a terminal: Confirm prompts instead of refusing.
	Interactive bool
}

// node is one level of the command tree.
type node struct {
	name     string
	short    string // noun description
	cmd      *Command
	children map[string]*node
}

func (n *node) child(name string) *node {
	if n.children == nil {
		return nil
	}
	return n.children[name]
}

func (n *node) sortedChildren() []*node {
	out := make([]*node, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// visible reports whether the node shows up in a help index.
func (n *node) visible() bool {
	if n.cmd != nil && !n.cmd.Hidden {
		return true
	}
	for _, c := range n.children {
		if c.visible() {
			return true
		}
	}
	return false
}

// Registry is the command tree.
type Registry struct {
	// Prog is the program name used in usage and help ("pursae" or "wpgo").
	Prog string
	// Description is printed under the usage line of the top-level help.
	Description string
	root        *node
	globals     []Flag
}

// New returns an empty registry for program prog with the standard global flags.
func New(prog string) *Registry {
	return &Registry{
		Prog: prog,
		root: &node{},
		globals: []Flag{
			{Name: "format", Usage: "Output format: table, json, csv, yaml, ids or count (some commands accept more)."},
			{Name: "fields", Usage: "Comma-separated list of fields to show."},
			{Name: "field", Usage: "Print the value of a single field."},
			{Name: "yes", Bool: true, Usage: "Answer yes to confirmation prompts (required for destructive commands when not run in a terminal). Short: -y."},
			{Name: "quiet", Bool: true, Usage: "Suppress informational messages. Short: -q."},
			{Name: "help", Bool: true, Usage: "Show help for the command. Short: -h."},
		},
	}
}

// AddGlobalFlag declares an extra flag every command accepts (e.g. --user).
func (r *Registry) AddGlobalFlag(f Flag) {
	for _, g := range r.globals {
		if g.Name == f.Name {
			panic("cli: duplicate global flag --" + f.Name)
		}
	}
	r.globals = append(r.globals, f)
}

// globalFlag finds a declared global flag.
func (r *Registry) globalFlag(name string) (Flag, bool) {
	for _, g := range r.globals {
		if g.Name == name {
			return g, true
		}
	}
	return Flag{}, false
}

// Noun describes a command group (e.g. Noun("post meta", "Manage post custom fields.")). Nouns
// are created implicitly by Add; Noun only sets the description shown in help.
func (r *Registry) Noun(path, short string) {
	n := r.ensure(splitPath(path))
	n.short = short
}

// Add registers cmd. It panics on an empty or duplicate path, or an invalid declaration, so
// mistakes surface in tests and at start-up.
func (r *Registry) Add(cmd Command) {
	parts := splitPath(cmd.Path)
	if len(parts) == 0 {
		panic("cli: command with an empty path")
	}
	if cmd.Run == nil && !cmd.External {
		panic("cli: command " + cmd.Path + " has no Run")
	}
	for i, a := range cmd.Args {
		if a.Repeat && i != len(cmd.Args)-1 {
			panic("cli: " + cmd.Path + ": only the last argument may repeat")
		}
		if !a.Optional && i > 0 && cmd.Args[i-1].Optional {
			panic("cli: " + cmd.Path + ": required argument after an optional one")
		}
	}
	seen := map[string]bool{}
	for _, f := range cmd.Flags {
		if seen[f.Name] || f.Name == "" {
			panic("cli: " + cmd.Path + ": duplicate or empty flag --" + f.Name)
		}
		seen[f.Name] = true
		for _, g := range r.globals {
			if g.Name == f.Name {
				panic("cli: " + cmd.Path + ": flag --" + f.Name + " shadows a global flag")
			}
		}
	}
	n := r.ensure(parts)
	if n.cmd != nil {
		panic("cli: duplicate command " + cmd.Path)
	}
	c := cmd
	c.Path = strings.Join(parts, " ")
	n.cmd = &c
}

// Lookup returns the command registered at path, or nil.
func (r *Registry) Lookup(path string) *Command {
	n := r.root
	for _, p := range splitPath(path) {
		if n = n.child(p); n == nil {
			return nil
		}
	}
	return n.cmd
}

// Commands returns every registered command, sorted by path.
func (r *Registry) Commands() []*Command {
	var out []*Command
	var walk func(n *node)
	walk = func(n *node) {
		if n.cmd != nil {
			out = append(out, n.cmd)
		}
		for _, c := range n.sortedChildren() {
			walk(c)
		}
	}
	walk(r.root)
	return out
}

func (r *Registry) ensure(parts []string) *node {
	n := r.root
	for _, p := range parts {
		c := n.child(p)
		if c == nil {
			if n.children == nil {
				n.children = map[string]*node{}
			}
			c = &node{name: p}
			n.children[p] = c
		}
		n = c
	}
	return n
}

func splitPath(p string) []string { return strings.Fields(p) }

// Run parses args (without the program name) and runs the matching command. It returns the
// process exit code; errors are printed to io.Err as "Error: ...".
func (r *Registry) Run(ctx context.Context, args []string, std IO) int {
	if std.Out == nil {
		std.Out = io.Discard
	}
	if std.Err == nil {
		std.Err = io.Discard
	}
	if std.In == nil {
		std.In = strings.NewReader("")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Leading global flags (`pursae --format=json post list`) are moved behind the path.
	var lead []string
	for len(args) > 0 && strings.HasPrefix(args[0], "-") && args[0] != "-" && args[0] != "--" {
		if isHelpFlag(args[0]) {
			break
		}
		lead = append(lead, args[0])
		args = args[1:]
		// `--format json`: a value-taking global flag written with a space takes the next
		// argument with it, so the value is not read as the command name.
		if name, ok := strings.CutPrefix(lead[len(lead)-1], "--"); ok && !strings.Contains(name, "=") && len(args) > 0 {
			if g, ok := r.globalFlag(name); ok && !g.Bool {
				lead = append(lead, args[0])
				args = args[1:]
			}
		}
	}

	if len(args) == 0 || isHelpFlag(args[0]) || args[0] == "help" {
		var topic []string
		if len(args) > 0 {
			topic = args[1:]
		}
		return r.help(std, topic)
	}

	n := r.root
	var path []string
	i := 0
	for i < len(args) {
		c := n.child(args[i])
		if c == nil {
			break
		}
		n = c
		path = append(path, args[i])
		i++
	}
	rest := append(append([]string(nil), args[i:]...), lead...)

	if n == r.root {
		fmt.Fprintf(std.Err, "Error: '%s' is not a %s command.%s\n", args[0], r.Prog, r.suggestion(r.root, args[0], nil))
		fmt.Fprintf(std.Err, "See '%s help'.\n", r.Prog)
		return ExitUsage
	}
	if n.cmd == nil || n.cmd.External && n.cmd.Run == nil {
		if n.cmd != nil { // help-only entry reached through the registry
			if len(rest) > 0 && isHelpFlag(rest[0]) {
				return r.help(std, path)
			}
			fmt.Fprintf(std.Err, "Error: '%s %s' is handled by the %s binary itself; run it directly.\n", r.Prog, n.cmd.Path, r.Prog)
			return ExitUsage
		}
		// A noun without a command of its own.
		if len(rest) == 0 || isHelpFlag(rest[0]) {
			return r.help(std, path)
		}
		if !strings.HasPrefix(rest[0], "-") {
			fmt.Fprintf(std.Err, "Error: '%s' is not a registered subcommand of '%s'.%s\n", rest[0], strings.Join(path, " "), r.suggestion(n, rest[0], path))
			fmt.Fprintf(std.Err, "See '%s help %s'.\n", r.Prog, strings.Join(path, " "))
			return ExitUsage
		}
		return r.help(std, path)
	}

	cmd := n.cmd
	if cmd.Raw {
		if len(rest) > 0 && isHelpFlag(rest[0]) {
			return r.help(std, path)
		}
		c := &Ctx{Context: ctx, Prog: r.Prog, Cmd: cmd, Args: rest, flags: map[string]string{}, io: std, Format: "table", reg: r}
		return r.exec(c)
	}

	parsed, err := r.parse(cmd, rest)
	if err != nil {
		if errors.Is(err, errHelp) {
			return r.help(std, path)
		}
		r.printUsageError(std, cmd, err)
		return ExitUsage
	}
	c := &Ctx{Context: ctx, Prog: r.Prog, Cmd: cmd, Args: parsed.args, flags: parsed.flags, io: std, reg: r}
	if err := c.initGlobals(); err != nil {
		r.printUsageError(std, cmd, err)
		return ExitUsage
	}
	return r.exec(c)
}

func (r *Registry) exec(c *Ctx) int {
	err := c.Cmd.Run(c)
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		if ee.Msg != "" {
			fmt.Fprintln(c.io.Err, "Error: "+Sanitize(ee.Msg))
		}
		return ee.Code
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		r.printUsageError(c.io, c.Cmd, err)
		return ExitUsage
	}
	fmt.Fprintln(c.io.Err, "Error: "+Sanitize(err.Error()))
	return ExitFailure
}

func (r *Registry) printUsageError(std IO, cmd *Command, err error) {
	fmt.Fprintln(std.Err, "Error: "+err.Error())
	fmt.Fprintf(std.Err, "usage: %s %s\n", r.Prog, r.synopsis(cmd))
	fmt.Fprintf(std.Err, "See '%s help %s'.\n", r.Prog, cmd.Path)
}

func isHelpFlag(s string) bool { return s == "--help" || s == "-h" }

// ExitError ends a command with a specific exit code. An empty Msg prints nothing (e.g.
// `post exists` answering no with code 1).
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Msg
}

// Exit returns an *ExitError with the given code and no message.
func Exit(code int) error { return &ExitError{Code: code} }

// UsageError reports a bad command line found by the command itself (exit code 2, with the
// command's synopsis printed).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usagef returns a *UsageError.
func Usagef(format string, a ...any) error { return &UsageError{Msg: fmt.Sprintf(format, a...)} }

// Errorf returns a plain error (exit code 1, printed as "Error: ...").
func Errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

// ErrAborted is returned by Confirm when the user answers no.
var ErrAborted = &ExitError{Code: ExitFailure, Msg: "Aborted."}

var errHelp = errors.New("help requested")
