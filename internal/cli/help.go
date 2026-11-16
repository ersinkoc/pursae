package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// help prints the help for topic (a command path, possibly empty) and returns the exit code.
func (r *Registry) help(std IO, topic []string) int {
	n := r.root
	var path []string
	for i, t := range topic {
		if strings.HasPrefix(t, "-") {
			continue
		}
		if t == "help" && n == r.root && n.child("help") == nil {
			if rest := topic[i+1:]; len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
				return r.help(std, rest) // `help help post` reads as `help post`
			}
			r.writeHelpHelp(std.Out)
			return ExitOK
		}
		c := n.child(t)
		if c == nil {
			fmt.Fprintf(std.Err, "Error: no help for '%s'.%s\n", strings.Join(append(path, t), " "), r.suggestion(n, t, path))
			return ExitUsage
		}
		n = c
		path = append(path, t)
	}
	r.writeHelp(std.Out, n, path)
	return ExitOK
}

// writeHelpHelp is the help of the help command itself (`<prog> help help`).
func (r *Registry) writeHelpHelp(w io.Writer) {
	fmt.Fprintf(w, `NAME

  %[1]s help

DESCRIPTION

  Show help for a command or a command group. Without arguments, list every command.

SYNOPSIS

  %[1]s help [<command>...]

  Equivalent forms: %[1]s <command> --help, %[1]s <command> -h.

EXAMPLES

  %[1]s help
  %[1]s help post
  %[1]s help post meta update
`, r.Prog)
}

// Help writes the help for path ("" for the top level) to w. Unknown paths print the top level.
func (r *Registry) Help(w io.Writer, path string) {
	n := r.root
	var p []string
	for _, part := range splitPath(path) {
		c := n.child(part)
		if c == nil {
			break
		}
		n = c
		p = append(p, part)
	}
	r.writeHelp(w, n, p)
}

func (r *Registry) writeHelp(w io.Writer, n *node, path []string) {
	var b strings.Builder
	if n == r.root {
		fmt.Fprintf(&b, "usage: %s <command> [<args>] [--<flag>[=<value>]]\n", r.Prog)
		if r.Description != "" {
			fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(r.Description))
		}
		b.WriteString("\nCOMMANDS\n\n")
		writeIndex(&b, n, "")
		r.writeGlobals(&b)
		fmt.Fprintf(&b, "\nRun '%s help <command>' for more information on a specific command.\n", r.Prog)
		io.WriteString(w, b.String())
		return
	}
	full := strings.Join(path, " ")
	if n.cmd != nil {
		r.writeCommandHelp(&b, n.cmd)
	}
	if len(n.children) > 0 {
		if n.cmd != nil {
			b.WriteString("\n")
		} else {
			fmt.Fprintf(&b, "NAME\n\n  %s %s\n", r.Prog, full)
			if n.short != "" {
				fmt.Fprintf(&b, "\nDESCRIPTION\n\n  %s\n", n.short)
			}
			fmt.Fprintf(&b, "\nSYNOPSIS\n\n  %s %s <command>\n", r.Prog, full)
		}
		b.WriteString("\nSUBCOMMANDS\n\n")
		writeIndex(&b, n, "")
		fmt.Fprintf(&b, "\nRun '%s help %s <command>' for more information.\n", r.Prog, full)
	}
	io.WriteString(w, b.String())
}

