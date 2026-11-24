package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type parsed struct {
	args  []string
	flags map[string]string
}

// flagSpec finds a declared command or global flag.
func (r *Registry) flagSpec(cmd *Command, name string) (Flag, bool) {
	for _, f := range cmd.Flags {
		if f.Name == name {
			return f, true
		}
	}
	for _, f := range r.globals {
		if f.Name == name {
			return f, true
		}
	}
	return Flag{}, false
}

// parse splits rest into positional arguments and flags and validates them against cmd.
//
// Accepted forms: --name=value, --name value (non-bool flags), --name (bool flags, or any
// undeclared flag on an AnyFlags command), --no-name (bool flags), -y, -q, -h, and "--" which
// ends flag parsing. A single "-" is a positional argument (stdin by convention). A flag given
// twice keeps the last value.
func (r *Registry) parse(cmd *Command, rest []string) (*parsed, error) {
	p := &parsed{flags: map[string]string{}}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			p.args = append(p.args, rest[i+1:]...)
			i = len(rest)
			continue
		case a == "-h":
			return nil, errHelp
		case a == "-y":
			p.flags["yes"] = "true"
			continue
		case a == "-q":
			p.flags["quiet"] = "true"
			continue
		case !strings.HasPrefix(a, "--") || a == "-":
			if strings.HasPrefix(a, "-") && a != "-" && !isNumber(a) {
				return nil, fmt.Errorf("unknown flag %s (flags start with --)", a)
			}
			p.args = append(p.args, a)
			continue
		}
		body := a[2:]
		name, val, hasVal := strings.Cut(body, "=")
		if name == "" {
			return nil, fmt.Errorf("invalid flag %s", a)
		}
		spec, ok := r.flagSpec(cmd, name)
		if !ok && strings.HasPrefix(name, "no-") && !hasVal {
			if s, ok2 := r.flagSpec(cmd, strings.TrimPrefix(name, "no-")); ok2 && s.Bool {
				p.flags[s.Name] = "false"
				continue
			}
		}
		if !ok {
			if cmd.AnyFlags {
				if !hasVal {
					val = "true"
				}
				p.flags[name] = val
				continue
			}
			return nil, fmt.Errorf("unknown flag --%s.%s", name, r.flagSuggestion(cmd, name))
		}
		if name == "help" {
			return nil, errHelp
		}
		if spec.Bool {
			if !hasVal {
				val = "true"
			} else if _, err := parseBool(val); err != nil {
				return nil, fmt.Errorf("--%s expects true or false, got %q", name, val)
			}
			p.flags[name] = val
			continue
		}
		if !hasVal {
			if i+1 >= len(rest) || rest[i+1] == "--" {
				return nil, fmt.Errorf("--%s needs a value", name)
			}
			i++
			val = rest[i]
		}
		p.flags[name] = val
	}

	// Required flags and allowed values.
	for _, f := range cmd.Flags {
		v, set := p.flags[f.Name]
		if f.Required && !set {
			return nil, fmt.Errorf("missing --%s", f.Name)
		}
		if set && len(f.Values) > 0 && !slices.Contains(f.Values, v) {
			return nil, fmt.Errorf("invalid value %q for --%s (allowed: %s)", v, f.Name, strings.Join(f.Values, ", "))
		}
	}

	// Positional arguments.
	min, max := 0, 0
	for _, a := range cmd.Args {
		if !a.Optional {
			min++
		}
		if a.Repeat {
			max = -1
		} else if max >= 0 {
			max++
		}
	}
	if len(p.args) < min {
		missing := cmd.Args[len(p.args)]
		return nil, fmt.Errorf("missing argument <%s>", missing.Name)
	}
	if max >= 0 && len(p.args) > max {
		return nil, fmt.Errorf("too many arguments (unexpected %q)", p.args[max])
	}
	return p, nil
}

// isNumber lets negative numbers through as positional arguments (e.g. "-1").
func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// parseBool accepts true/false, 1/0, yes/no, on/off.
func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "y":
		return true, nil
	case "0", "false", "no", "off", "n", "":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean: %q", s)
}

// ParseBool parses a boolean the way flags do: true/false, 1/0, yes/no, on/off, y/n.
func ParseBool(s string) (bool, error) { return parseBool(s) }
