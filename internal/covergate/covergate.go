// Package covergate is the exact statement-coverage gate. It reads a Go cover
// profile and passes only when every statement in it was executed.
//
// The gate has no threshold to configure: it compares the number of covered
// statements with the total number of statements, so 99.99% fails. There is no
// flag, environment variable or input that lowers the bar.
package covergate

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

// Result is the statement count of a cover profile.
type Result struct {
	Covered int
	Total   int
}

// Complete reports whether every statement is covered. An empty profile is not
// complete: a profile with no statements means the tests did not run.
func (r Result) Complete() bool {
	return r.Total > 0 && r.Covered == r.Total
}

// block is one profile line's statement block, identified by its position.
type block struct {
	stmts   int
	covered bool
}

// Parse reads a cover profile. A block listed more than once (for example by
// several test binaries) counts once, and counts as covered if any listing
// covered it.
func Parse(r io.Reader) (Result, error) {
	res, _, err := ParseByPackage(r)
	return res, err
}

// ParseByPackage is Parse that also returns the number of statements the profile
// holds for each package, named by import path (the directory of a block's file).
func ParseByPackage(r io.Reader) (Result, map[string]int, error) {
	blocks := map[string]block{}
	var order []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || (line == 1 && strings.HasPrefix(text, "mode:")) {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 3 {
			return Result{}, nil, fmt.Errorf("line %d: want \"<block> <statements> <count>\", got %q", line, text)
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil || stmts < 0 {
			return Result{}, nil, fmt.Errorf("line %d: bad statement count %q", line, fields[1])
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil || count < 0 {
			return Result{}, nil, fmt.Errorf("line %d: bad execution count %q", line, fields[2])
		}
		b, seen := blocks[fields[0]]
		if !seen {
			order = append(order, fields[0])
		}
		b.stmts = stmts
		b.covered = b.covered || count > 0
		blocks[fields[0]] = b
	}
	if err := sc.Err(); err != nil {
		return Result{}, nil, err
	}
	var res Result
	perPackage := map[string]int{}
	for _, key := range order {
		b := blocks[key]
		res.Total += b.stmts
		if b.covered {
			res.Covered += b.stmts
		}
		file, _, _ := strings.Cut(key, ":")
		perPackage[path.Dir(file)] += b.stmts
	}
	return res, perPackage, nil
}

// Open opens the named cover profile. Run takes it as a parameter so tests can
// supply an in-memory profile.
type Open func(name string) (io.ReadCloser, error)

// OSOpen opens a profile on disk.
func OSOpen(name string) (io.ReadCloser, error) {
	return os.Open(name)
}

// Run checks the profile named by args, which must be exactly one path. It
// returns the process exit code: 0 when every statement is covered and every
// package of the module that has statements is in the profile with some, 1 when
// not, 2 on a usage or I/O error. Anything else on the command line,
// including a flag, is a usage error: the gate takes no options.
func Run(args []string, stdout, stderr io.Writer, open Open, packages Packages) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: covergate <cover profile>  (the gate takes no options: every statement must be covered)")
		return 2
	}
	f, err := open(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "covergate: %v\n", err)
		return 2
	}
	defer f.Close()
	res, perPackage, err := ParseByPackage(f)
	if err != nil {
		fmt.Fprintf(stderr, "covergate: %s: %v\n", args[0], err)
		return 2
	}
	pkgs, err := packages()
	if err != nil {
		fmt.Fprintf(stderr, "covergate: %v\n", err)
		return 2
	}
	failed := false
	for _, m := range Missing(pkgs, perPackage) {
		fmt.Fprintf(stdout, "coverage gate FAILED: %s\n", m)
		failed = true
	}
	if !res.Complete() {
		fmt.Fprintf(stdout, "coverage gate FAILED: %d of %d statements covered (%d uncovered)\n", res.Covered, res.Total, res.Total-res.Covered)
		failed = true
	}
	if failed {
		return 1
	}
	fmt.Fprintf(stdout, "coverage gate passed: %d of %d statements covered\n", res.Covered, res.Total)
	return 0
}
