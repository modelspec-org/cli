package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/strongo/buildinfo"
	"github.com/strongo/cli-helpers/selfupdate"
)

// memFS is an in-memory modelspec.FS.
type memFS struct {
	fstest.MapFS
	written  map[string][]byte
	writeErr error
}

func (m *memFS) WriteFile(name string, data []byte, _ fs.FileMode) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.written[name] = data
	return nil
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
	h := &harness{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, fsys: &memFS{MapFS: fstest.MapFS{}, written: map[string][]byte{}}}
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

const warnHCL = `component "C" {
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

func TestLintText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		files    map[string]string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"clean default path", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint"}, 0, "ok: 1 file checked, 0 errors, 0 warnings\n", ""},
		{"findings", map[string]string{"a.modelspec.hcl": badHCL, "b.modelspec.hcl": goodHCL}, []string{"lint", "."}, 1, "a.modelspec.hcl:1: error: entity \"A\" has no key (a list of the properties that identify a record) [key]\nfailed: 2 files checked, 1 error, 0 warnings\n", ""},
		{"warnings only", map[string]string{"a.modelspec.hcl": warnHCL}, []string{"lint", "a.modelspec.hcl"}, 0, "warning: entity \"A\" property \"c\" uses a component", ""},
		{"explicit files", map[string]string{"a.modelspec.hcl": goodHCL, "b.modelspec.hcl": badHCL}, []string{"lint", "a.modelspec.hcl"}, 0, "ok: 1 file checked", ""},
		{"missing path", nil, []string{"lint", "nope"}, 2, "", "modelspec: "},
		{"no model files", map[string]string{"x.txt": ""}, []string{"lint"}, 2, "", "no ModelSpec files"},
		{"bad format", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--format", "xml"}, 2, "", `invalid --format "xml"`},
		{"unknown flag", nil, []string{"lint", "--nope"}, 2, "", "unknown flag: --nope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(tc.files)
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
		left  int
	}{
		{"finding line", map[string]string{"a.modelspec.hcl": badHCL}, []string{"lint"}, 0},
		{"summary line", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint"}, 0},
		{"json", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"lint", "--format", "json"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(tc.files)
			h.env.Stdout = &failWriter{left: tc.left}
			if code := h.run(tc.args...); code != 2 || !strings.Contains(h.errb.String(), "write failed") {
				t.Fatalf("exit %d, stderr %q", code, h.errb)
			}
		})
	}
}

func TestExport(t *testing.T) {
	t.Parallel()
	id := []string{"--module-id", "x/y", "--module-name", "y", "--module-version", "1"}
	h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, id...)...); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errb)
	}
	if !strings.Contains(h.out.String(), "\"modelspec\": \"1.0-draft\"") || !strings.HasSuffix(h.out.String(), "}\n") {
		t.Fatalf("stdout = %s", h.out)
	}
	stdoutJSON := h.out.String()

	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "a.modelspec.json"}, id...)...); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errb)
	}
	if string(h.fsys.written["a.modelspec.json"]) != stdoutJSON || h.out.Len() != 0 {
		t.Fatalf("--out wrote %q, stdout %q", h.fsys.written["a.modelspec.json"], h.out)
	}

	// The file just written is what the check wants.
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": stdoutJSON})
	if code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json"); code != 0 || !strings.Contains(h.out.String(), "ok: a.modelspec.json is what a.modelspec.hcl exports to") {
		t.Fatalf("check: exit %d, stdout %q, stderr %q", code, h.out, h.errb)
	}
}

func TestExportFailures(t *testing.T) {
	t.Parallel()
	id := []string{"--module-id", "x/y", "--module-name", "y", "--module-version", "1"}
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
		{"not an hcl file", map[string]string{"a.json": "{}"}, append([]string{"export", "a.json"}, id...), 2, "export reads .modelspec.hcl files"},
		{"missing file", nil, append([]string{"export", "a.modelspec.hcl"}, id...), 2, "modelspec: "},
		{"does not parse", map[string]string{"a.modelspec.hcl": "entity {"}, append([]string{"export", "a.modelspec.hcl"}, id...), 1, "does not parse as ModelSpec HCL"},
		{"no identity", map[string]string{"a.modelspec.hcl": goodHCL}, []string{"export", "a.modelspec.hcl"}, 1, "supply all three"},
		{"unmapped construct", map[string]string{"a.modelspec.hcl": "projection \"p\" {\n}\n"}, append([]string{"export", "a.modelspec.hcl"}, id...), 1, "no JSON form is defined"},
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
			if !strings.Contains(h.errb.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want containing %q", h.errb, tc.wantErr)
			}
		})
	}
}

func TestExportIOFailures(t *testing.T) {
	t.Parallel()
	id := []string{"--module-id", "x/y", "--module-name", "y", "--module-version", "1"}
	h := newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	h.fsys.writeErr = errors.New("disk full")
	if code := h.run(append([]string{"export", "a.modelspec.hcl", "--out", "o.json"}, id...)...); code != 2 || !strings.Contains(h.errb.String(), "disk full") {
		t.Errorf("--out failure: exit %d, stderr %q", code, h.errb)
	}
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	h.env.Stdout = &failWriter{}
	if code := h.run(append([]string{"export", "a.modelspec.hcl"}, id...)...); code != 2 || !strings.Contains(h.errb.String(), "write failed") {
		t.Errorf("stdout failure: exit %d, stderr %q", code, h.errb)
	}
	// A check whose ok message cannot be written.
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL})
	var buf bytes.Buffer
	h.env.Stdout = &buf
	h.run(append([]string{"export", "a.modelspec.hcl"}, id...)...)
	h = newHarness(map[string]string{"a.modelspec.hcl": goodHCL, "a.modelspec.json": buf.String()})
	h.env.Stdout = &failWriter{}
	if code := h.run("export", "--check", "a.modelspec.hcl", "a.modelspec.json"); code != 2 {
		t.Errorf("check message failure: exit %d", code)
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
