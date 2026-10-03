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
# A run that stalls does not pass either: the script reads the fuzzer's own progress
# lines, prints the executions and the rate in the first and the last fifth of the
# run, and fails when no execution happened in the last fifth or the rate there fell
# below a tenth of the rate in the first.
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
  MODELSPEC_FUZZ=1 go test ./scripts/fuzz -run '^$' -fuzz "^${target}\$" -fuzztime "${seconds}s" ${parallel[@]+"${parallel[@]}"} 2>&1 | tee "$log" || status=1
  # Progress lines look like: fuzz: elapsed: 3s, execs: 12345 (4115/sec), new interesting: ...
  awk -v target="$target" -v total="$seconds" '
    /^fuzz: elapsed: [0-9hms]+, execs: [0-9]+/ {
      # the elapsed time is 59s, then 1m0s, 1h2m3s
      t = $3; sub(/,$/, "", t); secs = 0
      if (match(t, /[0-9]+h/)) { secs += substr(t, RSTART, RLENGTH - 1) * 3600 }
      if (match(t, /[0-9]+m/)) { secs += substr(t, RSTART, RLENGTH - 1) * 60 }
      if (match(t, /[0-9]+s/)) { secs += substr(t, RSTART, RLENGTH - 1) }
      e = $5
      n++; at[n] = secs; ex[n] = e + 0
    }
    END {
      if (n < 2) { printf "%s: too few progress lines (%d) to judge the rate\n", target, n; exit 1 }
      last = at[n]; cut = total / 5
      # executions at the end of the first fifth and at the start of the last fifth
      for (i = 1; i <= n; i++) { if (at[i] <= cut) first = i; if (at[i] <= last - cut) late = i }
      if (first == 0) first = 1
      r1 = (at[first] > 0) ? ex[first] / at[first] : 0
      r2 = (last > at[late]) ? (ex[n] - ex[late]) / (last - at[late]) : 0
      printf "%s: %d executions in %ds; %.0f/sec in the first fifth, %.0f/sec in the last fifth\n", target, ex[n], last, r1, r2
      if (ex[n] == ex[late] || r2 < r1 / 10) { printf "%s: STALLED (rate fell or no execution in the last fifth)\n", target; exit 1 }
    }' "$log" || status=1
done
exit "$status"
