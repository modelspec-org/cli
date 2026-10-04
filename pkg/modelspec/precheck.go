package modelspec

import (
	"bytes"
	"fmt"
	"unicode/utf8"

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
	// every token of a file in memory at once, and the peak is about 420 to 470
	// MiB for each MB of one-byte tokens, so 1 MiB keeps one file under 500 MiB
	// and about a second of work. Chinook's model is under 8 KB and the largest
	// corpus file 30 KB; a module is a set of files, each under the limit.
	MaxInputBytes = 1 << 20
	// MaxDepth is the deepest nesting of HCL braces, brackets, quoted strings and
	// heredocs, counted from tokens (blocks, lists, objects), and of JSON arrays
	// and objects. Real models nest four or five levels in HCL, six to eight in
	// the JSON form.
	MaxDepth = 64
	// MaxHeredocLines is the most lines one heredoc may have. The lexer makes one
	// piece of each line, and the HCL parser joins the pieces of a template one at
	// a time, copying the text and shifting the rest of the list each time: time
	// that grows with the square of their number. (Pieces that the lexer also
	// starts at each `$` and `%`, and in quoted strings, are removed before the
	// parser sees them: see parserInput.) No real heredoc, a query or a pattern,
	// comes near the limit.
	MaxHeredocLines = 1000
	// MaxSyntaxFindings is the most syntax errors, or non-literal tokens, reported
	// for one HCL file.
	MaxSyntaxFindings = 50
)

// precheck returns a finding when the source must not be read: it is too large,
// or is not valid UTF-8. Findings say where.
func precheck(file string, src []byte) (Finding, bool) {
	if len(src) > MaxInputBytes {
		return oversize(file, int64(len(src)), false), true
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

// oversize is the finding for a source of the given size that exceeds the limit;
// partial says the size is how much was read before reading stopped, not the
// size of the file.
func oversize(file string, size int64, partial bool) Finding {
	if partial {
		return Finding{File: file, Rule: RuleLimit, Severity: SeverityError, Message: fmt.Sprintf("file is more than %d bytes; the limit is %d bytes", size-1, MaxInputBytes)}
	}
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

// hclLiterals returns findings for what must stop a lexed HCL source being
// parsed: a token that a literal value cannot contain (rule literal, one finding
// for each construct on a line, at most MaxSyntaxFindings), or nesting deeper than
// MaxDepth, a heredoc longer than MaxHeredocLines, a number or a name over its
// limit (limits.go) (rule limit). Only real
// tokens count: brackets, quotes and operators inside strings, heredocs and
// comments are text, and `-` is allowed as the sign of a number.
func hclLiterals(file string, lexed hclsyntax.Tokens) []Finding {
	// The tokens that matter here, by index: newlines and comments do not.
	significant := make([]int32, 0, len(lexed))
	for i, t := range lexed {
		if t.Type != hclsyntax.TokenNewline && t.Type != hclsyntax.TokenComment {
			significant = append(significant, int32(i))
		}
	}
	tok := func(i int) hclsyntax.Token { return lexed[significant[i]] }
	var out []Finding
	seen := map[string]bool{}
	depth, lines := 0, 0
	prev := hclsyntax.TokenNil
	attr := "" // the name of the attribute whose value is being read
	for i := range significant {
		t := tok(i)
		what := notLiteral[t.Type]
		problem := ""
		switch t.Type {
		case hclsyntax.TokenNumberLit:
			problem = numberProblem(string(t.Bytes))
		case hclsyntax.TokenIdent:
			problem = nameProblem("an identifier", len(t.Bytes))
		case hclsyntax.TokenEqual:
			if prev == hclsyntax.TokenIdent {
				attr = string(tok(i - 1).Bytes)
			}
		case hclsyntax.TokenOQuote:
			// A string is a name when it labels a block (after the block type or
			// another label), is the value of an attribute that holds a name, or an
			// item of one that holds a list of them.
			isName := prev == hclsyntax.TokenIdent || prev == hclsyntax.TokenCQuote ||
				(nameAttrs[attr] && prev == hclsyntax.TokenEqual) ||
				(listNameAttrs[attr] && (prev == hclsyntax.TokenOBrack || prev == hclsyntax.TokenComma))
			if isName {
				problem = nameProblem("a name", quotedLength(tok, i, len(significant)))
			}
		}
		switch t.Type {
		case hclsyntax.TokenOHeredoc:
			lines = 0
		case hclsyntax.TokenStringLit:
			if lines += bytes.Count(t.Bytes, []byte{'\n'}); lines > MaxHeredocLines {
				return append(out, Finding{File: file, Line: t.Range.Start.Line, Rule: RuleLimit, Severity: SeverityError, Message: fmt.Sprintf("a heredoc has more than %d lines; the parser's time grows with the square of the number of lines", MaxHeredocLines)})
			}
		}
		switch t.Type {
		case hclsyntax.TokenOBrace, hclsyntax.TokenOBrack, hclsyntax.TokenOQuote, hclsyntax.TokenOHeredoc:
			if depth++; depth > MaxDepth {
				return append(out, Finding{File: file, Line: t.Range.Start.Line, Rule: RuleLimit, Severity: SeverityError, Message: fmt.Sprintf("nesting of brackets, strings and blocks is deeper than %d levels", MaxDepth)})
			}
			if t.Type == hclsyntax.TokenOBrack && valueEnd[prev] {
				what = whatIndex
			}
			if (t.Type == hclsyntax.TokenOBrack || t.Type == hclsyntax.TokenOBrace) && i+2 < len(significant) &&
				tok(i+1).Type == hclsyntax.TokenIdent && string(tok(i+1).Bytes) == "for" && tok(i+2).Type == hclsyntax.TokenIdent {
				what = whatFor
			}
		case hclsyntax.TokenCBrace, hclsyntax.TokenCBrack, hclsyntax.TokenCQuote, hclsyntax.TokenCHeredoc:
			if depth > 0 {
				depth--
			}
		case hclsyntax.TokenMinus:
			// (The tokens end with an end-of-file token, so a `-` always has one after it.)
			if valueEnd[prev] || tok(i+1).Type != hclsyntax.TokenNumberLit {
				what = whatMinus
			}
		}
		prev = t.Type
		rule, msg := RuleLiteral, ""
		switch {
		case what != "":
			msg = what + " is not literal syntax (decision 0009: a ModelSpec value is a string, number, boolean or list); the file is refused before it is parsed"
		case problem != "":
			rule, what, msg = RuleLimit, problem, problem
		default:
			continue
		}
		line := t.Range.Start.Line
		key := fmt.Sprintf("%d %s", line, what)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(out) == MaxSyntaxFindings {
			more := "non-literal syntax"
			if rule == RuleLimit {
				more = "numbers and names over their limits"
			}
			return append(out, Finding{File: file, Line: line, Rule: rule, Severity: SeverityError, Message: fmt.Sprintf("more %s follows; only the first %d are shown", more, MaxSyntaxFindings)})
		}
		out = append(out, Finding{File: file, Line: line, Rule: rule, Severity: SeverityError, Message: msg})
	}
	return out
}

// quotedLength returns the length in bytes, as written, of the quoted string
// whose opening quote is the significant token i of n: the text between the
// quotes (interpolations and directives were refused before, so it is all text).
func quotedLength(tok func(int) hclsyntax.Token, i, n int) int {
	length := 0
	for j := i + 1; j < n && tok(j).Type == hclsyntax.TokenQuotedLit; j++ {
		length += len(tok(j).Bytes)
	}
	return length
}
