package modelspec

import (
	"errors"
	"fmt"
)

// Rewrite brings a model file from the old spelling to the new one (decisions
// 0018, 0020 and 0022): in HCL the block types entity and property and the
// attribute name entity, in JSON the format identifier and the keys entities,
// properties and entity. It returns the new source and the number of
// replacements, which is zero (and the source, as it was) for a file that has no
// old spelling.
//
// It edits byte ranges: every other byte of the file is as it was, so comments,
// blank lines, alignment, key order and line endings survive, and a file already
// in the new spelling comes back unchanged. The old spelling is what the readers
// recorded (Model.Old), so the count is the one the deprecation finding gives.
//
// A file it cannot rewrite safely is refused, with the reason: one that does not
// parse, one that holds a construct no rewriting fixes (a removed construct, a
// reserved word) or mixes the two vocabularies. A file with other mistakes is
// rewritten; the rewrite is syntactic. Before it returns, the rewritten source is
// read again and must be the same model with no old spelling left, or the file
// is refused.
func Rewrite(file string, src []byte) ([]byte, int, error) {
	return rewriteWith(file, src, applyEdits)
}

// rewriteWith is Rewrite with the edit step given, so that a test can make it
// wrong and see the check refuse the result.
func rewriteWith(file string, src []byte, apply func([]byte, []OldSpelling) []byte) ([]byte, int, error) {
	m, findings := Parse(file, src)
	if m.Broken {
		return nil, 0, fmt.Errorf("does not parse (%s)", findings[0].Message)
	}
	if m.cannotRewrite != "" {
		return nil, 0, errors.New(m.cannotRewrite)
	}
	if len(m.Old) == 0 {
		return src, 0, nil
	}
	out := apply(src, m.Old)
	if len(out) > MaxInputBytes {
		return nil, 0, fmt.Errorf("the rewritten file would be %d bytes, over the limit of %d bytes that every file this tool reads has; nothing was changed", len(out), MaxInputBytes)
	}
	again, againFindings := Parse(file, out)
	if again.Broken || len(again.Old) > 0 || sameModel(m, again) != "" || !sameFindings(findings, againFindings) {
		return nil, 0, errors.New("the rewritten file does not read as the same model, so nothing was changed; this is a defect of modelspec rewrite, please report it with the file")
	}
	return out, len(m.Old), nil
}

// applyEdits replaces each old spelling by its new one. The edits do not overlap
// and are in source order.
func applyEdits(src []byte, edits []OldSpelling) []byte {
	out := make([]byte, 0, len(src))
	last := 0
	for _, e := range edits {
		out = append(out, src[last:e.Start]...)
		out = append(out, e.New...)
		last = e.End
	}
	return append(out, src[last:]...)
}

// sameModel returns "" when two models hold the same concepts, members and
// attributes, with the same names, values and lines, and otherwise what differs.
func sameModel(a, b *Model) string {
	if a.Name != b.Name || a.Form != b.Form || (a.Module == nil) != (b.Module == nil) || (a.Module != nil && *a.Module != *b.Module) {
		return "the module"
	}
	if len(a.Concepts) != len(b.Concepts) {
		return "the number of concepts"
	}
	for i, ca := range a.Concepts {
		cb := b.Concepts[i]
		if ca.Kind != cb.Kind || ca.Name != cb.Name || ca.Line != cb.Line || len(ca.Members) != len(cb.Members) {
			return fmt.Sprintf("concept %q", ca.Name)
		}
		if !sameAttrs(ca.Attrs, cb.Attrs) {
			return fmt.Sprintf("the attributes of %q", ca.Name)
		}
		for j, ma := range ca.Members {
			if mb := cb.Members[j]; ma.Name != mb.Name || ma.Line != mb.Line || !sameAttrs(ma.Attrs, mb.Attrs) {
				return fmt.Sprintf("member %q of %q", ma.Name, ca.Name)
			}
		}
	}
	return ""
}

func sameAttrs(a, b []Attr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Line != b[i].Line || Diff(a[i].Value, b[i].Value) != "" {
			return false
		}
	}
	return true
}

// sameFindings reports whether two lists of findings from reading a file are the
// same problems in the same places, in any order. The wording is not compared: a
// message may name the spelling the source used.
func sameFindings(a, b []Finding) bool {
	if len(a) != len(b) {
		return false
	}
	count := map[string]int{}
	for i := range a {
		count[fmt.Sprintf("%d %s %s", a[i].Line, a[i].Rule, a[i].Severity)]++
		count[fmt.Sprintf("%d %s %s", b[i].Line, b[i].Rule, b[i].Severity)]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}
