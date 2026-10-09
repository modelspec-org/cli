package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

const (
	oldHCL = "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n}\n"
	newHCL = "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	oldDoc = `{"modelspec": "1.0-draft", "module": {"id": "x", "version": "1"}, "entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`
	newDoc = `{"modelspec": "1.0-draft-2", "module": {"id": "x", "version": "1"}, "records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`
)

func TestRewriteDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"a.modelspec.hcl": oldHCL, "b.modelspec.json": oldDoc, "c.modelspec.hcl": newHCL})
	if code := h.run("rewrite"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	want := "a.modelspec.hcl: would change, 2 replacements\nb.modelspec.json: would change, 3 replacements\n2 files would change, 1 unchanged\n"
	if h.out.String() != want || h.errb.Len() != 0 || len(h.fsys.written) != 0 {
		t.Errorf("stdout %q (want %q), stderr %q, written %v", h.out, want, h.errb, h.fsys.written)
	}
}

func TestRewriteWrite(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"a.modelspec.hcl": oldHCL, "b.modelspec.json": oldDoc, "c.modelspec.hcl": newHCL})
	if code := h.run("rewrite", "--write", "."); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	want := "a.modelspec.hcl: rewritten, 2 replacements\nb.modelspec.json: rewritten, 3 replacements\n2 files rewritten (5 replacements), 1 unchanged\n"
	if h.out.String() != want {
		t.Errorf("stdout %q, want %q", h.out, want)
	}
	if string(h.fsys.written["a.modelspec.hcl"]) != newHCL || string(h.fsys.written["b.modelspec.json"]) != newDoc || len(h.fsys.written) != 2 {
		t.Errorf("written %q", h.fsys.written)
	}
	// What was written is not rewritten again.
	h = newHarness(map[string]string{"a.modelspec.hcl": newHCL})
	if code := h.run("rewrite", "--write"); code != 0 || h.out.String() != "0 files rewritten (0 replacements), 1 unchanged\n" || len(h.fsys.written) != 0 {
		t.Errorf("a file in the new spelling: exit %d, stdout %q, written %v", code, h.out, h.fsys.written)
	}
}

func TestRewriteCheck(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"a.modelspec.hcl": oldHCL, "c.modelspec.hcl": newHCL})
	if code := h.run("rewrite", "--check"); code != 1 || h.out.String() != "a.modelspec.hcl: would change, 2 replacements\n1 file would change, 1 unchanged\n" || len(h.fsys.written) != 0 {
		t.Errorf("a file in the old spelling: exit %d, stdout %q, written %v", code, h.out, h.fsys.written)
	}
	h = newHarness(map[string]string{"c.modelspec.hcl": newHCL})
	if code := h.run("rewrite", "--check", "c.modelspec.hcl"); code != 0 || h.out.String() != "0 files would change, 1 unchanged\n" {
		t.Errorf("a file in the new spelling: exit %d, stdout %q", code, h.out)
	}
	h = newHarness(map[string]string{"c.modelspec.hcl": newHCL})
	if code := h.run("rewrite", "--check", "--write"); code != 2 || !strings.Contains(h.errb.String(), "--write and --check cannot be combined") {
		t.Errorf("both flags: exit %d, stderr %q", code, h.errb)
	}
}

// A file that cannot be rewritten safely is named with the reason and makes the exit
// code 1; the others are still rewritten.
func TestRewriteRefusals(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"a.modelspec.hcl":  oldHCL,
		"b.modelspec.hcl":  "entity \"B\" {\n}\ncollection \"c\" {\n}\n",
		"c.modelspec.hcl":  "entity {",
		"d.modelspec.json": `{"modelspec": "2"}`,
		"e.modelspec.hcl":  "# " + strings.Repeat("e", modelspec.MaxInputBytes) + "\n",
	}
	h := newHarness(files)
	if code := h.run("rewrite", "--write"); code != 1 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	wantErr := []string{
		"b.modelspec.hcl: not rewritten: it holds the collection block at line 3, a construct decision 0019 removed\n",
		"c.modelspec.hcl: not rewritten: does not parse (",
		`d.modelspec.json: not rewritten: its "modelspec" is "2", which is neither "1.0-draft" nor "1.0-draft-2"` + "\n",
		"e.modelspec.hcl: not rewritten: e.modelspec.hcl: file is ",
	}
	for _, w := range wantErr {
		if !strings.Contains(h.errb.String(), w) {
			t.Errorf("stderr lacks %q:\n%s", w, h.errb)
		}
	}
	if h.out.String() != "a.modelspec.hcl: rewritten, 2 replacements\n1 file rewritten (2 replacements), 0 unchanged, 4 not rewritten\n" || len(h.fsys.written) != 1 || string(h.fsys.written["a.modelspec.hcl"]) != newHCL {
		t.Errorf("stdout %q, written %v", h.out, h.fsys.written)
	}
}

