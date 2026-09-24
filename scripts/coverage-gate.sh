#!/bin/sh
# Coverage gate. Filters the generated API clients out of a raw Go
# coverage profile, then fails if total coverage is below a threshold.
#
#   usage: coverage-gate.sh [raw_profile] [filtered_profile] [threshold]
#   defaults:               coverage.raw.out coverage.out 90.0
#
# The gate is FAIL-CLOSED: any step that cannot produce a coverage
# number aborts non-zero. It must never be possible to satisfy this
# gate without a number having actually been computed and compared.
# The inline `bash -e` version this replaced could: with no pipefail an
# empty result reached `bc`, and bc's parse error inside an `if` was
# swallowed, so the step exited 0 having compared nothing.
#
# Generated clients are excluded because Go counts untested packages
# since 1.25 and generated code would put the threshold out of reach.
#
# This is the ONLY filter applied to the profile. The Makefile's `test`
# target used to carry a second copy, which meant a build gate could only
# compare two spellings — and a filter written any other way dropped a
# hand-written package's lines silently (#224). Both `make test` and CI
# now call this script with no arguments, so the pattern and the
# threshold each exist once. This filter must be a function of the
# IMPORT PATH alone. TestCoverageFilterDropsExactlyTheGeneratedPackages
# holds that by running the script and comparing which lines survive,
# rather than by reading how the filter is written -- but it samples the
# numeric fields rather than exhausting them, so a filter keyed on a
# shape it does not sample can still escape. Do not add a second filter
# here on the strength of the gate staying green.
set -eu

GENERATED='internal/(starfleet/(account|byoc|managed)|controlplane)/api/'

RAW="${1:-coverage.raw.out}"
FILTERED="${2:-coverage.out}"
THRESHOLD="${3:-90.0}"

die() {
    echo "coverage-gate: $1" >&2
    exit 1
}

[ -f "$RAW" ] || die "raw profile ${RAW} does not exist"
[ -s "$RAW" ] || die "raw profile ${RAW} is empty"

set +e
grep -Ev "$GENERATED" "$RAW" > "$FILTERED"
grep_rc=$?
set -e
[ "$grep_rc" -lt 2 ] || die "could not filter ${RAW}"

# A profile is more than its `mode:` header — a filtered file holding
# only that header carries no coverage records at all, so treat it as
# empty rather than letting it reach the comparison.
DATA_LINES=$(grep -cv '^mode:' "$FILTERED" || true)
[ "${DATA_LINES:-0}" -gt 0 ] || \
    die "${FILTERED} holds no coverage records after filtering ${GENERATED}"

FUNCS=$(mktemp)
trap 'rm -f "$FUNCS"' EXIT

go tool cover -func="$FILTERED" > "$FUNCS" || \
    die "go tool cover -func=${FILTERED} failed"

COVERAGE=$(awk '$1 == "total:" { gsub(/%/, "", $NF); print $NF; exit }' \
    "$FUNCS")

case "$COVERAGE" in
    '')
        die "no 'total:' line in go tool cover output for ${FILTERED}"
        ;;
    *[!0-9.]*)
        die "coverage value '${COVERAGE}' is not a number"
        ;;
esac

echo "Total coverage: ${COVERAGE}% (threshold ${THRESHOLD}%)"

if awk -v c="$COVERAGE" -v t="$THRESHOLD" \
    'BEGIN { exit !(c + 0 < t + 0) }'; then
    die "coverage ${COVERAGE}% is below the ${THRESHOLD}% threshold"
fi
