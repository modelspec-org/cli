package modelspec

import (
	"fmt"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Limits and the literal-only rule, applied before a source is parsed, so that
// hostile or accidental input is a finding and not a crash. The HCL parser is
// recursive and a stack overflow in Go is fatal and cannot be recovered. Rather
// than bound the recursive constructs one by one, the HCL source is lexed (the
// lexer does not recurse) and refused before parsing if it holds any token that a
// literal value cannot contain; what is left to bound is the nesting of brackets.
const (
	// MaxInputBytes is the largest source, in bytes, that is read. The lexer holds
	// every token of a file in memory at once, about a hundred bytes each, so the
	// limit also bounds that.
	MaxInputBytes = 4 << 20
	// MaxDepth is the deepest nesting of HCL braces, brackets, quoted strings and
	// heredocs, counted from tokens (blocks, lists, objects), and of JSON arrays
	// and objects. Real models nest four or five levels in HCL, six to eight in
	// the JSON form.
	MaxDepth = 64
	// MaxHeredocLines is the most lines one heredoc may have. The HCL parser joins
	// the pieces of a heredoc, one for each line, in time that grows with the
	// square of their number: a heredoc of 40,000 short lines takes over a second,
	// one of 400,000 lines over a minute. (A quoted string is one piece, however
	// many escape sequences it has.) Real heredocs are patterns and queries.
	MaxHeredocLines = 1000
	// MaxSyntaxFindings is the most syntax errors, or non-literal tokens, reported
	// for one HCL file.
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

// notLiteral names the tokens a ModelSpec value cannot contain. ModelSpec v0 HCL
// has literal values only (decision 0009; spec/hcl-authoring.md, "V0 Grammar
// Scope"): strings, numbers, booleans and lists, in blocks and attributes. Every
// construct that makes the HCL parser recurse, or take more than linear time, is
// written with one of these tokens.
var notLiteral = map[hclsyntax.TokenType]string{
	hclsyntax.TokenOParen:          "a parenthesis (grouping or a function call)",
	hclsyntax.TokenCParen:          "a parenthesis (grouping or a function call)",
	hclsyntax.TokenStar:            "`*` (a splat or a multiplication)",
	hclsyntax.TokenSlash:           "an arithmetic operator",
	hclsyntax.TokenPlus:            "an arithmetic operator",
	hclsyntax.TokenPercent:         "an arithmetic operator",
	hclsyntax.TokenEqualOp:         "a comparison operator",
	hclsyntax.TokenNotEqual:        "a comparison operator",
	hclsyntax.TokenLessThan:        "a comparison operator",
	hclsyntax.TokenLessThanEq:      "a comparison operator",
	hclsyntax.TokenGreaterThan:     "a comparison operator",
	hclsyntax.TokenGreaterThanEq:   "a comparison operator",
	hclsyntax.TokenAnd:             "a logical operator",
	hclsyntax.TokenOr:              "a logical operator",
	hclsyntax.TokenBang:            "a logical operator (`!`)",
	hclsyntax.TokenDot:             "`.` (a traversal)",
	hclsyntax.TokenDoubleColon:     "`::` (a function namespace)",
	hclsyntax.TokenEllipsis:        "`...` (an argument or `for` expansion)",
	hclsyntax.TokenFatArrow:        "`=>` (a `for` expression)",
	hclsyntax.TokenQuestion:        "a conditional (`?`)",
	hclsyntax.TokenTemplateInterp:  "a template interpolation (`${`)",
	hclsyntax.TokenTemplateControl: "a template directive (`%{`)",
}

const (
	whatMinus = "an operator (`-` is allowed only as the sign of a number)"
	whatIndex = "an index or a splat on a value (`[`)"
	whatFor   = "a `for` expression"
)

// valueEnd is the tokens a value can end with: after one, a `[` is an index or a
// splat, and a `-` is a subtraction.
var valueEnd = map[hclsyntax.TokenType]bool{
	hclsyntax.TokenIdent: true, hclsyntax.TokenNumberLit: true, hclsyntax.TokenCBrack: true,
	hclsyntax.TokenCBrace: true, hclsyntax.TokenCQuote: true, hclsyntax.TokenCHeredoc: true,
}

// hclLiterals lexes an HCL source and returns findings for what must stop it
// being parsed: a token that a literal value cannot contain (rule literal, one
// finding for each construct on a line, at most MaxSyntaxFindings), or nesting
// deeper than MaxDepth (rule limit). Only real tokens count: brackets, quotes and
// operators inside strings, heredocs and comments are text, and `-` is allowed as
// the sign of a number.
func hclLiterals(file string, src []byte) []Finding {
	lexed, _ := hclsyntax.LexConfig(src, file, hcl.Pos{Line: 1, Column: 1})
	tokens := lexed[:0] // newlines and comments do not matter here
	for _, t := range lexed {
		if t.Type != hclsyntax.TokenNewline && t.Type != hclsyntax.TokenComment {
			tokens = append(tokens, t)
		}
	}
	var out []Finding
	seen := map[string]bool{}
	depth, parts := 0, 0
	prev := hclsyntax.TokenNil
	for i, t := range tokens {
		what := notLiteral[t.Type]
		if t.Type == hclsyntax.TokenStringLit {
			if parts++; parts > MaxHeredocLines {
				return append(out, Finding{File: file, Line: t.Range.Start.Line, Rule: RuleLimit, Severity: SeverityError, Message: fmt.Sprintf("a heredoc has more than %d lines; the parser's time grows with the square of that", MaxHeredocLines)})
			}
		} else {
			parts = 0
		}
		switch t.Type {
		case hclsyntax.TokenOBrace, hclsyntax.TokenOBrack, hclsyntax.TokenOQuote, hclsyntax.TokenOHeredoc:
			if depth++; depth > MaxDepth {
				return append(out, Finding{File: file, Line: t.Range.Start.Line, Rule: RuleLimit, Severity: SeverityError, Message: fmt.Sprintf("nesting of brackets, strings and blocks is deeper than %d levels", MaxDepth)})
			}
			if t.Type == hclsyntax.TokenOBrack && valueEnd[prev] {
				what = whatIndex
			}
			if (t.Type == hclsyntax.TokenOBrack || t.Type == hclsyntax.TokenOBrace) && i+2 < len(tokens) &&
				tokens[i+1].Type == hclsyntax.TokenIdent && string(tokens[i+1].Bytes) == "for" && tokens[i+2].Type == hclsyntax.TokenIdent {
				what = whatFor
			}
		case hclsyntax.TokenCBrace, hclsyntax.TokenCBrack, hclsyntax.TokenCQuote, hclsyntax.TokenCHeredoc:
			if depth > 0 {
				depth--
			}
		case hclsyntax.TokenMinus:
			if valueEnd[prev] || i+1 == len(tokens) || tokens[i+1].Type != hclsyntax.TokenNumberLit {
				what = whatMinus
			}
		}
		prev = t.Type
		if what == "" {
			continue
		}
		line := t.Range.Start.Line
		key := fmt.Sprintf("%d %s", line, what)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(out) == MaxSyntaxFindings {
			return append(out, Finding{File: file, Line: line, Rule: RuleLiteral, Severity: SeverityError, Message: fmt.Sprintf("more non-literal syntax follows; only the first %d are shown", MaxSyntaxFindings)})
		}
		out = append(out, Finding{File: file, Line: line, Rule: RuleLiteral, Severity: SeverityError, Message: what + " is not literal syntax (decision 0009: a ModelSpec value is a string, number, boolean or list); the file is refused before it is parsed"})
	}
	return out
}
