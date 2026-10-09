package modelspec

import (
	"fmt"
	"unicode/utf8"
)

// Limits on single tokens. They are applied to the lexer's tokens before the HCL
// parser runs (precheck) and to the JSON reader's tokens as it reads, so that no
// later stage handles a token it was not built for. The standard sets none of
// them; they are this tool's, stricter than the standard, and each is refused as
// a `limit` finding that says what was counted and what the limit is.
const (
	// MaxNumberLength is the most characters one number literal may have. The
	// HCL library reads a number into 512 bits, about 154 decimal digits, and
	// rounds beyond that, so two different long integers could look equal. With
	// at most 40 characters (and an exponent within MaxNumberExponent) every
	// accepted number is read exactly, and two numbers are equal only when they
	// are the same value. It also bounds the time to read one: the library's
	// time to read `1e10000000`, 10 bytes, was ten seconds.
	MaxNumberLength = 40
	// MaxNumberExponent is the largest exponent, either sign, a number literal
	// may have: with at most MaxNumberLength characters a number is then between
	// 1e-140 and 1e140, a size where a decimal is read, compared and written
	// exactly and in constant time.
	MaxNumberExponent = 100
	// MaxNameLength is the most bytes, as written, a name may have: a block
	// label, an identifier or attribute name, a type, record, component or enum
	// reference, and an item of `key` or `use` (JSON: every object key and
	// the same strings). The standard states no length. Real names are a few
	// words, and where the databases a model describes limit an identifier it is
	// between 63 and 128 bytes, so 255 leaves room.
	MaxNameLength = 255
	// MaxMessageBytes is the most bytes of one finding's message. Longer ones are
	// cut with a marker (clipText), and so is every piece of the user's text
	// that a message echoes (MaxEchoBytes), so that one finding cannot be large
	// however large the input is.
	MaxMessageBytes = 1024
	// MaxEchoBytes is the most bytes of one piece of the user's text, such as a
	// name or a value, a message repeats: as many as a name may have, so a name
	// is always shown whole.
	MaxEchoBytes = MaxNameLength
)

// nameAttrs are the HCL attributes (and JSON keys) whose string value is a name.
// listNameAttrs are the ones whose value is a list of names.
var (
	nameAttrs     = map[string]bool{"type": true, "record": true, "entity": true, "component": true, "enum": true, "name": true}
	listNameAttrs = map[string]bool{"key": true, "use": true}
)

// nameProblem returns what is wrong with a name of the given length in bytes, or
// "".
func nameProblem(what string, length int) string {
	if length > MaxNameLength {
		return fmt.Sprintf("%s is %d bytes long; the limit is %d", what, length, MaxNameLength)
	}
	return ""
}

// clipText returns s when it has at most n bytes, and otherwise its beginning
// followed by a marker that says how long it was, in at most n bytes in all and
// ending on a character boundary. Every message and every piece of the user's
// text in a message goes through it.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	marker := fmt.Sprintf("…[%d bytes in all]", len(s))
	keep := max(n-len(marker), 0)
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + marker
}
