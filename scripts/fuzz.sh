#!/usr/bin/env bash
# Runs the fuzz targets for the HCL and JSON readers (scripts/fuzz/fuzz_test.go).
# Outside the default test run and outside the coverage gate: the targets skip
# unless MODELSPEC_FUZZ is set, which this script sets.
#
#   scripts/fuzz.sh [seconds-per-target]     (default 120)
#   MODELSPEC_FUZZ_PARALLEL=2 scripts/fuzz.sh   (workers per target; default: one per CPU)
#
# A crash (including a stack overflow in the HCL parser, which is fatal) leaves its
# input under scripts/fuzz/testdata/fuzz/; copy it into a unit test and fix the cause.
# An input that costs more than the budget in scripts/fuzz/fuzz_test.go (bytes
# allocated for its size, or ten seconds) fails the run in the same way.
#
# A run that stalls does not pass either: the fuzzer's progress lines go through
# cmd/fuzzjudge (internal/fuzzjudge, with its tests), which prints the executions and
# the rate in each half of the run, and fails when nothing ran for 30 seconds or the
# rate of the second half fell below 30% of the first. One input that takes ten
# seconds is stopped while it runs, by the watchdog in scripts/fuzz/fuzz_test.go.
#
# -fuzzminimizetime 5s: by default the fuzzer minimises a new input for up to a
# minute, and with two workers both can be doing it at once, with no execution
# reported, which a judge cannot tell from a stall.
set -euo pipefail
seconds="${1:-120}"
cd "$(dirname "$0")/.."
scratch="$(mktemp -d "${TMPDIR:-/tmp}/modelspec-fuzz.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
parallel=()
if [ -n "${MODELSPEC_FUZZ_PARALLEL:-}" ]; then parallel=(-parallel "$MODELSPEC_FUZZ_PARALLEL"); fi
status=0
for target in FuzzHCL FuzzJSON; do
  echo "== $target for ${seconds}s"
  log="$scratch/$target.log"
  MODELSPEC_FUZZ=1 go test ./scripts/fuzz -run '^$' -fuzz "^${target}\$" -fuzztime "${seconds}s" -fuzzminimizetime 5s ${parallel[@]+"${parallel[@]}"} 2>&1 | tee "$log" || status=1
  go run ./cmd/fuzzjudge "$target" < "$log" || status=1
done
exit "$status"
