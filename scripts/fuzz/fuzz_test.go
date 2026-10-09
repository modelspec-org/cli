// Package fuzz holds the fuzz targets for the HCL and JSON readers. They are not
// part of the default test run (they skip unless MODELSPEC_FUZZ is set) and,
// being test code, are not part of the coverage the gate counts. Run them with
// scripts/fuzz.sh.
//
// Three oracles apply to every input:
//
//  1. the reader and the checker do not panic, and the process survives
//     (a stack overflow in the HCL parser is fatal and fails the run);
//  2. the publish profile refuses whatever the default profile refuses;
//  3. a model that lints clean exports (HCL) to JSON that parses, round-trips
//     and lints clean under the same name;
//  4. a model that lints clean is rewritten (modelspec rewrite) to the same model,
//     which lints clean, and rewriting that again changes nothing;
//  5. one input costs a bounded amount of work: the bytes allocated reading and
//     checking it, exporting it and reading the export back (FuzzHCL does all of
//     that inside within) stay within allocBase plus allocPerByte for each byte of
//     the input (a count of the work done, which does not depend on the load of the
//     machine, as time does), and no input is still running after ten seconds (a
//     watchdog stops the process while the input runs). An input over either fails
//     the run, as a crash does; a fuzzer that is only given a time limit reports a
//     pass after an input that stalled it. A run that stalls as a whole is judged
//     by scripts/fuzz.sh (internal/fuzzjudge).
package fuzz

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

// The budget of one input. Reading and checking a large valid file (under both
// profiles, as refusal does) allocates about 470 times its size; the budget is twice
// that, and a fixed amount for the smallest inputs.
const (
	allocBase    = 4 << 20
	allocPerByte = 1000
	maxTime      = 10 * time.Second
)

// guard runs work and calls stalled, from another goroutine, if work has not
// returned within limit: the input is stopped while it runs, not judged after it
// returns, which an input that never returns would escape.
func guard(limit time.Duration, stalled func(), work func()) {
	timer := time.AfterFunc(limit, stalled)
	defer timer.Stop()
	work()
}

// within runs the work for one input and fails when it cost more than the budget.
// An input still running after maxTime ends the process, which the fuzzer reports
// as a failing input and keeps.
func within(t *testing.T, src []byte, work func()) {
	t.Helper()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	guard(maxTime, func() {
		panic(fmt.Sprintf("an input of %d bytes is still running after %v", len(src), maxTime))
	}, work)
	runtime.ReadMemStats(&after)
	if used, limit := after.TotalAlloc-before.TotalAlloc, uint64(allocBase+allocPerByte*len(src)); used > limit {
		t.Fatalf("an input of %d bytes allocated %d bytes, over the budget of %d", len(src), used, limit)
	}
}

func enabled(f *testing.F) {
	f.Helper()
	if os.Getenv("MODELSPEC_FUZZ") == "" {
		f.Skip("fuzz targets run with scripts/fuzz.sh (MODELSPEC_FUZZ=1)")
	}
}

func seeds(f *testing.F, dir, suffix string) {
	f.Helper()
	root := filepath.Join("..", "..", "testdata", "corpus", dir)
	entries, err := os.ReadDir(root)
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(""))
	f.Add([]byte("[[[[[[[[[[[[[[[["))
	f.Add([]byte("x = <<EOT\n\"\nEOT\nkey = [[[[\n"))
	f.Add([]byte("x = -------------1"))
	f.Add([]byte("record \"A\" {\n  key = a[*][*][*][*]\n}\n"))
	f.Add([]byte("x = <<EOT\n%{\nif x}%{/**/if y}\nEOT\n"))
	f.Add([]byte("x = a::b() + c.d[0] ? (1) : [for a in b : a]\n"))
	// Inputs that were once slow or large: numbers with a huge exponent or many
	// digits, and long names.
	f.Add([]byte("enum \"E\" {\n  values = [1e10000000]\n}\n"))
	f.Add([]byte("record \"A\" {\n  key = []\n  max_len = 1e4000000\n}\n"))
	f.Add([]byte("{\"modelspec\": \"1.0-draft\", \"enums\": {\"E\": {\"values\": [1e10000000, 1" + strings.Repeat("0", 400) + "]}}}"))
	f.Add([]byte("record \"" + strings.Repeat("A", 3000) + "\" {\n}\n"))
	// The old spelling, the removed constructs and the reserved words.
	f.Add([]byte("entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"r\" {\n    entity = \"A\"\n  }\n}\n"))
	f.Add([]byte("record \"A\" {\n  field \"r\" {\n    entity = \"A\"\n    record = \"A\"\n  }\n  index \"i\" {\n  }\n}\ncollection \"c\" {\n}\nprojection \"p\" {\n}\n"))
	f.Add([]byte("{\"modelspec\": \"1.0-draft\", \"module\": {\"id\": \"x\", \"version\": \"1\"}, \"entities\": {\"A\": {\"properties\": {\"p\": {\"entity\": \"A\"}}}}, \"records\": {}, \"collections\": {}, \"projections\": {}}"))
	f.Add([]byte("{\"modelspec\": \"1.0-draft-2\", \"module\": {\"id\": \"x\", \"version\": \"1\"}, \"entities\": {\"A\": {\"properties\": {\"p\": {\"entity\": \"A\"}}}}}"))
}