// A link found by a search is not written through; one named on the command line is
// followed, and the file it names is written, the link left as it is.
func TestRewriteLinks(t *testing.T) {
	t.Parallel()
	files := map[string]string{"real.modelspec.hcl": oldHCL, "alias.modelspec.hcl": oldHCL}
	newLinked := func() *harness {
		h := newHarness(files)
		h.fsys.links = map[string]string{"alias.modelspec.hcl": "real.modelspec.hcl"}
		return h
	}
	h := newLinked()
	if code := h.run("rewrite", "--write", "alias.modelspec.hcl"); code != 0 {
		t.Fatalf("named link: exit %d, stderr %q", code, h.errb)
	}
	if len(h.fsys.written) != 1 || string(h.fsys.written["real.modelspec.hcl"]) != newHCL {
		t.Errorf("named link: written %v", h.fsys.written)
	}
	h = newLinked()
	if code := h.run("rewrite", "--write", "."); code != 1 || !strings.Contains(h.errb.String(), "alias.modelspec.hcl: not rewritten: is a symbolic link, which a search does not read") || len(h.fsys.written) != 1 || h.fsys.written["real.modelspec.hcl"] == nil {
		t.Errorf("found link: exit %d, stderr %q, written %v", code, h.errb, h.fsys.written)
	}
	h = newLinked()
	h.fsys.linkErr = map[string]error{"alias.modelspec.hcl": errors.New("loop")}
	if code := h.run("rewrite", "--write", "alias.modelspec.hcl"); code != 2 || !strings.Contains(h.errb.String(), "loop") || len(h.fsys.written) != 0 {
		t.Errorf("a link that cannot be followed: exit %d, stderr %q, written %v", code, h.errb, h.fsys.written)
	}
}

