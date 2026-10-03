package modelspec

import (
	"fmt"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Limits applied before a source is parsed, so that hostile or accidental input
// is a finding and not a crash. The HCL parser is recursive, a stack overflow in
// Go is fatal and cannot be recovered, so the constructs that make it recurse are
// bounded first, from the lexer's tokens (the lexer does not recurse).
//
// The limits bound the recursion that was found by reading the parser
// (hclsyntax parser.go and parser_template.go, hcl v2.24.0). They are not a
// proof that no input can crash it: a crash on a hostile file is a bug to report.
const (
	// MaxInputBytes is the largest source, in bytes, that is read. The lexer holds
	// every token of a file in memory at once, about a hundred bytes each, so the
	// limit also bounds that.
	MaxInputBytes = 4 << 20
	// MaxDepth is the deepest nesting of HCL brackets and templates, counted from
	// tokens (blocks, tuples, objects, parentheses, calls, indexes, quoted
	// strings, heredocs, interpolations, template directives), and of JSON arrays
	// and objects. Real models nest four or five levels in HCL, six to eight in
	// the JSON form.
	MaxDepth = 64
	// MaxOperatorRun is the longest run of unary operators (- or !) in HCL, which
	// the parser reads by one recursive call each.
	MaxOperatorRun = 64
	// MaxConditionals is the most conditional operators (? :) in one HCL file; the
	// parser reads each nested or chained one by recursion. ModelSpec allows no
	// expressions (decision 0009), so a file never needs one.
	MaxConditionals = 64
	// MaxSyntaxFindings is the most syntax errors reported for one HCL file.
	MaxSyntaxFindings = 50
)

// precheck returns a finding when the source must not be read: it is too large,
// or is not valid UTF-8. Findings say where.
func precheck(file string, src []byte) (Finding, bool) {
	if len(src) > MaxInputBytes {
		return oversize(file, int64(len(src))), true
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
		return Finding{File: file, Line: line, Rule: RuleEncoding, Severity: SeverityError, Message: "not valid UTF-8; ModelSpec sources are UTF-8"}, true
	}
	return Finding{}, false
}

// oversize is the finding for a source of the given size that exceeds the limit.
func oversize(file string, size int64) Finding {
	return Finding{File: file, Rule: RuleLimit, Severity: SeverityError, Message: fmt.Sprintf("file is %d bytes; the limit is %d bytes", size, MaxInputBytes)}
}

// hclLimits lexes an HCL source and returns a finding when a construct that makes
// the parser recurse exceeds its limit. Only real tokens count: brackets inside
// strings, heredocs and comments are not tokens of their own.
func hclLimits(file string, src []byte) (Finding, bool) {
	tokens, _ := hclsyntax.LexConfig(src, file, hcl.Pos{Line: 1, Column: 1})
	fail := func(t hclsyntax.Token, what string, limit int) (Finding, bool) {
		return Finding{File: file, Line: t.Range.Start.Line, Rule: RuleLimit, Severity: SeverityError, Message: fmt.Sprintf("%s: more than %d; the parser recurses on it, so it is refused before parsing", what, limit)}, true
	}
	depth, directives, run, conditionals := 0, 0, 0, 0
	for i, t := range tokens {
		switch t.Type {
		case hclsyntax.TokenOBrace, hclsyntax.TokenOBrack, hclsyntax.TokenOParen, hclsyntax.TokenOQuote,
			hclsyntax.TokenOHeredoc, hclsyntax.TokenTemplateInterp, hclsyntax.TokenTemplateControl:
			if depth++; depth > MaxDepth {
				return fail(t, "nesting of brackets, strings and templates", MaxDepth)
			}
			if t.Type == hclsyntax.TokenTemplateControl && i+1 < len(tokens) && tokens[i+1].Type == hclsyntax.TokenIdent {
				switch string(tokens[i+1].Bytes) {
				case "if", "for":
					if directives++; directives > MaxDepth {
						return fail(t, "nesting of template directives (%{if}, %{for})", MaxDepth)
					}
				case "endif", "endfor":
					if directives > 0 {
						directives--
					}
				}
			}
		case hclsyntax.TokenCBrace, hclsyntax.TokenCBrack, hclsyntax.TokenCParen, hclsyntax.TokenCQuote,
			hclsyntax.TokenCHeredoc, hclsyntax.TokenTemplateSeqEnd:
			if depth > 0 {
				depth--
			}
		case hclsyntax.TokenQuestion:
			if conditionals++; conditionals > MaxConditionals {
				return fail(t, "conditional operators (?:)", MaxConditionals)
			}
		}
		// A run of unary operators; newlines and comments between them do not end it.
		switch t.Type {
		case hclsyntax.TokenMinus, hclsyntax.TokenBang:
			if run++; run > MaxOperatorRun {
				return fail(t, "run of unary operators (- or !)", MaxOperatorRun)
			}
		case hclsyntax.TokenNewline, hclsyntax.TokenComment:
		default:
			run = 0
		}
	}
	return Finding{}, false
}
