// Package fuzzjudge decides, from the progress lines the Go fuzzer prints, whether a
// fuzz run stalled. `go test -fuzz` ends with PASS after a time limit whether or not
// anything ran in the last minutes, so scripts/fuzz.sh passes the log through
// cmd/fuzzjudge.
//
// What it can see is the whole run's rate, not one input or one worker. It fails a
// run in which
//
//   - no execution happened for StallGap anywhere in the run, including at the end
//     of the log; or
//   - the rate in the second half of the run (the median of the stretches that had
//     executions) fell below MinRatio of the rate in the first half (after its
//     first tenth, which is warm-up): a collapse to under a third; or
//   - the share of the second half that was idle (stretches between progress lines
//     with no execution) is more than MaxIdleRise above the share of the first
//     half's: a run that pauses in stretches of less than StallGap and runs at full
//     rate between them has a healthy median rate and no stall, and is mostly idle.
//
// It does not fail the pauses the fuzzer makes itself (12 to 18 seconds with no
// execution, seen in real runs), nor a stall of 30 seconds or less at the end, nor
// a rate that varies by a factor of two, nor the same pauses in both halves. A stall of one input in one of several
// workers leaves most of the rate, so it is the watchdog in
// scripts/fuzz/fuzz_test.go that stops an input that takes ten seconds, while it
// runs.
package fuzzjudge

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const (
	// StallGap is the time without an execution that is a stall. The fuzzer pauses
	// by itself for 12 to 18 seconds (seen in real runs), so it is more than that.
	StallGap = 30 * time.Second
	// MinRatio is the least rate of the second half of a run, as a part of the rate
	// of its first half. The rate of a real run varies by a factor of two from one
	// half to the other (the fuzzer pauses to minimise an input that found new
	// coverage), so only a collapse is a stall.
	MinRatio = 0.3
	// MaxIdleRise is the most the share of idle time of the second half of a run may
	// exceed the first half's (a fraction of the half: 0.5 is 50 points). The fuzzer's
	// own pauses (12 to 18 seconds, seen in real runs) fall in either half, so only a
	// share far above the first half's is a stall.
	MaxIdleRise = 0.5
)

// Sample is one progress line: the time since the run began and the executions
// so far.
type Sample struct {
	Elapsed time.Duration
	Execs   int64
}

var progress = regexp.MustCompile(`^fuzz: elapsed: ([0-9hms.]+), execs: ([0-9]+)`)

// Parse reads the progress lines of a fuzz log, ignoring the others.
func Parse(r io.Reader) ([]Sample, error) {
	var out []Sample
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		m := progress.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		elapsed, err := time.ParseDuration(m[1])
		if err != nil {
			return nil, fmt.Errorf("the elapsed time %q of a progress line: %w", m[1], err)
		}
		execs, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("the executions %q of a progress line: %w", m[2], err)
		}
		out = append(out, Sample{elapsed, execs})
	}
	return out, sc.Err()
}

// Verdict is the judgement of a run.
type Verdict struct {
	Execs     int64
	Elapsed   time.Duration
	FirstRate float64 // executions a second in the first half, after the first tenth
	LastRate  float64 // and in the second half
	FirstIdle float64 // the share of the first half (after its first tenth) with no execution, from 0 to 1
	LastIdle  float64 // and of the second half
	Problem   string  // empty when the run is sound
}

// activeRate is the median of the rates, in executions a second, of the stretches
// between progress lines that end in (from, to] and had executions: a pause is the
// stall rule's concern, not the rate's, and a line printed in the middle of one
// gives a stretch of a part of the rate, which a median does not count.
func activeRate(samples []Sample, from, to time.Duration) float64 {
	var rates []float64
	prev := Sample{}
	for _, s := range samples {
		if s.Elapsed > from && s.Elapsed <= to && s.Execs > prev.Execs {
			rates = append(rates, float64(s.Execs-prev.Execs)/(s.Elapsed-prev.Elapsed).Seconds())
		}
		prev = s
	}
	if len(rates) == 0 {
		return 0
	}
	sort.Float64s(rates)
	return rates[len(rates)/2]
}

// idleShare is the part of the time from from to to that was idle: the stretches
// between progress lines with no execution, as far as they lie in (from, to].
func idleShare(samples []Sample, from, to time.Duration) float64 {
	var idle time.Duration
	prev := Sample{}
	for _, s := range samples {
		if s.Execs == prev.Execs {
			idle += max(0, min(s.Elapsed, to)-max(prev.Elapsed, from))
		}
		prev = s
	}
	return float64(idle) / float64(to-from)
}

// Judge judges a run from its samples.
func Judge(samples []Sample) Verdict {
	if len(samples) < 2 {
		return Verdict{Problem: fmt.Sprintf("only %d progress lines, too few to judge the run", len(samples))}
	}
	last := samples[len(samples)-1]
	v := Verdict{Execs: last.Execs, Elapsed: last.Elapsed}
	// The longest time without an execution, counting a stretch that runs to the end.
	var quietFrom time.Duration
	prev := Sample{}
	for _, s := range samples {
		if s.Execs > prev.Execs {
			quietFrom = s.Elapsed
		} else if s.Elapsed-quietFrom >= StallGap {
			v.Problem = fmt.Sprintf("no execution from %v to %v, at least %v: the run stalled", quietFrom.Round(time.Second), s.Elapsed.Round(time.Second), StallGap)
			break
		}
		prev = s
	}
	warm, half := last.Elapsed/10, last.Elapsed/2
	v.FirstRate = activeRate(samples, warm, half)
	v.LastRate = activeRate(samples, half, last.Elapsed)
	v.FirstIdle = idleShare(samples, warm, half)
	v.LastIdle = idleShare(samples, half, last.Elapsed)
	if v.Problem == "" && v.LastRate < MinRatio*v.FirstRate {
		v.Problem = fmt.Sprintf("the rate fell from %.0f to %.0f executions a second between the first and the second half of the run (below %.0f%% of it)", v.FirstRate, v.LastRate, MinRatio*100)
	}
	if v.Problem == "" && v.LastIdle > v.FirstIdle+MaxIdleRise {
		v.Problem = fmt.Sprintf("the run was idle %.0f%% of the second half, against %.0f%% of the first (more than %.0f points more): pauses shorter than %v each, but most of the half", v.LastIdle*100, v.FirstIdle*100, MaxIdleRise*100, StallGap)
	}
	return v
}

// Run reads a fuzz log from in, judges it, prints one line about the target to out
// and returns the exit code: 0 for a sound run, 1 for a stalled one, 2 for a
// log that cannot be read.
func Run(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, "usage: fuzzjudge <target> < fuzz.log")
		return 2
	}
	samples, err := Parse(in)
	if err != nil {
		fmt.Fprintf(errOut, "%s: %v\n", args[0], err)
		return 2
	}
	v := Judge(samples)
	fmt.Fprintf(out, "%s: %d executions in %v; %.0f/sec in the first half, %.0f/sec in the second; idle %.0f%% of the first half, %.0f%% of the second\n", args[0], v.Execs, v.Elapsed.Round(time.Second), v.FirstRate, v.LastRate, v.FirstIdle*100, v.LastIdle*100)
	if v.Problem != "" {
		fmt.Fprintf(out, "%s: STALLED: %s\n", args[0], v.Problem)
		return 1
	}
	return 0
}
