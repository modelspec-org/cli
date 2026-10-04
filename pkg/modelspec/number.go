package modelspec

import (
	"fmt"
	"strconv"
	"strings"
)

// A number has one canonical form, made from its text by string arithmetic, with
// no floating point. Every reader (HCL and JSON), the comparison of equal values
// (duplicate enum values), the comparison of a twin with its source and export use
// it, so that equal values are equal however they are spelled (`1`, `1.0`, `1e0`,
// `10e-1`; `-0` and `0`; `1e+100` and `1e100`), and what export writes is
// something this tool reads back.
//
// A number is read as digits times a power of ten: the digits with no leading and
// no trailing zeros (the trailing ones counted in the exponent), and the exponent.
// The canonical form is
//
//   - 0 for zero, with no sign;
//   - the plain decimal, with a minus sign for a negative number, no leading zeros
//     but the one before a point, no trailing zeros after it, and no exponent,
//     when that has at most MaxNumberLength characters;
//   - otherwise the digits, an `e` and the exponent: 1e41, 15e-50, -123e60.
//
// Every number within the limits has a canonical form within them: it has at most
// as many characters as the number was written with, and its exponent is the one
// the limit is on.

// decimal is a number as digits times ten to the exp: digits have no leading or
// trailing zero, and are empty for zero.
type decimal struct {
	neg    bool
	digits string
	exp    int
}

// readDecimal reads the text of a number: digits with an optional point, an
// optional exponent, an optional leading minus sign. It returns a problem, as a
// message, when the exponent is too large to be an integer. It does not check the
// limits.
func readDecimal(text string) (d decimal, problem string) {
	d.neg = strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	exp := 0
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		e, err := strconv.Atoi(text[i+1:])
		if err != nil {
			return d, fmt.Sprintf("a number has the exponent %s, which is too large; the limit is %d either way", text[i+1:], MaxNumberExponent)
		}
		exp, text = e, text[:i]
	}
	whole, fraction, _ := strings.Cut(text, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	trimmed := strings.TrimRight(digits, "0")
	if trimmed == "" {
		return decimal{}, ""
	}
	d.digits = trimmed
	d.exp = exp - len(fraction) + (len(digits) - len(trimmed))
	return d, ""
}

// numberProblem returns what is wrong with a number literal, as a message, or ""
// when it is within the limits: at most MaxNumberLength characters (not counting a
// sign), and, as digits times a power of ten, an exponent within MaxNumberExponent
// either way, which makes every accepted number readable exactly.
func numberProblem(text string) string {
	if n := len(strings.TrimPrefix(text, "-")); n > MaxNumberLength {
		return fmt.Sprintf("a number is %d characters long; the limit is %d (a longer number cannot be read exactly)", n, MaxNumberLength)
	}
	d, problem := readDecimal(text)
	if problem != "" {
		return problem
	}
	if d.exp > MaxNumberExponent || d.exp < -MaxNumberExponent {
		return fmt.Sprintf("a number has the exponent %d (its digits without trailing zeros, times a power of ten); the limit is %d either way (a larger or smaller number cannot be read exactly)", d.exp, MaxNumberExponent)
	}
	return ""
}

// canonicalNumber returns the canonical form of a number that numberProblem accepts.
func canonicalNumber(text string) string {
	d, _ := readDecimal(text)
	if d.digits == "" {
		return "0"
	}
	sign := ""
	if d.neg {
		sign = "-"
	}
	n := len(d.digits)
	switch {
	case d.exp >= 0 && n+d.exp <= MaxNumberLength:
		return sign + d.digits + strings.Repeat("0", d.exp)
	case d.exp < 0 && n+1 <= MaxNumberLength && -d.exp < n:
		return sign + d.digits[:n+d.exp] + "." + d.digits[n+d.exp:]
	case d.exp < 0 && -d.exp+2 <= MaxNumberLength && -d.exp >= n:
		return sign + "0." + strings.Repeat("0", -d.exp-n) + d.digits
	}
	return sign + d.digits + "e" + strconv.Itoa(d.exp)
}

// isIntegerNumber reports whether a canonical number is a whole number: it has no
// point and no negative exponent.
func isIntegerNumber(canonical string) bool {
	return !strings.Contains(canonical, ".") && !strings.Contains(canonical, "e-")
}
