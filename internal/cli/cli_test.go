package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelspec-org/cli/pkg/modelspec"
	"github.com/strongo/buildinfo"
	"github.com/strongo/cli-helpers/selfupdate"
)

// memFS is an in-memory modelspec.FS.
type memFS struct {
	fstest.MapFS
	written  map[string][]byte
	perms    map[string]fs.FileMode
	writeErr error
	listErr  error // makes ReadDir of a directory that has models in it fail
}

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if m.listErr != nil && strings.HasSuffix(filepath.ToSlash(name), "/models") {
		return nil, m.listErr
	}
	return m.MapFS.ReadDir(name)
}

func (m *memFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	if info, err := m.MapFS.Stat(strings.TrimPrefix(filepath.Clean(name), "/")); err == nil && info.IsDir() {
		return &fs.PathError{Op: "open", Path: name, Err: errors.New("is a directory")}
	}
	m.written[name] = data
	m.perms[name] = perm
	return nil
}

func (m *memFS) Abs(name string) (string, error) {
	return strings.TrimPrefix(filepath.Clean(name), "/"), nil
}

// pathInfo remembers which path it describes, so that SameFile can tell.
type pathInfo struct {
	fs.FileInfo
	path string
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	info, err := m.MapFS.Stat(strings.TrimPrefix(filepath.Clean(name), "/"))
	if err != nil {
		return nil, err
	}
	return pathInfo{info, filepath.Clean(name)}, nil
}

func (m *memFS) SameFile(a, b fs.FileInfo) bool {
	pa, aok := a.(pathInfo)
	pb, bok := b.(pathInfo)
	return aok && bok && pa.path == pb.path
}