// rewritten checks the fourth oracle for a source that lints clean: it is rewritten
// to a source that reads as clean, and rewriting that changes nothing.
func rewritten(t *testing.T, file string, src []byte) {
	t.Helper()
	out, _, err := modelspec.Rewrite(file, src)
	if err != nil {
		t.Fatalf("a clean model is not rewritten: %v", err)
	}
	m, parse := modelspec.Parse(file, out)
	if !refusal(t, m, parse) {
		t.Fatalf("the rewrite of a clean model does not lint clean:\n%s", out)
	}
	again, n, err := modelspec.Rewrite(file, out)
	if err != nil || n != 0 || !bytes.Equal(again, out) {
		t.Fatalf("rewriting the rewrite changed it (%d replacements, %v):\n%s", n, err, out)
	}
}

func lint(m *modelspec.Model, parse []modelspec.Finding, p modelspec.Profile) []modelspec.Finding {
	return append(append([]modelspec.Finding(nil), parse...), modelspec.Check([]*modelspec.Model{m}, modelspec.Options{Profile: p})...)
}

func refusal(t *testing.T, m *modelspec.Model, parse []modelspec.Finding) (clean bool) {
	t.Helper()
	def := lint(m, parse, modelspec.ProfileDefault)
	pub := lint(m, parse, modelspec.ProfilePublish)
	if modelspec.HasErrors(def) && !modelspec.HasErrors(pub) {
		t.Fatalf("the default profile refuses and publish accepts:\n%v", def)
	}
	return !modelspec.HasErrors(def)
}

func FuzzHCL(f *testing.F) {
	enabled(f)
	seeds(f, "hcl", ".modelspec.hcl")
	seeds(f, "new/hcl", ".modelspec.hcl")
	f.Fuzz(func(t *testing.T, src []byte) {
		// Everything one input costs runs inside within: the reading and the checks, the
		// export, the read of the export and the comparison, so the watchdog and the
		// allocation budget cover all of it.
		within(t, src, func() {
			m, parse := modelspec.ParseHCL("fuzz.modelspec.hcl", src)
			if !refusal(t, m, parse) {
				return
			}
			rewritten(t, "fuzz.modelspec.hcl", src)
			node, err := m.JSON(modelspec.ModuleIdentity{ID: "x/fuzz", Name: "fuzz", Version: "1"})
			if err != nil {
				t.Fatalf("a clean model does not export: %v", err)
			}
			back, backParse := modelspec.ParseJSON("fuzz.modelspec.json", node.Encode())
			if !refusal(t, back, backParse) {
				t.Fatalf("the export of a clean model does not lint clean:\n%v\n%s", lint(back, backParse, modelspec.ProfileDefault), node.Encode())
			}
			again, err := modelspec.ParseNode(node.Encode())
			if err != nil || modelspec.Diff(node, again) != "" {
				t.Fatalf("the export does not round-trip: %v %s", err, modelspec.Diff(node, again))
			}
		})
	})
}

func FuzzJSON(f *testing.F) {
	enabled(f)
	seeds(f, "json", ".modelspec.json")
	seeds(f, "new/json", ".modelspec.json")
	f.Fuzz(func(t *testing.T, src []byte) {
		within(t, src, func() {
			m, parse := modelspec.ParseJSON("fuzz.modelspec.json", src)
			if refusal(t, m, parse) {
				rewritten(t, "fuzz.modelspec.json", src)
			}
		})
	})
}
