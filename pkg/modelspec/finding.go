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

// Rule ids. They are stable: scripts may match on them. Rules whose id starts
// with "publish-" are enforced only under the publish profile.
const (
	RuleSyntax     = "syntax"        // the source is not valid HCL or JSON
	RuleEncoding   = "encoding"      // the source is not valid UTF-8
	RuleLimit      = "limit"         // the source exceeds a size or nesting limit
	RuleShape      = "shape"         // valid syntax, but not the structure ModelSpec defines
	RuleLiteral    = "literal"       // an HCL value that is not a literal (decision 0009)
	RuleReference  = "reference"     // a name that does not resolve (decisions 0014, 0013)
	RuleReserved   = "reserved-name" // a reserved kind token used as a concept name (decision 0015)
	RuleDuplicate  = "duplicate-name"
	RuleNameForm   = "name-form" // a dot in a concept name (decision 0014)
	RuleEnumValues = "enum-values"
	RuleType       = "unknown-type"
	RuleAttribute  = "attribute"
	RuleKey        = "key"
	RuleMemberKind = "member-kind"
	RuleVersion    = "modelspec-version"
	RuleModule     = "module"
	RuleCollection = "collection"
	RuleUnknown    = "unknown-field" // a top-level JSON field the format does not define
	RuleIO         = "unreadable"    // a file found by a directory search that cannot be read
	RuleSkipped    = "skipped-file"  // a model-named link, pipe or device a search does not read: an error, and its module is not checked
	RuleStaleTwin  = "stale-twin"    // a JSON twin that is not what its HCL exports to
	RuleNameCase   = "name-case"     // names in one scope that differ only by case

	RulePublishModuleName = "publish-module-name"
	RulePublishEntities   = "publish-entities"
	RulePublishProperties = "publish-properties"
	RulePublishNameForm   = "publish-name-form"
	RulePublishComponent  = "publish-component-property"
	RulePublishQualified  = "publish-qualified-entity"
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

// MaxFindings is the most findings one reader, one check and one run keep. A file
// of four megabytes can hold millions of mistakes, and listing them all would use
// memory and output without end; past the limit the findings are counted, and one
// finding says how many were left out. The count and the severity of that finding
// are the whole of what was dropped, so a run that dropped an error still has
// one, and its exit code is the same as with no limit.
const MaxFindings = 1000

// findingList collects findings, keeping at most MaxFindings.
type findingList struct {
	list        []Finding
	droppedFile string // the file of the first finding dropped
	count       int
	errors      int
	evict       int // every finding of list before this one is an error
}

// put keeps the finding, or counts it when the list is full. Every message is cut
// to MaxMessageBytes here, the one place all findings pass, so that no finding is
// larger than that however large the input is. When the list is full an error
// takes the place of a warning, so that errors are listed first; one is dropped
// only when MaxFindings errors are already there.
func (l *findingList) put(f Finding) {
	f.Message = clipText(f.Message, MaxMessageBytes)
	if len(l.list) < MaxFindings {
		l.list = append(l.list, f)
		return
	}
	if f.Severity == SeverityError {
		for ; l.evict < len(l.list); l.evict++ {
			if l.list[l.evict].Severity != SeverityError {
				f, l.list[l.evict] = l.list[l.evict], f
				l.evict++
				break
			}
		}
	}
	if l.count == 0 {
		l.droppedFile = f.File
	}
	l.count++
	if f.Severity == SeverityError {
		l.errors++
	}
}

// result returns the findings, sorted, and after them the finding that says how
// many were not kept, if any were.
func (l *findingList) result() []Finding {
	SortFindings(l.list)
	if l.count == 0 {
		return l.list
	}
	severity := SeverityWarning
	if l.errors > 0 {
		severity = SeverityError
	}
	return append(l.list, Finding{File: l.droppedFile, Rule: RuleLimit, Severity: severity, Message: fmt.Sprintf("%d more findings (%d errors, %d warnings) are not listed: at most %d are kept for one file or check, and for one run", l.count, l.errors, l.count-l.errors, MaxFindings)})
}

// capFindings applies the limit to a whole run's findings, sorted: the first
// MaxFindings are kept, and a finding says how many others there were. A list of
// one more than the limit is what one cut reader or check gives (the findings and
// the one that counts the rest), and is left as it is. When several files add up
// to more, a finding that already stands for a cut list counts as one.
func capFindings(fs []Finding) []Finding {
	if len(fs) <= MaxFindings+1 {
		return fs
	}
	var l findingList
	for _, f := range fs {
		l.put(f)
	}
	return l.result()
}