// roundTripper answers GitHub release requests from a canned body.
type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releases(body string, status int) *http.Client {
	return &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

type harness struct {
	env       *Env
	out, errb *bytes.Buffer
	fsys      *memFS
}

func newHarness(files map[string]string) *harness {
	h := &harness{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, fsys: &memFS{MapFS: fstest.MapFS{}, written: map[string][]byte{}, perms: map[string]fs.FileMode{}}}
	for n, s := range files {
		h.fsys.MapFS[n] = &fstest.MapFile{Data: []byte(s)}
	}
	h.env = &Env{
		Stdout: h.out,
		Stderr: h.errb,
		FS:     h.fsys,
		Build:  buildinfo.Info{Name: "modelspec", Version: "1.2.3", Commit: "abc", Date: "2026-01-01T00:00:00Z", DateSource: buildinfo.DateSourceBuild},
		Update: selfupdate.Config{
			BinaryName:     "modelspec",
			Repository:     "modelspec-org/cli",
			CurrentVersion: "1.2.3",
			HTTPClient:     releases(`[{"tag_name":"v1.2.3"}]`, http.StatusOK),
		},
		Interactive: func() bool { return false },
	}
	return h
}

func (h *harness) run(args ...string) int { return Run(args, h.env) }

const goodHCL = `entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
`

const badHCL = `entity "A" {
  property "id" {
    type = "int"
  }
}
`

const componentHCL = `component "C" {
  field "f" {
    type = "int"
  }
}
entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "c" {
    component = "C"
  }
}
`

const warnHCL = `collection "c" {
  kind = "computed"
}
`

const coreHCL = `entity "Space" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
`

const bookingHCL = `entity "Booking" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "space" {
    entity = "core.Space"
  }
}
`

func layoutPath(module, file string) string {
	return "spec/graph/modules/" + module + "/models/" + file
}

func TestLintText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		files    map[string]string
		args     []string
		wantCode int
		wantOut  string // the whole of standard output
		wantErr  string // a substring of standard error
	}{
		{"clean default path", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint"}, 0, "ok: 1 file checked, 0 errors, 0 warnings\n", ""},
		{"findings", map[string]string{"a.modelspec.hcl": badHCL, "b.modelspec.hcl": goodHCL}, []string{"lint", "."}, 1, "a.modelspec.hcl:1: error: entity \"A\" has no key (a list of the properties that identify a record) [key]\nfailed: 2 files checked, 1 error, 0 warnings\n", ""},
		{"warnings only", map[string]string{"a.modelspec.hcl": warnHCL}, []string{"lint", "a.modelspec.hcl"}, 0, "a.modelspec.hcl:2: warning: collection \"c\" is computed but carries no query (computed collections should) [collection]\nok: 1 file checked, 0 errors, 1 warning\n", ""},
		{"explicit files", map[string]string{"a.modelspec.hcl": goodHCL, "b.modelspec.hcl": badHCL}, []string{"lint", "a.modelspec.hcl"}, 0, "ok: 1 file checked, 0 errors, 0 warnings\n", ""},
		{"a component property is fine by default", map[string]string{"a.modelspec.hcl": componentHCL}, []string{"lint"}, 0, "ok: 1 file checked, 0 errors, 0 warnings\n", ""},
		{"the publish profile refuses it", map[string]string{"a.modelspec.hcl": componentHCL}, []string{"lint", "--profile", "publish"}, 1, "a.modelspec.hcl:12: error: entity \"A\" property \"c\" has a component value; the catalogue lists only scalar and entity-reference properties [publish-component-property]\nfailed: 1 file checked, 1 error, 0 warnings\n", ""},
		{"the default profile can be named", map[string]string{"a.modelspec.hcl": componentHCL}, []string{"lint", "--profile", "default"}, 0, "ok: 1 file checked, 0 errors, 0 warnings\n", ""},
		{"missing path", nil, []string{"lint", "nope"}, 2, "", "modelspec: "},
		{"no model files", map[string]string{"x.txt": ""}, []string{"lint"}, 2, "", "no ModelSpec files"},
		{"bad format", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--format", "xml"}, 2, "", `invalid --format "xml"`},
		{"bad profile", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--profile", "strict"}, 2, "", `unknown profile "strict"`},
		{"bad module flag", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--module", "core"}, 2, "", `invalid --module "core": expected <name>=<path>`},
		{"module flag without a name", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--module", "=x"}, 2, "", `invalid --module "=x"`},
		{"module flag without a path", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--module", "x="}, 2, "", `invalid --module "x="`},
		{"module name with a dot", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--module", "a.b=x"}, 2, "", `invalid --module "a.b=x"`},
		{"module flag for a missing path", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--module", "x=nope"}, 2, "", "modelspec: "},
		{"unknown flag", nil, []string{"lint", "--nope"}, 2, "", "unknown flag: --nope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(tc.files)
			if code := h.run(tc.args...); code != tc.wantCode {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, h.out, h.errb)
			}
			if h.out.String() != tc.wantOut {
				t.Errorf("stdout = %q, want exactly %q", h.out, tc.wantOut)
			}
			if !strings.Contains(h.errb.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want containing %q", h.errb, tc.wantErr)
			}
		})
	}
}

func TestLintModules(t *testing.T) {
	t.Parallel()
	sales := map[string]string{
		layoutPath("sales", "entities.modelspec.hcl"): "entity \"Order\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"s\" {\n    type = \"string\"\n    enum = \"Status\"\n  }\n  property \"c\" {\n    entity = \"core.Space\"\n  }\n}\n",
		layoutPath("sales", "enums.modelspec.hcl"):    "enum \"Status\" {\n  values = [\"open\"]\n}\n",
		layoutPath("core", "model.hcl"):               coreHCL,
	}
	h := newHarness(sales)
	if code := h.run("lint", "spec"); code != 0 || h.out.String() != "ok: 3 files checked, 0 errors, 0 warnings\n" {
		t.Fatalf("layout tree: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
	dup := map[string]string{}
	for k, v := range sales {
		dup[k] = v
	}
	dup[layoutPath("sales", "extra.hcl")] = "enum \"Status\" {\n  values = [\"x\"]\n}\n"
	h = newHarness(dup)
	if code := h.run("lint", "spec"); code != 1 || !strings.Contains(h.out.String(), "extra.hcl:1: error: duplicate concept name \"Status\"") {
		t.Fatalf("cross-file duplicate: exit %d, stdout %q", code, h.out)
	}

	files := map[string]string{"sales.modelspec.hcl": bookingHCL, "shared/core.hcl": coreHCL}
	h = newHarness(files)
	if code := h.run("lint", "sales.modelspec.hcl"); code != 1 || !strings.Contains(h.out.String(), `unknown module "core" (lint the files that declare it together with this one, or name it with --module core=<path>)`) {
		t.Fatalf("unsupplied module: exit %d, stdout %q", code, h.out)
	}
	h = newHarness(files)
	if code := h.run("lint", "sales.modelspec.hcl", "--module", "core=shared/core.hcl"); code != 0 || h.out.String() != "ok: 2 files checked, 0 errors, 0 warnings\n" {
		t.Fatalf("assigned module: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
	h = newHarness(files)
	if code := h.run("lint", "--module", "core=shared"); code != 0 || h.out.String() != "ok: 1 file checked, 0 errors, 0 warnings\n" {
		t.Fatalf("only an assignment: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
}

// A module is the unit: given one file of a layout module, lint checks the whole
// module, reports the sibling's findings with its path, and says so once.
func TestLintChecksTheWholeModule(t *testing.T) {
	t.Parallel()
	enums := "enum \"Status\" {\n  values = [\"open\"]\n}\n"
	tree := map[string]string{
		layoutPath("sales", "entities.hcl"): goodHCL,
		layoutPath("sales", "enums.hcl"):    enums,
		layoutPath("sales", "extra.hcl"):    enums,
	}
	h := newHarness(tree)
	code := h.run("lint", layoutPath("sales", "entities.hcl"))
	out := h.out.String()
	if code != 1 || !strings.Contains(out, "extra.hcl:1: error: duplicate concept name \"Status\"") || !strings.Contains(out, "failed: 3 files checked, 1 error") ||
		!strings.HasPrefix(out, "note: a module is the unit of checking: module \"sales\" has more files in spec/graph/modules/sales/models than were given") || strings.Count(out, "note:") != 1 {
		t.Fatalf("one file of the module: exit %d, stdout %q", code, out)
	}
	// Two files of the same module are one module, checked once.
	h = newHarness(tree)
	code = h.run("lint", layoutPath("sales", "entities.hcl"), layoutPath("sales", "extra.hcl"))
	out = h.out.String()
	if code != 1 || strings.Count(out, "duplicate concept name") != 1 || !strings.Contains(out, "failed: 3 files checked, 1 error") {
		t.Fatalf("two files of the module: exit %d, stdout %q", code, out)
	}
	// A module that is given whole needs no note, and the JSON report carries the notes.
	h = newHarness(tree)
	if code := h.run("lint", "--format", "json", layoutPath("sales", "entities.hcl")); code != 1 || !strings.Contains(h.out.String(), `"notes": [`) || !strings.Contains(h.out.String(), "a module is the unit of checking") {
		t.Fatalf("json: exit %d, stdout %q", code, h.out)
	}
	h = newHarness(tree)
	if code := h.run("lint", "spec"); strings.Contains(h.out.String(), "note:") {
		t.Fatalf("whole module given: exit %d, stdout %q", code, h.out)
	}
	h = newHarness(tree)
	if code := h.run("lint", "--format", "json", "spec"); !strings.Contains(h.out.String(), `"notes": []`) {
		t.Fatalf("whole module given, json: exit %d, stdout %q", code, h.out)
	}
	// Failing to write the note is an I/O failure.
	h = newHarness(tree)
	h.env.Stdout = &failWriter{}
	if code := h.run("lint", layoutPath("sales", "entities.hcl")); code != 2 || !strings.Contains(h.errb.String(), "write failed") {
		t.Fatalf("note write: exit %d, stderr %q", code, h.errb)
	}
}

// A file with a great many mistakes lists a bounded number of them, says how many
// more there were, and exits as it would without the limit.
func TestLintListsAtMostTheLimit(t *testing.T) {
	t.Parallel()
	vals := strings.Repeat(`"v", `, 4000) + `"v"`
	h := newHarness(map[string]string{"a.modelspec.hcl": "enum \"E\" {\n  values = [" + vals + "]\n}\n"})
	if code := h.run("lint"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if len(lines) != modelspec.MaxFindings+2 || !strings.Contains(h.out.String(), "3000 more findings (3000 errors, 0 warnings) are not listed") || !strings.HasPrefix(lines[len(lines)-1], "failed: 1 file checked, 1001 errors") {
		t.Fatalf("%d lines; first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	h = newHarness(map[string]string{"a.modelspec.hcl": "enum \"E\" {\n  values = [" + vals + "]\n}\n"})
	if code := h.run("lint", "--format", "json"); code != 1 || strings.Count(h.out.String(), `"rule": "enum-values"`) != modelspec.MaxFindings {
		t.Fatalf("json: exit %d, %d findings", code, strings.Count(h.out.String(), `"rule": "enum-values"`))
	}
}

func TestLintJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	if code := h.run("lint", "--format", "json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errb)
	}
	var rep struct {
		Files    int
		Errors   int
		Warnings int
		Findings []map[string]any
	}
	if err := json.Unmarshal(h.out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Files != 1 || rep.Findings == nil || len(rep.Findings) != 0 || !strings.Contains(h.out.String(), `"findings": []`) {
		t.Fatalf("report = %s", h.out)
	}

	h = newHarness(map[string]string{"a.modelspec.hcl": badHCL, "w.modelspec.hcl": warnHCL})
	if code := h.run("lint", "--format=json"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if err := json.Unmarshal(h.out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Errors != 1 || rep.Warnings != 1 || len(rep.Findings) != 2 || rep.Findings[0]["file"] != "a.modelspec.hcl" || rep.Findings[0]["rule"] != "key" || rep.Findings[0]["severity"] != "error" || rep.Findings[0]["line"] != float64(1) {
		t.Fatalf("report = %s", h.out)
	}
}

// With --format json, every exit-2 path writes one JSON object on standard
// output, {"error": ..., "exit": 2}, and nothing a script could read as a lint
// report; the message is still on standard error.
func TestJSONOnEveryExit2Path(t *testing.T) {
	t.Parallel()
	files := map[string]string{"a.modelspec.hcl": goodHCL}
	tests := []struct {
		name string
		args []string
		want string // a substring of the error
	}{
		{"missing path", []string{"lint", "--format", "json", "nope"}, "nope"},
		{"missing path, flag after", []string{"lint", "nope", "--format=json"}, "nope"},
		{"no model files", []string{"lint", "--format", "json", "x"}, "no ModelSpec files"},
		{"bad profile", []string{"lint", "--format", "json", "--profile", "strict"}, `unknown profile "strict"`},
		{"bad module", []string{"lint", "--format=json", "--module", "core"}, `invalid --module "core"`},
		{"unknown flag", []string{"lint", "--format", "json", "--nope"}, "unknown flag: --nope"},
		{"unknown command", []string{"nonsense", "--format", "json"}, "unknown command"},
		{"self-update failure", []string{"self-update", "--check", "--format", "json"}, "github releases request failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(files)
			h.fsys.MapFS["x/readme.txt"] = &fstest.MapFile{}
			h.env.Update.HTTPClient = releases("rate limited", http.StatusForbidden)
			if code := h.run(tc.args...); code != 2 {
				t.Fatalf("exit %d", code)
			}
			var got map[string]any
			if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not JSON: %q (%v)", h.out, err)
			}
			if len(got) != 2 || got["exit"] != float64(2) || !strings.Contains(got["error"].(string), tc.want) {
				t.Fatalf("stdout = %s", h.out)
			}
			if !strings.Contains(h.errb.String(), tc.want) {
				t.Errorf("stderr = %q", h.errb)
			}
		})
	}
	// Without --format json there is nothing on standard output; a bad --format value is not json.
	for _, args := range [][]string{{"lint", "nope"}, {"lint", "--format", "xml"}, {"lint", "--format"}} {
		h := newHarness(files)
		if code := h.run(args...); code != 2 || h.out.Len() != 0 {
			t.Errorf("%v: exit %d, stdout %q", args, code, h.out)
		}
	}
	if wantsJSON([]string{"--format"}) || wantsJSON(nil) || !wantsJSON([]string{"x", "--format=json"}) {
		t.Error("wantsJSON wrong")
	}
}

// failWriter fails after n bytes.
type failWriter struct{ left int }

func (w *failWriter) Write(p []byte) (int, error) {
	if w.left < len(p) {
		return 0, errors.New("write failed")
	}
	w.left -= len(p)
	return len(p), nil
}

func TestLintWriteFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		args  []string
	}{
		{"finding line", map[string]string{"a.modelspec.hcl": badHCL}, []string{"lint"}},
		{"summary line", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint"}},
		{"json", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--format", "json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(tc.files)
			h.env.Stdout = &failWriter{}
			if code := h.run(tc.args...); code != 2 || !strings.Contains(h.errb.String(), "write failed") {
				t.Fatalf("exit %d, stderr %q", code, h.errb)
			}
		})
	}
}

var exportID = []string{"--module-id", "x/y", "--module-name", "y", "--module-version", "1"}

func TestExport(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errb)
	}
	if !strings.Contains(h.out.String(), "\"modelspec\": \"1.0-draft\"") || !strings.HasSuffix(h.out.String(), "}\n") {
		t.Fatalf("stdout = %s", h.out)
	}
	stdoutJSON := h.out.String()

	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "a.modelspec.json"}, exportID...)...); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errb)
	}
	if string(h.fsys.written["a.modelspec.json"]) != stdoutJSON || h.out.Len() != 0 {
		t.Fatalf("--out wrote %q, stdout %q", h.fsys.written["a.modelspec.json"], h.out)
	}
	if h.fsys.perms["a.modelspec.json"] != 0o644 {
		t.Fatalf("--out file mode = %o, want 644", h.fsys.perms["a.modelspec.json"])
	}

	// Without a name the module object has none.
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	if code := h.run("export", "a.modelspec.hcl", "--module-id", "x/y", "--module-version", "1"); code != 0 || strings.Contains(h.out.String(), `"name"`) {
		t.Fatalf("export without a name: exit %d, stdout %s, stderr %s", code, h.out, h.errb)
	}

	// The file just written is what the check wants.
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": stdoutJSON})
	if code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json"); code != 0 || h.out.String() != "ok: a.modelspec.json is what a.modelspec.hcl exports to\n" {
		t.Fatalf("check: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
}

func TestExportLintsFirst(t *testing.T) {
	t.Parallel()
	// A file with errors is refused, with its findings shown, and nothing is written.
	h := newHarness(map[string]string{"a.modelspec.hcl": badHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "o.json"}, exportID...)...); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.errb.String(), `a.modelspec.hcl:1: error: entity "A" has no key`) || !strings.Contains(h.errb.String(), "has errors; fix them") || h.out.Len() != 0 || len(h.fsys.written) != 0 {
		t.Fatalf("stderr %q stdout %q written %v", h.errb, h.out, h.fsys.written)
	}
	// --check refuses an invalid model as well: a check must not pass on one.
	good, _ := exportString(t, goodHCL)
	h = newHarness(map[string]string{"a.modelspec.hcl": badHCL, "a.modelspec.json": good})
	if code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json"); code != 1 || h.out.Len() != 0 || !strings.Contains(h.errb.String(), "has errors; fix them") {
		t.Fatalf("check of an invalid model: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
	// A syntax error is a finding too.
	h = newHarness(map[string]string{"a.modelspec.hcl": "entity {"})
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 1 || !strings.Contains(h.errb.String(), "[syntax]") {
		t.Fatalf("syntax error: exit %d, stderr %q", code, h.errb)
	}
	// Warnings are shown and do not stop the export.
	h = newHarness(map[string]string{"a.modelspec.hcl": warnHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 0 || !strings.Contains(h.errb.String(), "warning: collection") {
		t.Fatalf("warning: exit %d, stderr %q", code, h.errb)
	}
	// Findings of other files do not stop it: the context module is broken here.
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "ctx/b.hcl": "entity {"})
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--module", "b=ctx/b.hcl"}, exportID...)...); code != 0 || strings.Contains(h.errb.String(), "ctx/b.hcl") {
		t.Fatalf("context findings: exit %d, stderr %q", code, h.errb)
	}
}

func exportString(t *testing.T, src string) (string, int) {
	t.Helper()
	h := newHarness(map[string]string{"a.modelspec.hcl": src})
	code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...)
	return h.out.String(), code
}

func TestExportModules(t *testing.T) {
	t.Parallel()
	files := map[string]string{"booking.modelspec.hcl": bookingHCL, "shared/core.hcl": coreHCL}
	// A reference to another module needs it supplied.
	h := newHarness(files)
	if code := h.run(append([]string{"export", "booking.modelspec.hcl"}, exportID...)...); code != 1 || !strings.Contains(h.errb.String(), `unknown module "core"`) {
		t.Fatalf("unsupplied: exit %d, stderr %q", code, h.errb)
	}
	h = newHarness(files)
	if code := h.run(append([]string{"export", "booking.modelspec.hcl", "--module", "core=shared/core.hcl"}, exportID...)...); code != 0 || !strings.Contains(h.out.String(), `"entity": "core.Space"`) || strings.Contains(h.out.String(), `"Space": {`) {
		t.Fatalf("supplied: exit %d, stdout %s, stderr %s", code, h.out, h.errb)
	}
	// One file of several in a SpecScore layout module is not exported.
	multi := map[string]string{
		layoutPath("sales", "a.modelspec.hcl"):   goodHCL,
		layoutPath("sales", "b.hcl"):             "enum \"E\" {\n  values = [\"x\"]\n}\n",
		layoutPath("solo", "only.modelspec.hcl"): goodHCL,
	}
	h = newHarness(multi)
	code := h.run(append([]string{"export", layoutPath("sales", "a.modelspec.hcl")}, exportID...)...)
	if code != 1 || !strings.Contains(h.errb.String(), "is one of 2 files of module sales (spec/graph/modules/sales/models/b.hcl)") || h.out.Len() != 0 {
		t.Fatalf("multi-file module: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
	// The other file's name does not matter: the module is the unit.
	h = newHarness(multi)
	code = h.run(append([]string{"export", layoutPath("sales", "b.hcl")}, exportID...)...)
	if code != 1 || !strings.Contains(h.errb.String(), "is one of 2 files of module sales (spec/graph/modules/sales/models/a.modelspec.hcl)") {
		t.Fatalf("multi-file module, other file: exit %d, stderr %q", code, h.errb)
	}
	// A module of one file in the layout is exported.
	h = newHarness(multi)
	if code := h.run(append([]string{"export", layoutPath("solo", "only.modelspec.hcl")}, exportID...)...); code != 0 {
		t.Fatalf("single-file layout module: exit %d, stderr %q", code, h.errb)
	}
	// Any .hcl file of a layout module is exported, whatever it is called, and a
	// JSON copy beside it is not another file of the module.
	copyOfA, _ := exportString(t, goodHCL)
	one := map[string]string{
		layoutPath("solo", "entities.hcl"):            goodHCL,
		layoutPath("solo", "entities.modelspec.json"): copyOfA,
	}
	h = newHarness(one)
	if code := h.run(append([]string{"export", layoutPath("solo", "entities.hcl")}, exportID...)...); code != 0 || !strings.Contains(h.out.String(), `"entities"`) {
		t.Fatalf("a layout .hcl file: exit %d, stdout %s, stderr %q", code, h.out, h.errb)
	}
	// A JSON file is not exported.
	h = newHarness(map[string]string{"a.modelspec.json": "{}"})
	if code := h.run(append([]string{"export", "a.modelspec.json"}, exportID...)...); code != 2 || !strings.Contains(h.errb.String(), "export reads HCL files") {
		t.Fatalf("a JSON file: exit %d, stderr %q", code, h.errb)
	}
}

// A model that refers to its own module by name exports with module.name written
// even when none is given, so the JSON lints clean saved under any file name.
func TestExportOfAModelThatRefersToItself(t *testing.T) {
	t.Parallel()
	self := "entity \"Node\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"parent\" {\n    entity = \"tree.Node\"\n  }\n}\n"
	h := newHarness(map[string]string{"tree.modelspec.hcl": self})
	if code := h.run("export", "tree.modelspec.hcl", "--module-id", "x/tree", "--module-version", "1", "--out", "other.modelspec.json"); code != 0 {
		t.Fatalf("export: exit %d, stderr %q", code, h.errb)
	}
	written := h.fsys.written["other.modelspec.json"]
	if !strings.Contains(string(written), `"name": "tree"`) {
		t.Fatalf("module.name not written:\n%s", written)
	}
	lint := newHarness(map[string]string{"other.modelspec.json": string(written)})
	if code := lint.run("lint"); code != 0 {
		t.Fatalf("lint of the export: exit %d, stdout %q", code, lint.out)
	}
	// Under another name it is refused, with the reason.
	h = newHarness(map[string]string{"tree.modelspec.hcl": self})
	if code := h.run("export", "tree.modelspec.hcl", "--module-id", "x/tree", "--module-version", "1", "--module-name", "forest"); code != 1 || !strings.Contains(h.errb.String(), "module.name must be") {
		t.Fatalf("other name: exit %d, stderr %q", code, h.errb)
	}
}

func TestExportFailures(t *testing.T) {
	t.Parallel()
	good, _ := exportString(t, goodHCL)
	tests := []struct {
		name     string
		files    map[string]string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"no argument", nil, []string{"export"}, 2, "export takes one HCL file"},
		{"two arguments", nil, []string{"export", "a", "b"}, 2, "export takes one HCL file"},
		{"check with one argument", nil, []string{"export", "--check", "a.modelspec.hcl"}, 2, "export --check takes the HCL file and the committed JSON file"},
		{"out with check", map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": good}, []string{"export", "--check", "--out", "x.json", "a.modelspec.hcl", "a.modelspec.json"}, 2, "--out cannot be combined with --check"},
		{"not a model file", map[string]string{"a.json": "{}"}, append([]string{"export", "a.json"}, exportID...), 2, "not a ModelSpec file"},
		{"missing file", nil, append([]string{"export", "a.modelspec.hcl"}, exportID...), 2, "modelspec: "},
		{"bad module flag", map[string]string{"a.modelspec.hcl": goodHCL}, append([]string{"export", "a.modelspec.hcl", "--module", "x"}, exportID...), 2, `invalid --module "x"`},
		{"module flag for a missing path", map[string]string{"a.modelspec.hcl": goodHCL}, append([]string{"export", "a.modelspec.hcl", "--module", "x=nope"}, exportID...), 2, "modelspec: "},
		{"no identity", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"export", "a.modelspec.hcl"}, 1, "supply both"},
		{"unmapped construct", map[string]string{"a.modelspec.hcl": "projection \"p\" {\n}\n"}, append([]string{"export", "a.modelspec.hcl"}, exportID...), 1, "no JSON form is defined"},
		{"check: missing json", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"export", "--check", "a.modelspec.hcl", "a.modelspec.json"}, 2, "modelspec: "},
		{"check: drift", map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": `{"modelspec":"1.0-draft","module":{"id":"x","name":"y","version":"1"},"entities":{}}`}, []string{"export", "--check", "a.modelspec.hcl", "a.modelspec.json"}, 1, "not what a.modelspec.hcl exports to"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(tc.files)
			if code := h.run(tc.args...); code != tc.wantCode {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, h.out, h.errb)
			}
			if h.out.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", h.out)
			}
			if !strings.Contains(h.errb.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want containing %q", h.errb, tc.wantErr)
			}
		})
	}
}

