package gotest

import (
	"fmt"
	"strings"
	"unicode"
)

// Action is the first-line execution recipe from a $GOROOT/test .go file
// (see src/cmd/internal/testdir).
type Action struct {
	Kind    string   // run, errorcheck, compile, skip, …
	Flags   []string // leftover -gcflags etc.
	Args    []string // program args after flags
	Raw     string   // full recipe line without //
	WantErr bool     // -1 / errorcheck
}

// UnsupportedActions are v1 auto-skipped kinds (counted, not skip.txt).
var UnsupportedActions = map[string]bool{
	"rundir":               true,
	"runindir":             true,
	"runoutput":            true,
	"errorcheck":           true,
	"errorcheckdir":        true,
	"errorcheckoutput":     true,
	"errorcheckandrundir":  true,
	"errorcheckwithauto":   true,
	"compile":              true,
	"compiledir":           true,
	"asmcheck":             true,
	"build":                true,
	"builddir":             true,
	"buildrundir":          true,
	"buildrun":             true,
}

// ParseAction extracts the execution recipe from source text.
func ParseAction(src string) (Action, error) {
	var recipe string
	for rest := src; recipe == "" && rest != ""; {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "//go:build") || strings.HasPrefix(trim, "// +build") {
			continue
		}
		if strings.HasPrefix(trim, "//") {
			recipe = strings.TrimSpace(strings.TrimPrefix(trim, "//"))
			break
		}
		if trim != "" {
			break
		}
	}
	if recipe == "" {
		return Action{}, fmt.Errorf("no execution recipe")
	}
	fields, err := splitQuoted(recipe)
	if err != nil {
		return Action{}, fmt.Errorf("invalid recipe %q: %w", recipe, err)
	}
	if len(fields) == 0 {
		return Action{}, fmt.Errorf("empty recipe")
	}
	a := Action{Kind: fields[0], Raw: recipe}
	args := fields[1:]
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-1":
			a.WantErr = true
		case "-0":
			a.WantErr = false
		case "-s", "-t", "-goexperiment", "-godebug", "-gomodversion":
			// consume value for flags that take an argument
			if args[0] != "-s" {
				if len(args) < 2 {
					return Action{}, fmt.Errorf("recipe flag %s needs a value", args[0])
				}
				args = args[1:]
			}
		default:
			a.Flags = append(a.Flags, args[0])
		}
		args = args[1:]
	}
	a.Args = args
	if strings.HasPrefix(a.Kind, "errorcheck") {
		a.WantErr = true
	}
	return a, nil
}

// splitQuoted splits on whitespace respecting " and ' quotes (testdir style).
func splitQuoted(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() > 0 || quote != 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	flush()
	return out, nil
}
