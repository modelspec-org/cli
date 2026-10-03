package modelspec

import (
	"fmt"
	"unicode/utf8"
)

// Limits applied before a source is parsed, so that hostile or accidental input
// is a finding and not a crash. The HCL parser recurses once per nesting level
// and a stack overflow in Go is fatal, so nesting has to be bounded first.
const (
	// MaxInputBytes is the largest source, in bytes, that is read.
	MaxInputBytes = 16 << 20
	// MaxDepth is the deepest nesting of brackets ({ [ ( in HCL, { [ in JSON).
	MaxDepth = 64
)

// precheck returns a finding when the source must not be parsed: it is too
// large, is not valid UTF-8, or nests too deeply. Findings say where.
func precheck(file string, form Form, src []byte) (Finding, bool) {
	fail := func(line int, rule, format string, args ...any) (Finding, bool) {
		return Finding{File: file, Line: line, Rule: rule, Severity: SeverityError, Message: fmt.Sprintf(format, args...)}, true
	}
	if len(src) > MaxInputBytes {
		return fail(0, RuleLimit, "file is %d bytes; the limit is %d bytes", len(src), MaxInputBytes)
	}
	if !utf8.Valid(src) {
		line := 1
		for i := 0; i < len(src); {
			r, size := utf8.DecodeRune(src[i:])
			if r == utf8.RuneError && size == 1 {
				break
			}
			if r == '\n' {
				line++
			}
			i += size
		}
		return fail(line, RuleEncoding, "not valid UTF-8; ModelSpec sources are UTF-8")
	}
	if line := tooDeep(form, src); line > 0 {
		return fail(line, RuleLimit, "nesting is deeper than %d levels", MaxDepth)
	}
	return Finding{}, false
}

// tooDeep returns the line where bracket nesting first exceeds MaxDepth, or 0.
// Strings are skipped, and in HCL so are # and // comments and /* */ comments.
func tooDeep(form Form, src []byte) int {
	depth, line := 0, 1
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '\n':
			line++
		case c == '"':
			for i++; i < len(src) && src[i] != '"'; i++ {
				switch src[i] {
				case '\\':
					i++
				case '\n':
					line++
				}
			}
		case form == FormHCL && (c == '#' || (c == '/' && i+1 < len(src) && src[i+1] == '/')):
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i-- // the newline is counted by the next iteration
		case form == FormHCL && c == '/' && i+1 < len(src) && src[i+1] == '*':
			for i += 2; i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/'); i++ {
				if src[i] == '\n' {
					line++
				}
			}
			i++
		case c == '{' || c == '[' || (form == FormHCL && c == '('):
			depth++
			if depth > MaxDepth {
				return line
			}
		case c == '}' || c == ']' || (form == FormHCL && c == ')'):
			if depth > 0 {
				depth--
			}
		}
	}
	return 0
}