func TestExportIOFailures(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	h.fsys.writeErr = errors.New("disk full")
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "o.json"}, exportID...)...); code != 2 || !strings.Contains(h.errb.String(), "disk full") {
		t.Errorf("--out failure: exit %d, stderr %q", code, h.errb)
	}
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	h.env.Stdout = &failWriter{}
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 2 || !strings.Contains(h.errb.String(), "write failed") {
		t.Errorf("stdout failure: exit %d, stderr %q", code, h.errb)
	}
	// A check whose ok message cannot be written.
	good, _ := exportString(t, goodHCL)
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": good})
	h.env.Stdout = &failWriter{}
	if code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json"); code != 2 {
		t.Errorf("check message failure: exit %d", code)
	}
	// A layout module whose directory cannot be listed.
	h = newHarness(map[string]string{layoutPath("sales", "a.modelspec.hcl"): goodHCL})
	h.fsys.listErr = errors.New("cannot list")
	if code := h.run(append([]string{"export", layoutPath("sales", "a.modelspec.hcl")}, exportID...)...); code != 2 || !strings.Contains(h.errb.String(), "cannot list") {
		t.Errorf("layout listing failure: exit %d, stderr %q", code, h.errb)
	}
}

// Every command with every kind of wrong path: a directory where a file is
// wanted, a missing path, an empty string, a path given twice. None may panic, and
// each is a usage or I/O error (exit 2), or a plain success where the command can
// do what was asked; with --format json the exit-2 cases write the JSON error.
func TestWrongPaths(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"a.modelspec.hcl":       goodHCL,
		"d/b.modelspec.hcl":     goodHCL,
		"empty/readme.txt":      "",
		"c.modelspec.json":      "{}",
		"shared/core.hcl":       coreHCL,
		"booking.modelspec.hcl": bookingHCL,
	}
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"lint an empty string", []string{"lint", ""}, 2, "modelspec: "},
		{"lint a missing path", []string{"lint", "nope"}, 2, "nope"},
		{"lint a directory with no models", []string{"lint", "empty"}, 2, "no ModelSpec files"},
		{"lint a file twice", []string{"lint", "a.modelspec.hcl", "a.modelspec.hcl"}, 0, ""},
		{"lint a file and its directory", []string{"lint", "d", "d/b.modelspec.hcl", "d"}, 0, ""},
		{"lint a non-model file", []string{"lint", "empty/readme.txt"}, 2, "not a ModelSpec file"},
		{"lint --module with an empty path", []string{"lint", "--module", "core=", "a.modelspec.hcl"}, 2, "modelspec: "},
		{"lint --module with an empty name", []string{"lint", "--module", "=shared", "a.modelspec.hcl"}, 2, "invalid --module"},
		{"lint --module with a missing path", []string{"lint", "--module", "core=nope", "a.modelspec.hcl"}, 2, "nope"},
		{"lint --module with a path twice", []string{"lint", "--module", "core=shared", "--module", "core=shared/core.hcl", "a.modelspec.hcl"}, 0, ""},
		{"lint --module, one path as two modules", []string{"lint", "--module", "x=shared", "--module", "y=shared"}, 2, `assigned to module "x" and to module "y"`},
		{"export a directory", append([]string{"export", "d"}, exportID...), 2, "is a directory"},
		{"export the current directory", append([]string{"export", "."}, exportID...), 2, "is a directory"},
		{"export an empty string", append([]string{"export", ""}, exportID...), 2, "modelspec: "},
		{"export a missing file", append([]string{"export", "nope.modelspec.hcl"}, exportID...), 2, "nope"},
		{"export a non-model file", append([]string{"export", "empty/readme.txt"}, exportID...), 2, "not a ModelSpec file"},
		{"export a file twice", append([]string{"export", "a.modelspec.hcl", "a.modelspec.hcl"}, exportID...), 2, "export takes one HCL file"},
		{"export --check a directory", []string{"export", "--check", "d", "c.modelspec.json", "--module-id", "x", "--module-version", "1"}, 2, "is a directory"},
		{"export --check against a directory", []string{"export", "--check", "a.modelspec.hcl", "d"}, 2, "modelspec: "},
		{"export --check against a missing file", []string{"export", "--check", "a.modelspec.hcl", "nope.json"}, 2, "nope.json"},
		{"export --check against an empty string", []string{"export", "--check", "a.modelspec.hcl", ""}, 2, "modelspec: "},
		{"export --check the same file twice", []string{"export", "--check", "a.modelspec.hcl", "a.modelspec.hcl"}, 1, "not valid JSON"},
		{"export --module with an empty path", append([]string{"export", "a.modelspec.hcl", "--module", "core="}, exportID...), 2, "modelspec: "},
		{"export --module naming the file itself", append([]string{"export", "a.modelspec.hcl", "--module", "core=a.modelspec.hcl"}, exportID...), 0, ""},
		{"export --out a directory", append([]string{"export", "a.modelspec.hcl", "--out", "d"}, exportID...), 2, "is a directory"},
	}
	for _, tc := range tests {
		for _, format := range []string{"text", "json"} {
			if format == "json" && tc.args[0] != "lint" {
				continue // only lint has --format
			}
			t.Run(tc.name+" ("+format+")", func(t *testing.T) {
				t.Parallel()
				h := newHarness(files)
				args := tc.args
				if format == "json" && args[0] == "lint" {
					args = append([]string{"lint", "--format", "json"}, args[1:]...)
				}
				code := h.run(args...)
				if code != tc.wantCode {
					t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, h.out, h.errb)
				}
				if !strings.Contains(h.errb.String(), tc.wantErr) {
					t.Errorf("stderr = %q, want containing %q", h.errb, tc.wantErr)
				}
				if format == "json" && tc.wantCode == 2 && !strings.Contains(h.out.String(), `"exit":2`) {
					t.Errorf("--format json: stdout = %q, want the JSON error", h.out)
				}
			})
		}
	}
}

