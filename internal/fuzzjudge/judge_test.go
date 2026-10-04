package fuzzjudge

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// synth is the log of a run of the given seconds, a progress line every three
// seconds, in which rate(t) is the executions a second at second t.
func synth(seconds int, rate func(t int) int) string {
	var b strings.Builder
	b.WriteString("fuzz: elapsed: 0s, gathering baseline coverage: 0/10 completed\n")
	b.WriteString("fuzz: elapsed: 1s, gathering baseline coverage: 10/10 completed, now fuzzing with 2 workers\n")
	execs := 0
	for t := 3; t <= seconds; t += 3 {
		for s := t - 3; s < t; s++ {
			execs += rate(s)
		}
		fmt.Fprintf(&b, "fuzz: elapsed: %s, execs: %d (%d/sec), new interesting: 1 (total: 9)\n", time.Duration(t)*time.Second, execs, rate(t-1))
	}
	return b.String()
}

func steady(t int) int { return 20000 }

func TestJudge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		log  string
		want string // a part of the problem; empty when the run is sound
	}{
		{"a steady run", synth(60, steady), ""},
		{"a slow decline, as in real runs (20000 to 15000)", synth(180, func(t int) int { return 20000 - t*28 }), ""},
		{"a cold start", synth(60, func(t int) int {
			if t < 6 {
				return 2000
			}
			return 20000
		}), ""},
		{"the fuzzer's own pauses of 18 and 12 seconds", synth(60, func(t int) int {
			if (t >= 20 && t < 38) || (t >= 45 && t < 57) {
				return 0
			}
			return 20000
		}), ""},
		{"a stall of 29 seconds at the end of a run of three minutes", synth(180, func(t int) int {
			if t >= 151 {
				return 0
			}
			return 20000
		}), ""},
		{"a stall of nine seconds in the last fifth", synth(60, func(t int) int {
			if t >= 51 {
				return 0
			}
			return 20000
		}), ""},
		{"one worker of ten stalling", synth(60, func(t int) int {
			if t >= 20 {
				return 18000
			}
			return 20000
		}), ""},
		{"a stall from 20 seconds", synth(60, func(t int) int {
			if t >= 20 {
				return 0
			}
			return 20000
		}), "no execution from 21s to 51s"},
		{"a stall of 30 seconds at the end", synth(180, func(t int) int {
			if t >= 150 {
				return 0
			}
			return 20000
		}), "the run stalled"},
		{"a stall in the middle that the run recovers from", synth(180, func(t int) int {
			if t >= 60 && t < 100 {
				return 0
			}
			return 20000
		}), "no execution from 1m0s to 1m30s"},
		{"one worker of two stalling (half the rate: the watchdog's to catch)", synth(60, func(t int) int {
			if t >= 20 {
				return 10000
			}
			return 20000
		}), ""},
		{"a rate that halves between the halves, as in a real run", synth(60, func(t int) int {
			if t >= 30 {
				return 12000
			}
			return 24000
		}), ""},
		{"a rate that fell to a quarter", synth(180, func(t int) int {
			if t >= 90 {
				return 5000
			}
			return 20000
		}), "the rate fell from 20000 to 5000"},
		{"a rate that fell to a third", synth(180, func(t int) int {
			if t >= 90 {
				return 6000
			}
			return 20000
		}), ""},
		{"no execution at all", synth(60, func(t int) int { return 0 }), "no execution from 0s"},
		{"no progress lines", "PASS\n", "only 0 progress lines"},
		{"one progress line", "fuzz: elapsed: 3s, execs: 10 (3/sec), new interesting: 0 (total: 1)\n", "only 1 progress lines"},
	} {
		samples, err := Parse(strings.NewReader(tc.log))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		v := Judge(samples)
		if tc.want == "" && v.Problem != "" || tc.want != "" && !strings.Contains(v.Problem, tc.want) {
			t.Errorf("%s: problem %q, want %q (rates %.0f and %.0f)", tc.name, v.Problem, tc.want, v.FirstRate, v.LastRate)
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	log := "fuzz: elapsed: 0s, gathering baseline coverage: 0/5 completed\n" +
		"fuzz: elapsed: 59s, execs: 100 (1/sec), new interesting: 0 (total: 1)\n" +
		"fuzz: elapsed: 1m0s, execs: 200 (2/sec), new interesting: 0 (total: 1)\n" +
		"fuzz: elapsed: 3m1s, execs: 3964796 (0/sec), new interesting: 12 (total: 614)\n" +
		"fuzz: elapsed: 1h2m3s, execs: 9 (0/sec), new interesting: 12 (total: 614)\n" +
		"PASS\nok  \tgithub.com/x/fuzz\t180.343s\n"
	got, err := Parse(strings.NewReader(log))
	want := []Sample{{59 * time.Second, 100}, {time.Minute, 200}, {3*time.Minute + time.Second, 3964796}, {time.Hour + 2*time.Minute + 3*time.Second, 9}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("Parse = %v, %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d = %v, want %v", i, got[i], want[i])
		}
	}
	for name, bad := range map[string]string{
		"an elapsed time that is not one": "fuzz: elapsed: 1.2.3s, execs: 5 (1/sec)\n",
		"executions that overflow":        "fuzz: elapsed: 3s, execs: 99999999999999999999 (1/sec)\n",
	} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := Parse(failingReader{}); err == nil || err.Error() != "read failed" {
		t.Errorf("a read error: %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestRun(t *testing.T) {
	t.Parallel()
	run := func(args []string, in string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := Run(args, strings.NewReader(in), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	if code, out, _ := run([]string{"FuzzHCL"}, synth(60, steady)); code != 0 || !strings.Contains(out, "FuzzHCL: 1200000 executions in 1m0s; 20000/sec in the first half, 20000/sec in the second") {
		t.Errorf("a sound run: %d %q", code, out)
	}
	stalled := synth(60, func(t int) int {
		if t >= 20 {
			return 0
		}
		return 20000
	})
	if code, out, _ := run([]string{"FuzzJSON"}, stalled); code != 1 || !strings.Contains(out, "FuzzJSON: STALLED: no execution from 21s to 51s") {
		t.Errorf("a stalled run: %d %q", code, out)
	}
	if code, _, errOut := run(nil, ""); code != 2 || !strings.Contains(errOut, "usage: fuzzjudge") {
		t.Errorf("no target: %d %q", code, errOut)
	}
	if code, _, errOut := run([]string{"FuzzHCL"}, "fuzz: elapsed: 1.2.3s, execs: 5\n"); code != 2 || !strings.Contains(errOut, "FuzzHCL: the elapsed time") {
		t.Errorf("an unreadable log: %d %q", code, errOut)
	}
}
