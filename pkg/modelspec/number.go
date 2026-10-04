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
// Every number within the limits has a canonical form within them: its exponent is
// the one the limit is on, and it has at most MaxNumberLength characters, because
// the plain decimal is used only when it fits and the form with an exponent is not
// longer than the number as it was written. The canonical form can be longer than
// the text it came from, though: 0.5e36 is written in 6 characters and is 36 digits.

// decimal is a number as digits times ten to the exp: digits have no leading or
// trailing zero, and are empty for zero.
type decimal struct {
	neg    bool
	digits string
	exp    int
}

// readDecimal reads the text of a number: digits with an optional point, an
// optional exponent, an optional leading minus sign. It returns a problem, as a
// message, when the exponent is too large to be an integer. Zero has no digits
// and keeps the exponent it was written with (the limit is on that, so `0e2147483648`
// and `0e99999999999999999999` are refused alike). It does not check the limits.
func readDecimal(text string) (d decimal, problem string) {
	d.neg = strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	exponent := ""
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent, text = text[i+1:], text[:i]
	}
	whole, fraction, _ := strings.Cut(text, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	trimmed := strings.TrimRight(digits, "0")
	exp := 0
	if exponent != "" {
		var err error
		if exp, err = strconv.Atoi(exponent); err != nil {
			return d, fmt.Sprintf("a number has the exponent %s, which is too large; the limit is %d either way", exponent, MaxNumberExponent)
		}
	}
	if trimmed == "" {
		return decimal{exp: exp}, ""
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
		if d.digits == "" {
			return fmt.Sprintf("the number %s is zero, with the exponent %d past the limit of %d either way", text, d.exp, MaxNumberExponent)
		}
		sign := ""
		if d.neg {
			sign = "-"
		}
		return fmt.Sprintf("the number %s is %s%se%d, whose exponent %d is past the limit of %d either way (the digits without trailing zeros, times a power of ten): a larger or smaller number cannot be read exactly", text, sign, d.digits, d.exp, d.exp, MaxNumberExponent)
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
