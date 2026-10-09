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
	RuleUnknown    = "unknown-field" // a top-level JSON field the format does not define
	RuleIO         = "unreadable"    // a file found by a directory search that cannot be read
	RuleSkipped    = "skipped-file"  // a model-named link, pipe or device a search does not read: an error, and its module is not checked
	RuleStaleTwin  = "stale-twin"    // a JSON twin that is not what its HCL exports to
	RuleNameCase   = "name-case"     // names in one scope that differ only by case

	RuleDeprecated   = "deprecated-spelling" // the old spelling of record, field and record = (decisions 0018, 0020, 0022)
	RuleRemoved      = "removed-construct"   // a collection or a recordset (decision 0019)
	RuleReservedWord = "reserved-word"       // projection, index or migration: reserved, no content yet (decision 0019)

	RulePublishModuleName      = "publish-module-name"
	RulePublishRecords         = "publish-records"
	RulePublishFields          = "publish-fields"
	RulePublishNameForm        = "publish-name-form"
	RulePublishComponentField  = "publish-component-field"
	RulePublishQualifiedRecord = "publish-qualified-record"
)

// OldSpellingSeverity is the severity of the deprecated-spelling finding for a file,
// and the one place in the code that decides it: an error in a file that is being
// checked, a warning in a file that is only read so that references into its module
// resolve (referenceOnly is Model.ReferenceOnly).
//
// The staged rename (decision 0022) ends with the old spelling an error: a model
// that is being written or registered must not be in it. A model that another
// refers to, pinned at a past commit, keeps its old spelling and stays readable
// (decisions 0018 and 0021), and a person checking the model that refers to it must
// not be failed by it: that is the one exception, and it is a warning.
// modelspec rewrite does not look at this function, because it must read old files
// to rewrite them.
//
// What pins the rule: TestDeprecatedSpelling (check_test.go) for the library, the
// lint, export and rewrite tests in internal/cli, the corpus test that pairs each
// old item with its copy in the new spelling (oldSpellingVerdict), and the
// manifest's old items with the differences from the recorded readers that follow.
func OldSpellingSeverity(referenceOnly bool) Severity {
	if referenceOnly {
		return SeverityWarning
	}
	return SeverityError
}

// Finding is one located problem in one file.
type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"` // 0 when the position is not known
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	// cut is set on the finding that says how many others a full list did not keep:
	// when a later list drops that finding too, it counts for all of them.
	cut cutCount
}

// cutCount is what a cut list left out: findings, of them errors, of them skipped files.
type cutCount struct{ n, errors, skipped int }

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
	skipped     int // of those dropped, the skipped-file findings
	evictWarn   int // every finding of list before this one is an error or worse
	evictError  int // every finding of list before this one is a skipped-file finding
}

// rank orders the findings the list keeps when it is full: a warning, an error, and
// a skipped-file error, which says that a module was not checked and must not be
// dropped for lack of room before any other finding is.
func rank(f Finding) int {
	switch {
	case f.Rule == RuleSkipped:
		return 2
	case f.Severity == SeverityError:
		return 1
	}
	return 0
}

// replace puts f in the place of the first finding of list from *from on whose rank
// is below r, and returns the one it replaced; it reports whether there was one.
func (l *findingList) replace(f Finding, r int, from *int) (Finding, bool) {
	for ; *from < len(l.list); *from++ {
		if rank(l.list[*from]) < r {
			f, l.list[*from] = l.list[*from], f
			*from++
			return f, true
		}
	}
	return f, false
}

// put keeps the finding, or counts it when the list is full. Every message is cut
// to MaxMessageBytes here, the one place all findings pass, so that no finding is
// larger than that however large the input is. When the list is full an error
// takes the place of a warning, so that errors are listed first, and a skipped-file
// error takes the place of a warning and then of any other error; one is dropped
// only when MaxFindings of its own rank or better are already there.
func (l *findingList) put(f Finding) {
	f.Message = clipText(f.Message, MaxMessageBytes)
	if len(l.list) < MaxFindings {
		l.list = append(l.list, f)
		return
	}
	switch r := rank(f); {
	case r == 1:
		f, _ = l.replace(f, 1, &l.evictWarn)
	case r == 2:
		var ok bool
		if f, ok = l.replace(f, 1, &l.evictWarn); !ok {
			f, _ = l.replace(f, 2, &l.evictError)
		}
	}
	if l.count == 0 {
		l.droppedFile = f.File
	}
	switch {
	case f.cut.n > 0: // the line that counts a cut list: it stands for the findings that list left out
		l.count += f.cut.n
		l.errors += f.cut.errors
		l.skipped += f.cut.skipped
	default:
		l.count++
		if f.Severity == SeverityError {
			l.errors++
		}
		if f.Rule == RuleSkipped {
			l.skipped++
		}
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
	message := fmt.Sprintf("%d more findings (%d errors, %d warnings) are not listed: at most %d are kept for one file or check, and for one run", l.count, l.errors, l.count-l.errors, MaxFindings)
	if l.skipped > 0 {
		message += fmt.Sprintf("; %d of them are skipped-file errors: model files that were not read, and their modules were not checked", l.skipped)
	}
	return append(l.list, Finding{File: l.droppedFile, Rule: RuleLimit, Severity: severity, Message: message, cut: cutCount{l.count, l.errors, l.skipped}})
}

// capFindings applies the limit to a whole run's findings, sorted: the first
// MaxFindings are kept, and a finding says how many others there were. A list of
// one more than the limit is what one cut reader or check gives (the findings and
// the one that counts the rest), and is left as it is. When several files add up
// to more, a finding that already stands for a cut list counts for all that list
// left out, whether it is dropped or another finding takes its place.
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
