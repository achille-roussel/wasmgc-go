package gotest

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	skipFileName     = "skip.txt"
	skipBumpFileName = "skip_bump.txt"
	emDash           = "—"
)

// SkipEntry is one skip.txt line.
type SkipEntry struct {
	Name   string
	Reason string
	Raw    string
}

// ParseSkipFile reads skip.txt. Lines without an em-dash reason are errors
// (unless blank or # comments).
func ParseSkipFile(path string) ([]SkipEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSkipBytes(data)
}

// ParseSkipBytes parses skip.txt content.
func ParseSkipBytes(data []byte) ([]SkipEntry, error) {
	var out []SkipEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, reason, ok := strings.Cut(line, emDash)
		if !ok {
			// Also accept ASCII --- as a migration aid, but still require a reason.
			name, reason, ok = strings.Cut(line, " --- ")
			if !ok {
				return nil, fmt.Errorf("%s:%d: skip without reason (want `<name> — <reason>`): %q", skipFileName, lineNo, line)
			}
		}
		name = strings.TrimSpace(name)
		reason = strings.TrimSpace(reason)
		if name == "" || reason == "" {
			return nil, fmt.Errorf("%s:%d: skip without reason (want `<name> — <reason>`): %q", skipFileName, lineNo, line)
		}
		out = append(out, SkipEntry{Name: name, Reason: reason, Raw: line})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SkipMap indexes skip entries by relative test name (slash-separated).
func SkipMap(entries []SkipEntry) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.Name] = e.Reason
	}
	return m
}

// FormatSkipLine builds a canonical skip.txt line.
func FormatSkipLine(name, reason string) string {
	return name + " " + emDash + " " + reason
}

// LoadBaseSkipEntries reads skip.txt from the comparison base (WASMGC_SKIP_BASE
// ref, default origin/main). The base ref must resolve (shallow CI checkouts
// need fetch-depth: 0 or an explicit fetch of main). Missing file on that ref
// → empty list (first introduction of skip.txt). A missing ref is an error so
// we never treat "could not see main" as "main has zero skips".
func LoadBaseSkipEntries(repoRoot, skipRel string) ([]SkipEntry, error) {
	base := os.Getenv("WASMGC_SKIP_BASE")
	if base == "" {
		base = "origin/main"
	}
	rev := exec.Command("git", "rev-parse", "--verify", base)
	rev.Dir = repoRoot
	if out, err := rev.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("skip baseline ref %q not available (fetch the base branch, e.g. git fetch origin main): %w\n%s",
			base, err, strings.TrimSpace(string(out)))
	}
	cmd := exec.Command("git", "show", base+":"+filepath.ToSlash(skipRel))
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// File missing on base → empty list.
		return nil, nil
	}
	return ParseSkipBytes(out)
}

// SkipGrowth reports names present in current but not in base.
func SkipGrowth(base, current []SkipEntry) []string {
	bm := SkipMap(base)
	var added []string
	for _, e := range current {
		if _, ok := bm[e.Name]; !ok {
			added = append(added, e.Name)
		}
	}
	sort.Strings(added)
	return added
}

// ReadSkipBump returns the one-line justification if skip_bump.txt exists.
func ReadSkipBump(dir string) (string, bool, error) {
	path := filepath.Join(dir, skipBumpFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	line := strings.TrimSpace(string(data))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if line == "" {
		return "", true, fmt.Errorf("%s exists but is empty", skipBumpFileName)
	}
	return line, true, nil
}

// MergeSkipFile appends new entries (by name) to skip.txt, keeping existing.
func MergeSkipFile(path string, additions []SkipEntry) error {
	existing, err := ParseSkipFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	m := SkipMap(existing)
	for _, a := range additions {
		if _, ok := m[a.Name]; ok {
			continue
		}
		existing = append(existing, a)
		m[a.Name] = a.Reason
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].Name < existing[j].Name })
	var b strings.Builder
	b.WriteString("# skip.txt — $GOROOT/test // run cases that must not fail CI.\n")
	b.WriteString("# Format: <name> — <reason>\n")
	b.WriteString("# A skip without a reason fails CI. Growing this list fails CI unless\n")
	b.WriteString("# skip_bump.txt holds a one-line reviewed justification.\n")
	b.WriteString("#\n")
	b.WriteString("# Stdlib (encoding/json, gob) StructOf skips live under stdlib/ — see README.\n\n")
	for _, e := range existing {
		b.WriteString(FormatSkipLine(e.Name, e.Reason))
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// CategorizeFailure picks a short skip reason from compile/run errors.
func CategorizeFailure(errMsg string) string {
	msg := strings.ToLower(errMsg)
	switch {
	case strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "build constraints exclude all"):
		return "build constraints exclude file"
	case strings.Contains(msg, "cgo") && strings.Contains(msg, "unsupported"):
		return "unsupported: cgo"
	case strings.Contains(msg, "range over func") || strings.Contains(msg, "range-over-func"):
		return "range-over-func"
	case strings.Contains(msg, "unsupported unsafe") || strings.Contains(msg, "unsafe.pointer") || strings.Contains(msg, "uintptr(unsafe"):
		return "unsupported: unsafe"
	case strings.Contains(msg, "output does not match") || strings.Contains(msg, "output mismatch") || strings.Contains(msg, "output should be empty"):
		return "output mismatch"
	case strings.Contains(msg, "could not import"):
		pkg := "stdlib import"
		if i := strings.Index(errMsg, "could not import "); i >= 0 {
			rest := errMsg[i+len("could not import "):]
			if j := strings.IndexAny(rest, " \n("); j > 0 {
				pkg = "could not import " + strings.Trim(rest[:j], `"`)
			}
		}
		return pkg
	case strings.Contains(msg, "compileerror") || strings.Contains(msg, "webassembly.compile"):
		return "wasm validate/compile error"
	case strings.Contains(msg, "illegal cast") || strings.Contains(msg, "runtimeerror"):
		return "runtime error"
	case strings.Contains(msg, "lower:"):
		if i := strings.Index(errMsg, "lower:"); i >= 0 {
			return "compile error: " + clipReason(errMsg[i:])
		}
		return "compile error: lower"
	case strings.Contains(msg, "codegen:"):
		return "compile error: codegen"
	case strings.Contains(msg, "load:") || strings.Contains(msg, "typecheck"):
		return "compile error: " + clipReason(errMsg)
	default:
		r := clipReason(errMsg)
		if r == "" {
			return "failed"
		}
		return "failed: " + r
	}
}

func clipReason(s string) string {
	r := strings.TrimSpace(s)
	r = strings.ReplaceAll(r, "\n", " ")
	// Drop duplicated temp-dir noise from driver stderr echoes.
	if i := strings.Index(r, " gotestcase:"); i > 0 {
		r = strings.TrimSpace(r[:i])
	}
	if len(r) > 100 {
		r = r[:100] + "…"
	}
	return r
}
