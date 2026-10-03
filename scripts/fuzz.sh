#!/usr/bin/env bash
# Runs the fuzz targets for the HCL and JSON readers (scripts/fuzz/fuzz_test.go).
# Outside the default test run and outside the coverage gate: the targets skip
# unless MODELSPEC_FUZZ is set, which this script sets.
#
#   scripts/fuzz.sh [seconds-per-target]     (default 120)
#
# A crash (including a stack overflow in the HCL parser, which is fatal) leaves its
# input under scripts/fuzz/testdata/fuzz/; copy it into a unit test and fix the cause.
set -euo pipefail
seconds="${1:-120}"
cd "$(dirname "$0")/.."
for target in FuzzHCL FuzzJSON; do
  echo "== $target for ${seconds}s"
  MODELSPEC_FUZZ=1 go test ./scripts/fuzz -run '^$' -fuzz "^${target}\$" -fuzztime "${seconds}s"
done
