package modelspec

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// lexHCL splits an HCL source into tokens. The lexer does not recurse, and takes
// time linear in the size of the source.
func lexHCL(file string, src []byte) hclsyntax.Tokens {
	tokens, _ := hclsyntax.LexConfig(src, file, hcl.Pos{Line: 1, Column: 1})
	return tokens
}

// The HCL parser joins the pieces of a string or heredoc one at a time, copying
// the text and shifting the rest of the list each time, so the time it takes
// grows with the square of the number of pieces. The lexer starts a piece at each
// `$` and `%` (and, in a heredoc, at each line). parserInput rewrites those two
// characters inside literal text so that each such `$` or `%` stays inside its
// piece, and the number of pieces is the number of lines.
//
//   - In a quoted string `$` and `%` become the escapes `\u0024` and `\u0025`, which
//     the parser decodes to the same text, so nothing needs to be undone.
//   - A heredoc has no escapes, so they become private-use characters, which
//     unescapeHeredoc turns back into `$` and `%` in the value of an attribute
//     that is a heredoc. A private-use character that is already in the heredoc
//     is kept apart from them by a third one.
//
// The escapes `$${` and `%%{` (a literal `${` and `%{`) are rewritten to the
// character and the brace. Interpolations and directives are never there: they
// were refused before this (hclLiterals). The rewrite changes no newline, so
// line numbers are the same, and it changes no token boundary.
const (
	heredocDollar  = '\ue000'
	heredocPercent = '\ue001'
	heredocEscape  = '\ue002'
)

// shift records that the parser's input is delta bytes longer than the source
// from the offset at (in the parser's input) on.
type shift struct{ at, delta int }

// offsetMap maps an offset in the parser's input back to the source. The rewrite
// of parserInput changes the inside of literal text only, so an offset that is
// not inside such text (the start of a name, of a block type) maps exactly.
type offsetMap []shift

// source returns the offset in the source of the offset off in the parser's input.
func (m offsetMap) source(off int) int {
	i := sort.Search(len(m), func(i int) bool { return m[i].at > off })
	if i == 0 {
		return off
	}
	return off - m[i-1].delta
}

// parserInput returns the source to give the HCL parser, the offsets (in that
// source) at which a heredoc begins, whose values need unescapeHeredoc, the
// text of the number that begins at each offset (the reader writes a number from
// its own text, see canonicalNumber), and the map from offsets in that source
// back to the original.
func parserInput(src []byte, tokens hclsyntax.Tokens) ([]byte, map[int]bool, map[int]string, offsetMap) {
	var out []byte
	var offsets offsetMap
	heredocs := map[int]bool{}
	numbers := map[int]string{}
	last := 0
	for _, t := range tokens {
		var rewritten string
		switch t.Type {
		case hclsyntax.TokenOHeredoc:
			out = append(out, src[last:t.Range.Start.Byte]...)
			last = t.Range.Start.Byte
			heredocs[len(out)] = true
			continue
		case hclsyntax.TokenNumberLit:
			numbers[len(out)+t.Range.Start.Byte-last] = string(t.Bytes)
			continue
		case hclsyntax.TokenQuotedLit:
			rewritten = rewriteText(string(t.Bytes), `\u0024`, `\u0025`, false)
		case hclsyntax.TokenStringLit:
			rewritten = rewriteText(string(t.Bytes), string(heredocDollar), string(heredocPercent), true)
		default:
			continue
		}
		if rewritten == string(t.Bytes) {
			continue
		}
		out = append(out, src[last:t.Range.Start.Byte]...)
		out = append(out, rewritten...)
		last = t.Range.End.Byte
		delta := len(rewritten) - len(t.Bytes)
		if n := len(offsets); n > 0 {
			delta += offsets[n-1].delta
		}
		offsets = append(offsets, shift{at: len(out), delta: delta})
	}
	return append(out, src[last:]...), heredocs, numbers, offsets
}

// rewriteText replaces `$` and `%` in the text of a literal token by dollar and
// percent, and `$${` and `%%{` by the same followed by the brace. In a heredoc
// a private-use character of its own is protected by an escape. In a quoted
// string a backslash and the character after it are one unit that is copied as it
// is, so that an escape the HCL library refuses (`\$`, `\%`) reaches it as written
// and is refused as before, and `\\$` is a backslash and then a `$`.
func rewriteText(text, dollar, percent string, heredoc bool) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		switch {
		case !heredoc && text[i] == '\\':
			_, size := utf8.DecodeRuneInString(text[i+1:])
			b.WriteString(text[i : i+1+size])
			i += 1 + size
		case heredoc && (text[i] == '$' || text[i] == '%') && strings.HasPrefix(text[i+1:], "\r"):
			// The lexer takes the character after a `$` or `%` into the same piece,
			// and a carriage return there is how a line may end in `$` or `%` with
			// a bare CR before the line break. It is kept with the `$`, as written.
			b.WriteString(text[i : i+2])
			i += 2
		case strings.HasPrefix(text[i:], "$${"):
			b.WriteString(dollar + "{")
			i += 3
		case strings.HasPrefix(text[i:], "%%{"):
			b.WriteString(percent + "{")
			i += 3
		case text[i] == '$':
			b.WriteString(dollar)
			i++
		case text[i] == '%':
			b.WriteString(percent)
			i++
		default:
			r, size := utf8.DecodeRuneInString(text[i:])
			if heredoc && (r == heredocDollar || r == heredocPercent || r == heredocEscape) {
				b.WriteRune(heredocEscape)
			}
			b.WriteString(text[i : i+size])
			i += size
		}
	}
	return b.String()
}

// unescapeHeredoc undoes the rewrite of a heredoc's text.
func unescapeHeredoc(s string) string {
	var b strings.Builder
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == heredocEscape:
			escaped = true
		case r == heredocDollar:
			b.WriteByte('$')
		case r == heredocPercent:
			b.WriteByte('%')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