func TestRewriteModuleAssignments(t *testing.T) {
	t.Parallel()
	files := map[string]string{"use.modelspec.hcl": oldHCL, "parts/a.hcl": oldHCL, "other/b.hcl": oldHCL}
	h := newHarness(files)
	if code := h.run("rewrite", "--write", "use.modelspec.hcl", "--module", "shop=parts"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if len(h.fsys.written) != 2 || h.fsys.written["parts/a.hcl"] == nil || h.fsys.written["use.modelspec.hcl"] == nil {
		t.Errorf("written %v", h.fsys.written)
	}
	// With only an assignment, the current directory is not searched.
	h = newHarness(files)
	if code := h.run("rewrite", "--module", "shop=parts"); code != 0 || h.out.String() != "parts/a.hcl: would change, 2 replacements\n1 file would change, 0 unchanged\n" {
		t.Errorf("only an assignment: exit %d, stdout %q", code, h.out)
	}
	h = newHarness(files)
	if code := h.run("rewrite", "--module", "shop"); code != 2 || !strings.Contains(h.errb.String(), `invalid --module "shop"`) {
		t.Errorf("a bad assignment: exit %d, stderr %q", code, h.errb)
	}
	h = newHarness(files)
	if code := h.run("rewrite", "--module", "shop=nope"); code != 2 {
		t.Errorf("an assignment of a missing path: exit %d", code)
	}
}

func TestRewriteIOFailures(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"x.txt": ""})
	if code := h.run("rewrite"); code != 2 || !strings.Contains(h.errb.String(), "no ModelSpec files (.modelspec.hcl, .modelspec.json) found in [.]") {
		t.Errorf("no files: exit %d, stderr %q", code, h.errb)
	}
	h = newHarness(map[string]string{"parts/x.txt": ""})
	if code := h.run("rewrite", "--module", "m=parts"); code != 2 || !strings.Contains(h.errb.String(), "found in [parts]") {
		t.Errorf("no files under an assignment: exit %d, stderr %q", code, h.errb)
	}
	h = newHarness(map[string]string{"x.txt": ""})
	if code := h.run("rewrite", "nope"); code != 2 {
		t.Errorf("a missing path: exit %d", code)
	}
	h = newHarness(map[string]string{"x.txt": ""})
	if code := h.run("rewrite", "--module", "m=nope"); code != 2 {
		t.Errorf("a missing assignment: exit %d", code)
	}
	h = newHarness(map[string]string{"a.modelspec.hcl": oldHCL})
	h.fsys.openErr = map[string]error{"a.modelspec.hcl": errors.New("unreadable")}
	if code := h.run("rewrite"); code != 2 || !strings.Contains(h.errb.String(), "unreadable") {
		t.Errorf("a file that cannot be read: exit %d, stderr %q", code, h.errb)
	}
	h = newHarness(map[string]string{"a.modelspec.hcl": oldHCL})
	h.fsys.writeErr = errors.New("disk full")
	if code := h.run("rewrite", "--write"); code != 2 || !strings.Contains(h.errb.String(), "disk full") || h.out.Len() != 0 {
		t.Errorf("a file that cannot be written: exit %d, stderr %q, stdout %q", code, h.errb, h.out)
	}
	// Standard output that fails: after the first line, and at the summary.
	for _, left := range []int{0, len("a.modelspec.hcl: would change, 2 replacements\n")} {
		h = newHarness(map[string]string{"a.modelspec.hcl": oldHCL})
		h.env.Stdout = &failWriter{left: left}
		if code := h.run("rewrite"); code != 2 || !strings.Contains(h.errb.String(), "write failed") {
			t.Errorf("stdout failing after %d bytes: exit %d, stderr %q", left, code, h.errb)
		}
	}
}

// Against the real filesystem: the file keeps its mode, a link named on the command
// line stays a link and its target is rewritten, a link found in a search is left
// alone, and no temporary file is left behind.
func TestRewriteOnTheRealFilesystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real.modelspec.hcl")
	if err := os.WriteFile(real, []byte(oldHCL), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.modelspec.json")
	if err := os.WriteFile(other, []byte(oldDoc), 0o640); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "target.modelspec.hcl")
	if err := os.WriteFile(elsewhere, []byte(oldHCL), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.modelspec.hcl")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	h := newHarness(nil)
	h.env.FS = modelspec.OSFS{}
	if code := h.run("rewrite", "--write", real, other, link); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	for path, want := range map[string]struct {
		text string
		mode fs.FileMode
	}{real: {newHCL, 0o600}, other: {newDoc, 0o640}, elsewhere: {newHCL, 0o644}} {
		got, err := os.ReadFile(path)
		info, _ := os.Stat(path)
		if err != nil || string(got) != want.text || info.Mode().Perm() != want.mode {
			t.Errorf("%s: %q, mode %v, %v; want %q, %v", path, got, info.Mode().Perm(), err, want.text, want.mode)
		}
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the link is no longer a link: %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Errorf("files left in the directory: %v", entries)
	}
	// A search does not write through the link; it says so.
	if err := os.WriteFile(elsewhere, []byte(oldHCL), 0o644); err != nil {
		t.Fatal(err)
	}
	h = newHarness(nil)
	h.env.FS = modelspec.OSFS{}
	if code := h.run("rewrite", "--write", dir); code != 1 || !strings.Contains(h.errb.String(), "link.modelspec.hcl: not rewritten: is a symbolic link") {
		t.Errorf("search: exit %d, stderr %q", code, h.errb)
	}
	if got, _ := os.ReadFile(elsewhere); string(got) != oldHCL {
		t.Errorf("the link was written through: %q", got)
	}
}
