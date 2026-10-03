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
//     and lints clean under the same name.
package fuzz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

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
	f.Add([]byte("entity \"A\" {\n  key = a[*][*][*][*]\n}\n"))
	f.Add([]byte("x = <<EOT\n%{\nif x}%{/**/if y}\nEOT\n"))
	f.Add([]byte("x = a::b() + c.d[0] ? (1) : [for a in b : a]\n"))
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
	f.Fuzz(func(t *testing.T, src []byte) {
		m, parse := modelspec.ParseHCL("fuzz.modelspec.hcl", src)
		if !refusal(t, m, parse) || len(m.Unmapped) > 0 {
			return
		}
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
}

func FuzzJSON(f *testing.F) {
	enabled(f)
	seeds(f, "json", ".modelspec.json")
	f.Fuzz(func(t *testing.T, src []byte) {
		m, parse := modelspec.ParseJSON("fuzz.modelspec.json", src)
		refusal(t, m, parse)
	})
}