// writeIndex lists the visible children of n with their short descriptions.
func writeIndex(b *strings.Builder, n *node, _ string) {
	kids := n.sortedChildren()
	width := 0
	for _, c := range kids {
		if c.visible() && len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range kids {
		if !c.visible() {
			continue
		}
		desc := c.short
		if desc == "" && c.cmd != nil {
			desc = c.cmd.Short
		}
		fmt.Fprintf(b, "  %-*s  %s\n", width, c.name, desc)
	}
}

func (r *Registry) writeCommandHelp(b *strings.Builder, cmd *Command) {
	fmt.Fprintf(b, "NAME\n\n  %s %s\n", r.Prog, cmd.Path)
	if cmd.Short != "" || cmd.Long != "" {
		b.WriteString("\nDESCRIPTION\n\n")
		if cmd.Short != "" {
			fmt.Fprintf(b, "  %s\n", cmd.Short)
		}
		if cmd.Long != "" {
			if cmd.Short != "" {
				b.WriteString("\n")
			}
			for _, line := range strings.Split(strings.TrimSpace(cmd.Long), "\n") {
				if line = strings.TrimRight(line, " "); line == "" {
					b.WriteString("\n") // blank paragraph separator: no trailing spaces
					continue
				}
				fmt.Fprintf(b, "  %s\n", line)
			}
		}
	}
	fmt.Fprintf(b, "\nSYNOPSIS\n\n  %s %s\n", r.Prog, r.synopsis(cmd))
	if len(cmd.Args) > 0 || hasVisibleFlags(cmd.Flags) {
		b.WriteString("\nOPTIONS\n")
		for _, a := range cmd.Args {
			fmt.Fprintf(b, "\n  %s\n", argSyntax(a))
			if a.Usage != "" {
				fmt.Fprintf(b, "      %s\n", a.Usage)
			}
		}
		for _, f := range cmd.Flags {
			if f.Hidden {
				continue
			}
			fmt.Fprintf(b, "\n  %s\n", flagSyntax(f, true))
			if f.Usage != "" {
				fmt.Fprintf(b, "      %s\n", f.Usage)
			}
			if len(f.Values) > 0 {
				fmt.Fprintf(b, "      Options: %s\n", strings.Join(f.Values, ", "))
			}
			if f.Default != "" {
				fmt.Fprintf(b, "      Default: %s\n", f.Default)
			}
		}
	}
	if cmd.AnyFlags {
		b.WriteString("\n  [--<field>=<value>]\n      Other fields are accepted as --<field>=<value>.\n")
	}
	if len(cmd.Examples) > 0 {
		b.WriteString("\nEXAMPLES\n\n")
		for _, e := range cmd.Examples {
			e = strings.TrimSpace(e)
			if !strings.HasPrefix(e, "#") && !strings.HasPrefix(e, "$") && !strings.HasPrefix(e, r.Prog+" ") {
				e = r.Prog + " " + e
			}
			fmt.Fprintf(b, "  %s\n", e)
		}
	}
	if !cmd.Raw && !cmd.External {
		r.writeGlobals(b)
	}
}

func (r *Registry) writeGlobals(b *strings.Builder) {
	b.WriteString("\nGLOBAL PARAMETERS\n\n")
	width := 0
	for _, g := range r.globals {
		if s := flagSyntax(g, false); len(s) > width {
			width = len(s)
		}
	}
	for _, g := range r.globals {
		fmt.Fprintf(b, "  %-*s  %s\n", width, flagSyntax(g, false), g.Usage)
	}
}

func hasVisibleFlags(fs []Flag) bool {
	for _, f := range fs {
		if !f.Hidden {
			return true
		}
	}
	return false
}

func argSyntax(a Arg) string {
	s := "<" + a.Name + ">"
	if a.Repeat {
		s += "..."
	}
	if a.Optional {
		s = "[" + s + "]"
	}
	return s
}

func flagSyntax(f Flag, brackets bool) string {
	s := "--" + f.Name
	if !f.Bool {
		v := f.Name
		if len(f.Values) > 0 && len(f.Values) <= 4 {
			v = strings.Join(f.Values, "|")
		}
		s += "=<" + v + ">"
	}
	if brackets && !f.Required {
		s = "[" + s + "]"
	}
	return s
}

// synopsis is "path <arg>... [--flag=<v>]" for usage lines.
func (r *Registry) synopsis(cmd *Command) string {
	if cmd.Synopsis != "" {
		return cmd.Path + " " + cmd.Synopsis
	}
	parts := []string{cmd.Path}
	if cmd.Raw {
		parts = append(parts, "[<args>...]")
	}
	for _, a := range cmd.Args {
		parts = append(parts, argSyntax(a))
	}
	for _, f := range cmd.Flags {
		if !f.Hidden {
			parts = append(parts, flagSyntax(f, true))
		}
	}
	if cmd.AnyFlags {
		parts = append(parts, "[--<field>=<value>]")
	}
	return strings.Join(parts, " ")
}

// --- suggestions ---------------------------------------------------------------------

// suggestion returns " Did you mean 'x'?" for the closest child name of n, or "".
func (r *Registry) suggestion(n *node, word string, path []string) string {
	var names []string
	for _, c := range n.children {
		if c.visible() {
			names = append(names, c.name)
		}
	}
	best := Suggest(word, names)
	if best == "" {
		return ""
	}
	full := strings.Join(append(append([]string(nil), path...), best), " ")
	return fmt.Sprintf(" Did you mean '%s'?", full)
}

func (r *Registry) flagSuggestion(cmd *Command, name string) string {
	var names []string
	for _, f := range cmd.Flags {
		names = append(names, f.Name)
	}
	for _, g := range r.globals {
		names = append(names, g.Name)
	}
	if best := Suggest(name, names); best != "" {
		return fmt.Sprintf(" Did you mean --%s?", best)
	}
	return ""
}

// Suggest returns the candidate closest to word: a unique prefix match, else the smallest edit
// distance within max(2, len(word)/3). It returns "" when nothing is close.
func Suggest(word string, candidates []string) string {
	if word == "" || len(candidates) == 0 {
		return ""
	}
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	var prefixed []string
	for _, c := range sorted {
		if strings.HasPrefix(c, word) {
			prefixed = append(prefixed, c)
		}
	}
	if len(prefixed) == 1 {
		return prefixed[0]
	}
	limit := max(2, len(word)/3)
	best, bestD := "", limit+1
	for _, c := range sorted {
		if d := levenshtein(strings.ToLower(word), strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}