func TestRootCommands(t *testing.T) {
	t.Parallel()
	h := newHarness(nil)
	if code := h.run("--help"); code != 0 || !strings.Contains(h.out.String(), "modelspec validates ModelSpec models") || !strings.Contains(h.out.String(), "self-update") {
		t.Errorf("--help: exit %d, %q", code, h.out)
	}
	h = newHarness(nil)
	if code := h.run("nonsense"); code != 2 || !strings.Contains(h.errb.String(), "unknown command") {
		t.Errorf("unknown command: exit %d, %q", code, h.errb)
	}
	h = newHarness(nil)
	if code := h.run("--nope"); code != 2 || !strings.Contains(h.errb.String(), "unknown flag") {
		t.Errorf("unknown flag: exit %d, %q", code, h.errb)
	}
	h = newHarness(nil)
	if code := h.run("version"); code != 0 || !strings.Contains(h.out.String(), "1.2.3") {
		t.Errorf("version: exit %d, %q", code, h.out)
	}
	h = newHarness(nil)
	if code := h.run("version", "--json"); code != 0 || !strings.Contains(h.out.String(), `"version":"1.2.3"`) {
		t.Errorf("version --json: exit %d, %q", code, h.out)
	}
	h = newHarness(nil)
	if code := h.run("--version"); code != 0 || strings.TrimSpace(h.out.String()) == "" {
		t.Errorf("--version: exit %d, %q", code, h.out)
	}
}

func TestSelfUpdateCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		client   *http.Client
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"up to date", releases(`[{"tag_name":"v1.2.3"}]`, 200), []string{"self-update", "--check"}, 0, "up to date", ""},
		{"update available", releases(`[{"tag_name":"v1.3.0"}]`, 200), []string{"self-update", "--check"}, 10, "1.3.0", ""},
		{"update available as json", releases(`[{"tag_name":"v1.3.0"}]`, 200), []string{"self-update", "--check", "--format", "json"}, 10, `"latest"`, ""},
		{"release lookup fails", releases(`rate limited`, 403), []string{"self-update", "--check"}, 2, "", "github releases request failed"},
		{"transport fails", &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}, []string{"self-update", "--check"}, 2, "", "offline"},
		{"bad format", releases(`[]`, 200), []string{"self-update", "--format", "xml"}, 2, "", "invalid --format"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(nil)
			h.env.Update.HTTPClient = tc.client
			if code := h.run(tc.args...); code != tc.wantCode {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantCode, h.out, h.errb)
			}
			if !strings.Contains(h.out.String(), tc.wantOut) {
				t.Errorf("stdout = %q, want containing %q", h.out, tc.wantOut)
			}
			if !strings.Contains(h.errb.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want containing %q", h.errb, tc.wantErr)
			}
		})
	}
}

func TestExitErrorMessages(t *testing.T) {
	t.Parallel()
	if got := (&exitError{code: 1}).Error(); got != "exit 1" {
		t.Errorf("Error() = %q", got)
	}
	inner := errors.New("inner")
	e := &exitError{code: 2, err: inner}
	if e.Error() != "inner" || !errors.Is(e, inner) {
		t.Errorf("Error()/Unwrap() wrong: %v", e)
	}
}

func TestOSEnv(t *testing.T) {
	t.Parallel()
	env := OSEnv()
	if env.Stdout == nil || env.Stderr == nil || env.FS == nil {
		t.Fatalf("OSEnv = %+v", env)
	}
	if env.Update.BinaryName != "modelspec" || env.Update.Repository != "modelspec-org/cli" || env.Update.CurrentVersion != env.Build.Version || env.Build.Name != "modelspec" {
		t.Fatalf("OSEnv identity = %+v / %+v", env.Update, env.Build)
	}
}
