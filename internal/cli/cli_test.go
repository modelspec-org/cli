package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	reads    []string
	special  map[string]fs.FileMode // files that are not regular: a device, a pipe
	lstatErr map[string]error       // paths that cannot be asked about
	links    map[string]string      // symbolic links (they are also in special) and the files they name
	linkErr  map[string]error       // links that cannot be followed
	openErr  map[string]error       // files that cannot be opened
}

// endless is a file that never ends: what a device is.
type endless struct{ info fs.FileInfo }

func (e endless) Read(p []byte) (int, error) { return len(p), nil }
func (endless) Close() error                 { return nil }
func (e endless) Stat() (fs.FileInfo, error) { return e.info, nil }

func (m *memFS) Open(name string) (fs.File, error) {
	m.reads = append(m.reads, name)
	if err, ok := m.openErr[filepath.Clean(name)]; ok {
		return nil, err
	}
	if _, ok := m.special[filepath.Clean(name)]; ok {
		info, err := m.Stat(name)
		return endless{info}, err
	}
	return m.MapFS.Open(strings.TrimPrefix(filepath.Clean(name), "/"))
}

func (m *memFS) Lstat(name string) (fs.FileInfo, error) {
	if err, ok := m.lstatErr[filepath.Clean(name)]; ok {
		return nil, err
	}
	if _, ok := m.links[filepath.Clean(name)]; ok {
		info, err := fs.Lstat(m.MapFS, strings.TrimPrefix(filepath.Clean(name), "/"))
		if err != nil {
			return nil, err
		}
		return pathInfo{info, filepath.Clean(name), fs.ModeSymlink}, nil
	}
	info, err := fs.Lstat(m.MapFS, strings.TrimPrefix(filepath.Clean(name), "/"))
	if err != nil {
		return nil, err
	}
	return pathInfo{info, filepath.Clean(name), m.special[filepath.Clean(name)]}, nil
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

// EvalSymlinks is the target of a link in links, and the name of anything else.
func (m *memFS) EvalSymlinks(name string) (string, error) {
	if err, ok := m.linkErr[filepath.Clean(name)]; ok {
		return "", err
	}
	if target, ok := m.links[filepath.Clean(name)]; ok {
		return target, nil
	}
	return name, nil
}

func (m *memFS) Abs(name string) (string, error) {
	return strings.TrimPrefix(filepath.Clean(name), "/"), nil
}

// pathInfo remembers which path it describes, so that SameFile can tell.
type pathInfo struct {
	fs.FileInfo
	path string
	mode fs.FileMode // when not zero, the mode of the file
}

func (p pathInfo) Mode() fs.FileMode {
	if p.mode != 0 {
		return p.mode
	}
	return p.FileInfo.Mode()
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	info, err := m.MapFS.Stat(strings.TrimPrefix(filepath.Clean(name), "/"))
	if err != nil {
		return nil, err
	}
	return pathInfo{info, filepath.Clean(name), m.special[filepath.Clean(name)]}, nil
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

const goodHCL = `record "A" {
  key = ["id"]
  field "id" {
    type = "int"
  }
}
`

const badHCL = `record "A" {
  key = []
  field "id" {
    type = "int"
  }
}
`

const componentHCL = `component "C" {
  field "f" {
    type = "int"
  }
}
record "A" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  field "c" {
    component = "C"
  }
}
`

// warnHCL is in the old spelling, which is a warning.
const warnHCL = `entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
`

const coreHCL = `record "Space" {
  key = ["id"]
  field "id" {
    type = "int"
  }
}
`

const bookingHCL = `record "Booking" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  field "space" {
    record = "core.Space"
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
		{"findings", map[string]string{"a.modelspec.hcl": badHCL, "b.modelspec.hcl": goodHCL}, []string{"lint", "."}, 1, "a.modelspec.hcl:2: error: record \"A\" has an empty key [key]\nfailed: 2 files checked, 1 error, 0 warnings\n", ""},
		{"warnings only", map[string]string{"a.modelspec.hcl": warnHCL}, []string{"lint", "a.modelspec.hcl"}, 0, "a.modelspec.hcl:1: warning: holds 2 old spellings: entity, property and entity = are the old spellings of record, field and record = (decision 0018, decision 0020); modelspec rewrite \"a.modelspec.hcl\" rewrites the file [deprecated-spelling]\nok: 1 file checked, 0 errors, 1 warning\n", ""},
		{"explicit files", map[string]string{"a.modelspec.hcl": goodHCL, "b.modelspec.hcl": badHCL}, []string{"lint", "a.modelspec.hcl"}, 0, "ok: 1 file checked, 0 errors, 0 warnings\n", ""},
		{"a component field is fine by default", map[string]string{"a.modelspec.hcl": componentHCL}, []string{"lint"}, 0, "ok: 1 file checked, 0 errors, 0 warnings\n", ""},
		{"the publish profile refuses it", map[string]string{"a.modelspec.hcl": componentHCL}, []string{"lint", "--profile", "publish"}, 1, "a.modelspec.hcl:12: error: record \"A\" field \"c\" has a component value; the catalogue lists only scalar and record-reference fields [publish-component-field]\nfailed: 1 file checked, 1 error, 0 warnings\n", ""},
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
		layoutPath("sales", "records.modelspec.hcl"): "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"s\" {\n    type = \"string\"\n    enum = \"Status\"\n  }\n  field \"c\" {\n    record = \"core.Space\"\n  }\n}\n",
		layoutPath("sales", "enums.modelspec.hcl"):   "enum \"Status\" {\n  values = [\"open\"]\n}\n",
		layoutPath("core", "model.hcl"):              coreHCL,
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
		layoutPath("sales", "records.hcl"): goodHCL,
		layoutPath("sales", "enums.hcl"):   enums,
		layoutPath("sales", "extra.hcl"):   enums,
	}
	h := newHarness(tree)
	code := h.run("lint", layoutPath("sales", "records.hcl"))
	out := h.out.String()
	if code != 1 || !strings.Contains(out, "extra.hcl:1: error: duplicate concept name \"Status\"") || !strings.Contains(out, "failed: 3 files checked, 1 error") ||
		!strings.HasPrefix(out, "note: a module is the unit of checking: module \"sales\" has more files in spec/graph/modules/sales/models than were given") || strings.Count(out, "note:") != 1 {
		t.Fatalf("one file of the module: exit %d, stdout %q", code, out)
	}
	// Two files of the same module are one module, checked once.
	h = newHarness(tree)
	code = h.run("lint", layoutPath("sales", "records.hcl"), layoutPath("sales", "extra.hcl"))
	out = h.out.String()
	if code != 1 || strings.Count(out, "duplicate concept name") != 1 || !strings.Contains(out, "failed: 3 files checked, 1 error") {
		t.Fatalf("two files of the module: exit %d, stdout %q", code, out)
	}
	// A module that is given whole needs no note, and the JSON report carries the notes.
	h = newHarness(tree)
	if code := h.run("lint", "--format", "json", layoutPath("sales", "records.hcl")); code != 1 || !strings.Contains(h.out.String(), `"notes": [`) || !strings.Contains(h.out.String(), "a module is the unit of checking") {
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
	if code := h.run("lint", layoutPath("sales", "records.hcl")); code != 2 || !strings.Contains(h.errb.String(), "write failed") {
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
	if rep.Errors != 1 || rep.Warnings != 1 || len(rep.Findings) != 2 || rep.Findings[0]["file"] != "a.modelspec.hcl" || rep.Findings[0]["rule"] != "key" || rep.Findings[0]["severity"] != "error" || rep.Findings[0]["line"] != float64(2) {
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
	if !strings.Contains(h.out.String(), "\"modelspec\": \"1.0-draft-2\"") || !strings.HasSuffix(h.out.String(), "}\n") {
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

	keyless := "record \"HeapRow\" {\n  field \"value\" {\n    type = \"string\"\n  }\n}\n"
	h = newHarness(map[string]string{"heap.modelspec.hcl": keyless})
	if code := h.run(append([]string{"export", "heap.modelspec.hcl"}, exportID...)...); code != 0 {
		t.Fatalf("export keyless record: exit %d, stderr %s", code, h.errb)
	}
	var exported map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	records := exported["records"].(map[string]any)
	if _, hasKey := records["HeapRow"].(map[string]any)["key"]; hasKey {
		t.Fatalf("keyless record export invented a key: %s", h.out)
	}
}

func TestExportLintsFirst(t *testing.T) {
	t.Parallel()
	// A file with errors is refused, with its findings shown, and nothing is written.
	h := newHarness(map[string]string{"a.modelspec.hcl": badHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "o.json"}, exportID...)...); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.errb.String(), `a.modelspec.hcl:2: error: record "A" has an empty key`) || !strings.Contains(h.errb.String(), "has errors; fix them") || h.out.Len() != 0 || len(h.fsys.written) != 0 {
		t.Fatalf("stderr %q stdout %q written %v", h.errb, h.out, h.fsys.written)
	}
	// --check refuses an invalid model as well: a check must not pass on one.
	good, _ := exportString(t, goodHCL)
	h = newHarness(map[string]string{"a.modelspec.hcl": badHCL, "a.modelspec.json": good})
	if code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json"); code != 1 || h.out.Len() != 0 || !strings.Contains(h.errb.String(), "has errors; fix them") {
		t.Fatalf("check of an invalid model: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
	// A syntax error is a finding too.
	h = newHarness(map[string]string{"a.modelspec.hcl": "record {"})
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 1 || !strings.Contains(h.errb.String(), "[syntax]") {
		t.Fatalf("syntax error: exit %d, stderr %q", code, h.errb)
	}
	// Warnings are shown and do not stop the export.
	h = newHarness(map[string]string{"a.modelspec.hcl": warnHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 0 || !strings.Contains(h.errb.String(), "[deprecated-spelling]") || !strings.Contains(h.out.String(), `"modelspec": "1.0-draft"`) || !strings.Contains(h.out.String(), `"entities"`) {
		t.Fatalf("warning: exit %d, stderr %q, stdout %q", code, h.errb, h.out)
	}
	// Findings of other files do not stop it: the context module is broken here.
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "ctx/b.hcl": "record {"})
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
	if code := h.run(append([]string{"export", "booking.modelspec.hcl", "--module", "core=shared/core.hcl"}, exportID...)...); code != 0 || !strings.Contains(h.out.String(), `"record": "core.Space"`) || strings.Contains(h.out.String(), `"Space": {`) {
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
		layoutPath("solo", "records.hcl"):            goodHCL,
		layoutPath("solo", "records.modelspec.json"): copyOfA,
	}
	h = newHarness(one)
	if code := h.run(append([]string{"export", layoutPath("solo", "records.hcl")}, exportID...)...); code != 0 || !strings.Contains(h.out.String(), `"records"`) {
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
	self := "record \"Node\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"parent\" {\n    record = \"tree.Node\"\n  }\n}\n"
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
		{"a reserved word", map[string]string{"a.modelspec.hcl": "projection \"p\" {\n}\n"}, append([]string{"export", "a.modelspec.hcl"}, exportID...), 1, "[reserved-word]"},
		{"check: missing json", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"export", "--check", "a.modelspec.hcl", "a.modelspec.json"}, 2, "modelspec: "},
		{"check: drift", map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": `{"modelspec":"1.0-draft-2","module":{"id":"x","name":"y","version":"1"},"records":{}}`}, []string{"export", "--check", "a.modelspec.hcl", "a.modelspec.json"}, 1, "not what a.modelspec.hcl exports to"},
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

// A model within the size limit can export to a twin over it (valid HCL of 3.6 to
// 4.1 MB exports to 4.9 to 6.9 MB), which lint would then refuse: export refuses to
// write it, to a file or to standard output, and says so. The limit is the
// environment's seam, set small so that the test does not build 4 MiB.
func TestExportRefusesATwinOverTheLimit(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("record \"E\" {\n  key = [\"p0\"]\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "  field \"p%d\" { type = \"int\" }\n", i)
	}
	b.WriteString("}\n")
	for _, args := range [][]string{{"export", "a.modelspec.hcl"}, {"export", "a.modelspec.hcl", "--out", "a.modelspec.json"}} {
		h := newHarness(map[string]string{"a.modelspec.hcl": b.String()})
		h.env.MaxTwinBytes = b.Len() // the HCL itself is within it; its twin is larger
		code := h.run(append(args, exportID...)...)
		if code != 1 || h.out.Len() != 0 || len(h.fsys.written) != 0 || !strings.Contains(h.errb.String(), "the JSON form of a.modelspec.hcl is") || !strings.Contains(h.errb.String(), fmt.Sprintf("over the %d-byte limit", b.Len())) || !strings.Contains(h.errb.String(), "nothing was written") {
			t.Fatalf("%v: exit %d, stdout %d bytes, written %d, stderr %.300s", args, code, h.out.Len(), len(h.fsys.written), h.errb)
		}
	}
	// A twin of exactly the limit is written.
	h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	h.env.MaxTwinBytes = 1 << 20
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errb)
	}
	h.env.MaxTwinBytes = h.out.Len()
	h.out.Reset()
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 0 || h.out.Len() != h.env.MaxTwinBytes {
		t.Fatalf("at the limit: exit %d, %d bytes: %s", code, h.out.Len(), h.errb)
	}
	h.env.MaxTwinBytes--
	h.out.Reset()
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, exportID...)...); code != 1 || h.out.Len() != 0 {
		t.Fatalf("one byte over: exit %d, %d bytes", code, h.out.Len())
	}
}

// Every file a command reads is limited, the JSON operand of export --check too:
// one over the limit is refused without being read.
func TestExportCheckRefusesAJSONOperandOverTheLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	h.fsys.MapFS["big.json"] = &fstest.MapFile{Data: make([]byte, modelspec.MaxInputBytes+1)}
	code := h.run("export", "--check", "a.modelspec.hcl", "big.json")
	if code != 1 || !strings.Contains(h.errb.String(), "big.json: file is 1048577 bytes; the limit is 1048576 bytes") {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	for _, name := range h.fsys.reads {
		if name == "big.json" {
			t.Fatal("the oversized file was read")
		}
	}
	// A file of exactly the limit is read (and is not the model's export).
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	h.fsys.MapFS["edge.json"] = &fstest.MapFile{Data: []byte(strings.Repeat(" ", modelspec.MaxInputBytes))}
	if code := h.run("export", "--check", "a.modelspec.hcl", "edge.json"); code != 1 || strings.Contains(h.errb.String(), "the limit is") {
		t.Fatalf("exit %d, stderr %.200s", code, h.errb)
	}
}

// What is not a regular file is refused by every command that reads a file: exit
// 2 and a message that says what it is, and it is never opened.
func TestCommandsRefuseWhatIsNotARegularFile(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"lint":                   {"lint", "dev.modelspec.hcl"},
		"lint of a json":         {"lint", "pipe.modelspec.json"},
		"export":                 {"export", "dev.modelspec.hcl"},
		"export of the model":    {"export", "--check", "dev.modelspec.hcl", "a.modelspec.json"},
		"export --check operand": {"export", "--check", "a.modelspec.hcl", "pipe.modelspec.json"},
		"lint --module path":     {"lint", "a.modelspec.hcl", "--module", "x=dev.modelspec.hcl"},
	} {
		h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": "{}", "dev.modelspec.hcl": "", "pipe.modelspec.json": ""})
		h.fsys.special = map[string]fs.FileMode{"dev.modelspec.hcl": fs.ModeDevice | fs.ModeCharDevice, "pipe.modelspec.json": fs.ModeNamedPipe}
		code := h.run(append(args[:len(args):len(args)], exportIDIfExport(args)...)...)
		if code != 2 || !strings.Contains(h.errb.String(), "not a regular file") {
			t.Errorf("%s: exit %d, stderr %q", name, code, h.errb)
		}
		for _, read := range h.fsys.reads {
			if read == "dev.modelspec.hcl" || read == "pipe.modelspec.json" {
				t.Errorf("%s: opened %s", name, read)
			}
		}
	}
}

func exportIDIfExport(args []string) []string {
	if args[0] == "export" && args[1] != "--check" {
		return exportID
	}
	return nil
}

// Numbers have one form, and export writes one that lint reads back: 1e41 through
// 1e100 are not written as integers of 42 to 101 digits, which lint refuses.
func TestExportWritesNumbersLintReadsBack(t *testing.T) {
	t.Parallel()
	hcl := `record "E" {
  key = ["id"]
  field "id" {
    type    = "string"
    max_len = 1e41
    min_len = 10e-1
  }
}

enum "N" {
  values = [1e41, 1e100, 5, 100000000000000000000000000000000000000, 1.0e2, -0]
}
`
	h := newHarness(map[string]string{"a.modelspec.hcl": hcl})
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "a.modelspec.json"}, exportID...)...); code != 0 {
		t.Fatalf("export: exit %d: %s", code, h.errb)
	}
	twin := string(h.fsys.written["a.modelspec.json"])
	for _, want := range []string{`"max_len": 1e41`, "\"min_len\": 1\n", `1e100`, `100000000000000000000000000000000000000`, `100,`, "0\n"} {
		if !strings.Contains(twin, want) {
			t.Errorf("the twin has no %q:\n%s", want, twin)
		}
	}
	h.fsys.MapFS["a.modelspec.json"] = &fstest.MapFile{Data: h.fsys.written["a.modelspec.json"]}
	for _, profile := range []string{"default", "publish"} {
		h.errb.Reset()
		h.out.Reset()
		if code := h.run("lint", "--profile", profile, "a.modelspec.hcl", "a.modelspec.json"); code != 0 {
			t.Errorf("lint of the pair under %s: exit %d: %s%s", profile, code, h.out, h.errb)
		}
	}
	h.errb.Reset()
	if code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json"); code != 0 {
		t.Errorf("export --check: exit %d: %s", code, h.errb)
	}
}

// The difference export --check reports is cut like a finding is, with its values.
func TestExportCheckCutsItsMessage(t *testing.T) {
	t.Parallel()
	model := func(pattern string) string {
		return "record \"E\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"string\"\n    pattern = \"" + pattern + "\"\n  }\n}\n"
	}
	h := newHarness(map[string]string{"a.modelspec.hcl": model(strings.Repeat("a", 200000) + "x")})
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "a.modelspec.json"}, exportID...)...); code != 0 {
		t.Fatalf("export: exit %d: %s", code, h.errb)
	}
	twin := strings.Replace(string(h.fsys.written["a.modelspec.json"]), "ax", "ay", 1)
	h.fsys.MapFS["a.modelspec.json"] = &fstest.MapFile{Data: []byte(twin)}
	h.errb.Reset()
	code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json")
	if code != 1 || h.errb.Len() > modelspec.MaxMessageBytes+200 || !strings.Contains(h.errb.String(), "bytes in all]") {
		t.Fatalf("exit %d, %d bytes of stderr: %.300s", code, h.errb.Len(), h.errb)
	}
}

// A model file that a search does not read (a link, a pipe, a device) is an error at
// the CLI under both profiles, and export and export --check will not write or approve
// the part of its module that was read. Three sequences of a review, for each kind.
func TestSkippedModelFileAtTheCLI(t *testing.T) {
	t.Parallel()
	order := "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"Customer\"\n  }\n}\n"
	customer := "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	invalid := "record \"Bad\" {\n  key = [\"nope\"]\n}\n"
	const dir = "spec/modules/shop/models/"
	kinds := map[string]func(h *harness, p, content string){
		"a link to a regular file": func(h *harness, p, content string) {
			h.fsys.MapFS["real/"+filepath.Base(p)] = &fstest.MapFile{Data: []byte(content)}
			h.fsys.MapFS[p] = &fstest.MapFile{Data: []byte("../../../../real/" + filepath.Base(p)), Mode: fs.ModeSymlink}
		},
		"a dangling link": func(h *harness, p, content string) {
			h.fsys.MapFS[p] = &fstest.MapFile{Data: []byte("nowhere"), Mode: fs.ModeSymlink}
		},
		"a named pipe": func(h *harness, p, content string) {
			h.fsys.MapFS[p] = &fstest.MapFile{Data: []byte(content)}
			h.fsys.special = map[string]fs.FileMode{p: fs.ModeNamedPipe}
		},
		"a device": func(h *harness, p, content string) {
			h.fsys.MapFS[p] = &fstest.MapFile{Data: []byte(content)}
			h.fsys.special = map[string]fs.FileMode{p: fs.ModeDevice | fs.ModeCharDevice}
		},
	}
	for kind, make := range kinds {
		shop := func() *harness {
			h := newHarness(map[string]string{dir + "order.hcl": order})
			make(h, dir+"customer.hcl", customer)
			return h
		}
		// 1: export writes nothing and approves nothing, and says why.
		h := shop()
		if code := h.run(append([]string{"export", dir + "order.hcl", "--out", "o.json"}, exportID...)...); code != 1 || len(h.fsys.written) != 0 || h.out.Len() != 0 || !strings.Contains(h.errb.String(), "[skipped-file]") || !strings.Contains(h.errb.String(), "not the whole module") {
			t.Errorf("%s: export: exit %d, written %d, stdout %q, stderr %q", kind, code, len(h.fsys.written), h.out, h.errb)
		}
		h = shop()
		h.fsys.MapFS["a.json"] = &fstest.MapFile{Data: []byte("{}")}
		if code := h.run("export", "--check", dir+"order.hcl", "a.json"); code != 1 || strings.Contains(h.out.String(), "ok:") || !strings.Contains(h.errb.String(), "[skipped-file]") {
			t.Errorf("%s: export --check: exit %d, stdout %q, stderr %q", kind, code, h.out, h.errb)
		}
		for _, profile := range []string{"default", "publish"} {
			// 3: a valid module is refused for the file, not for a reference that file answers.
			h = shop()
			code := h.run("lint", "--profile", profile, dir+"order.hcl")
			if code != 1 || strings.Contains(h.errb.String()+h.out.String(), "does not resolve") || !strings.Contains(h.out.String(), "[skipped-file]") {
				t.Errorf("%s, %s: lint of the module: exit %d, %q %q", kind, profile, code, h.out, h.errb)
			}
			// 2: an invalid model reached through one is not green.
			h = newHarness(map[string]string{"models/note.txt": ""})
			make(h, "models/bad.modelspec.hcl", invalid)
			if code := h.run("lint", "--profile", profile, "models"); code != 1 || !strings.Contains(h.out.String(), "models/bad.modelspec.hcl: error: is ") {
				t.Errorf("%s, %s: lint of a model behind one: exit %d, %q %q", kind, profile, code, h.out, h.errb)
			}
		}
	}
}

// --out writes a regular file: a path that exists and is not one (a link, a pipe, a
// device, a directory) is refused, without following a link, and nothing is written;
// a new path and a regular file are written as before. /dev/null is a device, and is
// refused like one.
func TestExportOutRefusesWhatIsNotARegularFile(t *testing.T) {
	t.Parallel()
	newHarnessWith := func() *harness {
		h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "existing.json": "old", "target.json": "target", "dir/x": ""})
		h.fsys.MapFS["link.json"] = &fstest.MapFile{Data: []byte("target.json"), Mode: fs.ModeSymlink}
		h.fsys.MapFS["dangling.json"] = &fstest.MapFile{Data: []byte("nowhere"), Mode: fs.ModeSymlink}
		h.fsys.MapFS["fifo.json"] = &fstest.MapFile{}
		h.fsys.MapFS["dev/null"] = &fstest.MapFile{}
		h.fsys.special = map[string]fs.FileMode{"fifo.json": fs.ModeNamedPipe, "dev/null": fs.ModeDevice | fs.ModeCharDevice}
		h.fsys.lstatErr = map[string]error{"unreadable.json": errors.New("permission denied")}
		return h
	}
	for out, want := range map[string]string{
		"link.json":       "link.json is a symbolic link, not a regular file",
		"dangling.json":   "dangling.json is a symbolic link, not a regular file",
		"fifo.json":       "fifo.json is a named pipe, not a regular file",
		"dev/null":        "dev/null is a character device, not a regular file",
		"dir":             "dir is a directory, not a regular file",
		"unreadable.json": "permission denied",
	} {
		h := newHarnessWith()
		code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", out}, exportID...)...)
		if code != 2 || len(h.fsys.written) != 0 || h.out.Len() != 0 || !strings.Contains(h.errb.String(), want) {
			t.Errorf("--out %s: exit %d, written %v, stderr %q", out, code, h.fsys.written, h.errb)
		}
	}
	for _, out := range []string{"new.json", "existing.json"} {
		h := newHarnessWith()
		if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", out}, exportID...)...); code != 0 || len(h.fsys.written[out]) == 0 {
			t.Errorf("--out %s: exit %d, written %v, stderr %q", out, code, h.fsys.written, h.errb)
		}
	}
}

// export and export --check decide from the loaded module, not from the findings: a second
// module supplied with --module that has more errors than the findings list holds (and a
// path that sorts before the link) must not push the finding about the link out of the list
// export reads. Every place the noisy module can have, and 999, 1,000 and 1,201 errors.
func TestExportRefusesAPartialModuleWhateverTheFindingsListHolds(t *testing.T) {
	t.Parallel()
	const dir = "spec/modules/shop/models/"
	order := "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	customer := "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	for _, noisyPath := range []string{"aaa.modelspec.hcl", "zzz.modelspec.hcl"} {
		for _, repeats := range []int{999, 1000, 1201} {
			noisy := "enum \"E\" {\n  values = [" + strings.Repeat("1, ", repeats-1) + "1]\n}\n"
			newTree := func() *harness {
				h := newHarness(map[string]string{dir + "order.hcl": order, "real/customer.hcl": customer, noisyPath: noisy})
				h.fsys.MapFS[dir+"customer.hcl"] = &fstest.MapFile{Data: []byte("../../../../real/customer.hcl"), Mode: fs.ModeSymlink}
				h.fsys.MapFS["half.json"] = &fstest.MapFile{Data: []byte("{}")}
				return h
			}
			module := "noisy=" + noisyPath
			for name, args := range map[string][]string{
				"export":         append([]string{"export", "--module", module, dir + "order.hcl"}, exportID...),
				"export --out":   append([]string{"export", "--module", module, dir + "order.hcl", "--out", "o.json"}, exportID...),
				"export --check": {"export", "--check", "--module", module, dir + "order.hcl", "half.json"},
			} {
				h := newTree()
				code := h.run(args...)
				if code != 1 || h.out.Len() != 0 || len(h.fsys.written) != 0 || !strings.Contains(h.errb.String(), "[skipped-file]") || !strings.Contains(h.errb.String(), "not the whole module") || !strings.Contains(h.errb.String(), "module shop has 1 file(s)") {
					t.Errorf("%s, %s, %d repeats: exit %d, stdout %q, written %d, stderr %.300q", name, noisyPath, repeats, code, h.out, len(h.fsys.written), h.errb)
				}
			}
			// lint of the same tree lists the skipped-file error, whatever else it cuts.
			h := newTree()
			code := h.run("lint", "spec", "--module", module)
			if code != 1 || !strings.Contains(h.out.String(), "customer.hcl: error: is a symbolic link") || !strings.Contains(h.out.String(), "[skipped-file]") {
				t.Errorf("lint, %s, %d repeats: exit %d, the skipped-file error is not listed (%d bytes of output)", noisyPath, repeats, code, h.out.Len())
			}
		}
	}
}

// The refusal names the --module module a skipped file was found under.
func TestExportRefusalNamesTheAssignedModule(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"ctx/x.modelspec.hcl": goodHCL, "real/y.hcl": goodHCL})
	h.fsys.MapFS["ctx/y.modelspec.hcl"] = &fstest.MapFile{Data: []byte("../real/y.hcl"), Mode: fs.ModeSymlink}
	code := h.run(append([]string{"export", "--module", "core=ctx", "ctx/x.modelspec.hcl"}, exportID...)...)
	if code != 1 || !strings.Contains(h.errb.String(), `ctx/y.modelspec.hcl (assigned to module "core" with --module)`) || len(h.fsys.written) != 0 || h.out.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
}

// The refusal lists the skipped files of the module that was exported and no others: a
// second module supplied with --module has a skipped file of its own, and neither its path
// nor its finding is printed.
func TestExportRefusalListsOnlyTheExportedModulesSkippedFiles(t *testing.T) {
	t.Parallel()
	const dir = "spec/modules/shop/models/"
	h := newHarness(map[string]string{dir + "order.hcl": goodHCL, "real/customer.hcl": goodHCL, "ctx/x.modelspec.hcl": coreHCL, "real/y.hcl": goodHCL})
	h.fsys.MapFS[dir+"customer.hcl"] = &fstest.MapFile{Data: []byte("../../../../real/customer.hcl"), Mode: fs.ModeSymlink}
	h.fsys.MapFS["ctx/y.modelspec.hcl"] = &fstest.MapFile{Data: []byte("../real/y.hcl"), Mode: fs.ModeSymlink}
	code := h.run(append([]string{"export", "--module", "core=ctx", dir + "order.hcl"}, exportID...)...)
	if code != 1 || !strings.Contains(h.errb.String(), "module shop has 1 file(s) found and not read ("+dir+"customer.hcl)") || strings.Contains(h.errb.String(), "y.modelspec.hcl") {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
}

// A module that refers into one that was not read whole (a link in its directory, supplied
// with --module) is exported as before, exit 0 and the same document, and standard error
// says what was not checked: the skipped-file finding of every such file, and once for the
// module that references into it were not checked.
func TestExportSaysWhenReferencesIntoAnIncompleteModuleWereNotChecked(t *testing.T) {
	t.Parallel()
	app := "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"s\" {\n    record = \"core.Nothing\"\n  }\n}\n"
	files := map[string]string{"app/app.modelspec.hcl": app, "core/space.hcl": coreHCL, "real/y.hcl": coreHCL, "real/z.hcl": coreHCL}
	args := append([]string{"export", "app/app.modelspec.hcl", "--module", "core=core"}, exportID...)
	whole := newHarness(files)
	if code := whole.run(args...); code != 1 || !strings.Contains(whole.errb.String(), `unknown record "Nothing" in module "core"`) {
		t.Fatalf("core whole: exit %d, stderr %q", code, whole.errb)
	}
	h := newHarness(files)
	h.fsys.MapFS["core/y.hcl"] = &fstest.MapFile{Data: []byte("../real/y.hcl"), Mode: fs.ModeSymlink}
	h.fsys.MapFS["core/z.hcl"] = &fstest.MapFile{Data: []byte("../real/z.hcl"), Mode: fs.ModeSymlink}
	code := h.run(args...)
	stderr := h.errb.String()
	if code != 0 || !strings.Contains(h.out.String(), `"record": "core.Nothing"`) ||
		!strings.Contains(stderr, "core/y.hcl: error: is a symbolic link") || !strings.Contains(stderr, "core/z.hcl: error: is a symbolic link") ||
		strings.Count(stderr, "references into it were not checked") != 1 || !strings.Contains(stderr, "note: module core was not read whole") {
		t.Fatalf("core incomplete: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
	// export --check says the same and still approves the document.
	committed := h.out.String()
	h = newHarness(files)
	h.fsys.MapFS["core/y.hcl"] = &fstest.MapFile{Data: []byte("../real/y.hcl"), Mode: fs.ModeSymlink}
	h.fsys.MapFS["committed.json"] = &fstest.MapFile{Data: []byte(committed)}
	if code := h.run("export", "--check", "--module", "core=core", "app/app.modelspec.hcl", "committed.json"); code != 0 || !strings.Contains(h.errb.String(), "references into it were not checked") || !strings.HasPrefix(h.out.String(), "ok:") {
		t.Fatalf("export --check: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
	// Nothing to say when every module was read whole.
	if whole = newHarness(map[string]string{"app/app.modelspec.hcl": goodHCL}); whole.run(append([]string{"export", "app/app.modelspec.hcl"}, exportID...)...) != 0 || whole.errb.Len() != 0 {
		t.Fatalf("no skipped file: stderr %q", whole.errb)
	}
}

// --out is not one of the inputs: writing the model's own file, however it is spelled, or a
// file supplied with --module, would replace a source with its JSON. It is refused, exit 2,
// and nothing is written. The JSON copy beside the model is an output, not a source.
func TestExportOutRefusesAnInput(t *testing.T) {
	t.Parallel()
	files := map[string]string{"m/booking.modelspec.hcl": bookingHCL, "shared/core.hcl": coreHCL, "shared/more.hcl": "enum \"E\" {\n  values = [\"x\"]\n}\n", "m/booking.modelspec.json": "{}"}
	for _, tc := range []struct {
		name string
		out  string
		want string
	}{
		{"the model", "m/booking.modelspec.hcl", "--out m/booking.modelspec.hcl is the input m/booking.modelspec.hcl"},
		{"the model, spelled otherwise", "./m/../m/booking.modelspec.hcl", "is the input m/booking.modelspec.hcl"},
		{"a file supplied with --module", "shared/core.hcl", "is the input shared/core.hcl"},
		{"a file of a directory supplied with --module", "shared/more.hcl", "is the input shared/more.hcl"},
	} {
		h := newHarness(files)
		code := h.run(append([]string{"export", "m/booking.modelspec.hcl", "--module", "core=shared", "--out", tc.out}, exportID...)...)
		if code != 2 || len(h.fsys.written) != 0 || h.out.Len() != 0 || !strings.Contains(h.errb.String(), tc.want) || !strings.Contains(h.errb.String(), "replace") {
			t.Errorf("%s: exit %d, written %v, stderr %q", tc.name, code, h.fsys.written, h.errb)
		}
	}
	// The copy beside the model, and a path that is nothing yet, are written.
	for _, out := range []string{"m/booking.modelspec.json", "m/new.json"} {
		h := newHarness(files)
		if code := h.run(append([]string{"export", "m/booking.modelspec.hcl", "--module", "core=shared", "--out", out}, exportID...)...); code != 0 || len(h.fsys.written[out]) == 0 {
			t.Errorf("--out %s: exit %d, written %v, stderr %q", out, code, h.fsys.written, h.errb)
		}
	}
}
