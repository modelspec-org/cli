package modelspec

import (
	"fmt"
	"sort"
)

// Severity ranks a finding. Only SeverityError makes a model invalid.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Rule ids. They are stable: scripts may match on them.
const (
	RuleSyntax      = "syntax"        // the source is not valid HCL or JSON
	RuleShape       = "shape"         // valid syntax, but not the structure ModelSpec defines
	RuleLiteral     = "literal"       // an HCL value that is not a literal (decision 0009)
	RuleReference   = "reference"     // a name that does not resolve (decisions 0014, 0013)
	RuleReserved    = "reserved-name" // a reserved kind token used as a concept name (decision 0015)
	RuleDuplicate   = "duplicate-name"
	RuleNameForm    = "name-form"
	RuleEnumValues  = "enum-values"
	RuleType        = "unknown-type"
	RuleAttribute   = "attribute"
	RuleKey         = "key"
	RuleMemberKind  = "member-kind"
	RuleVersion     = "modelspec-version"
	RuleModule      = "module"
	RuleEntities    = "entities"
	RuleCollection  = "collection"
	RuleConsumerGap = "unsupported-by-consumers"
)

// Finding is one located problem in one file.
type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"` // 0 when the position is not known
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// String formats a finding as "file:line: severity: message [rule]".
func (f Finding) String() string {
	pos := f.File
	if f.Line > 0 {
		pos = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	return fmt.Sprintf("%s: %s: %s [%s]", pos, f.Severity, f.Message, f.Rule)
}

// SortFindings orders findings by file, line, rule and message, so output is
// deterministic.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
}

// HasErrors reports whether any finding has error severity.
func HasErrors(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}
