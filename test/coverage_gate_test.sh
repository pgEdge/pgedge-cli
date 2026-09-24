#!/bin/sh
# Tests for scripts/coverage-gate.sh. Focus: its fail-closed contract —
# a gate that cannot compute a coverage number must ABORT, never pass
# silently. The inline ci.yml gate this replaced exited 0 when
# `go tool cover` produced no total line, because an unset pipefail let
# the empty result through and `bc` then failed inside an `if`.
#
# `go` is stubbed on PATH so the cover output is fixture-controlled; the
# stub prints $STUB_COVER_OUT and exits $STUB_COVER_RC.
#
# Exit codes are captured with `|| rc=$?`, which is exempt from errexit.
# Do not use set +e/set -e around a function call here: `set` is not
# function-scoped, so a set -e inside a helper re-arms errexit for the
# caller and the first non-zero return kills the run.
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
GATE="${SCRIPT_DIR}/../scripts/coverage-gate.sh"

if [ ! -x "$GATE" ]; then
    echo "FAIL - ${GATE} is missing or not executable" >&2
    exit 1
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir -p "${WORK}/bin"
cat > "${WORK}/bin/go" <<'STUB'
#!/bin/sh
# Stub for: go tool cover -func=FILE
if [ -n "${STUB_COVER_OUT:-}" ]; then
    cat "$STUB_COVER_OUT"
    exit "${STUB_COVER_RC:-0}"
fi
exit 0
STUB
chmod +x "${WORK}/bin/go"
PATH="${WORK}/bin:${PATH}"
export PATH

fails=0
check() { # description  actual  want
    if [ "$2" -eq "$3" ]; then
        echo "ok   - $1"
    else
        echo "FAIL - $1 (got $2, want $3)"
        fails=$((fails + 1))
    fi
}

RAW="${WORK}/coverage.raw.out"
OUT="${WORK}/coverage.out"
LOG="${WORK}/gate.log"

# A raw profile carrying both generated-API lines (must be filtered
# out) and a hand-written line (must survive the filter).
write_raw() {
    {
        echo 'mode: atomic'
        echo 'github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api/client.gen.go:1.1,2.2 1 0'
        echo 'github.com/pgEdge/pgedge-cli/internal/controlplane/api/client.gen.go:1.1,2.2 1 0'
        echo 'github.com/pgEdge/pgedge-cli/internal/cli/root.go:1.1,2.2 1 1'
    } > "$RAW"
}

cover_fixture() { # basename  contents
    printf '%s\n' "$2" > "${WORK}/$1"
    echo "${WORK}/$1"
}

TOTAL_OK=$(cover_fixture ok.txt 'total:	(statements)	91.0%')
TOTAL_LOW=$(cover_fixture low.txt 'total:	(statements)	89.5%')
TOTAL_EDGE=$(cover_fixture edge.txt 'total:	(statements)	90.0%')
NO_TOTAL=$(cover_fixture nototal.txt 'github.com/x/y.go:1:	Foo	100.0%')
NAN_TOTAL=$(cover_fixture nan.txt 'total:	(statements)	n/a%')
EMPTY_COVER=$(cover_fixture empty.txt '')

# Runs the gate against a fresh raw profile. Never touches errexit.
run_gate() { # cover_fixture  [cover_exit_code]
    write_raw
    STUB_COVER_OUT="$1" STUB_COVER_RC="${2:-0}" \
        "$GATE" "$RAW" "$OUT" 90.0 >"$LOG" 2>&1
}

nonzero() { if [ "$1" -ne 0 ]; then echo 1; else echo 0; fi; }

# T1 (the original defect): no total line must FAIL CLOSED.
rc=0; run_gate "$NO_TOTAL" || rc=$?
check "no total line fails closed" "$(nonzero "$rc")" 1

# T2: entirely empty cover output must FAIL CLOSED.
rc=0; run_gate "$EMPTY_COVER" || rc=$?
check "empty cover output fails closed" "$(nonzero "$rc")" 1

# T3: a non-numeric total must FAIL CLOSED.
rc=0; run_gate "$NAN_TOTAL" || rc=$?
check "non-numeric total fails closed" "$(nonzero "$rc")" 1

# T4: `go tool cover` exiting non-zero must FAIL CLOSED.
rc=0; run_gate "$TOTAL_OK" 3 || rc=$?
check "cover tool failure fails closed" "$(nonzero "$rc")" 1

# T5 (negative control): a genuine below-threshold number fails.
rc=0; run_gate "$TOTAL_LOW" || rc=$?
check "89.5% is rejected" "$rc" 1

# T6 (positive control): a genuine above-threshold number passes.
rc=0; run_gate "$TOTAL_OK" || rc=$?
check "91.0% is accepted" "$rc" 0

# T7: the threshold is inclusive — exactly 90.0 passes.
rc=0; run_gate "$TOTAL_EDGE" || rc=$?
check "90.0% is accepted at the boundary" "$rc" 0

# T8: a missing raw profile must FAIL CLOSED, not be read as 100%.
rc=0
STUB_COVER_OUT="$TOTAL_OK" "$GATE" "${WORK}/nope.out" "$OUT" 90.0 \
    >/dev/null 2>&1 || rc=$?
check "missing raw profile fails closed" "$(nonzero "$rc")" 1

# T9: a raw profile whose every line is filtered away must FAIL CLOSED.
{
    echo 'mode: atomic'
    echo 'github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api/c.gen.go:1.1,2.2 1 0'
} > "${WORK}/allfiltered.raw"
rc=0
STUB_COVER_OUT="$TOTAL_OK" "$GATE" "${WORK}/allfiltered.raw" "$OUT" 90.0 \
    >/dev/null 2>&1 || rc=$?
check "fully-filtered profile fails closed" "$(nonzero "$rc")" 1

# T10: the filter drops generated API packages and keeps the rest.
rc=0; run_gate "$TOTAL_OK" || rc=$?
kept=$(grep -c 'internal/cli/root.go' "$OUT" || true)
byoc_dropped=$(grep -c 'internal/starfleet/byoc/api/' "$OUT" || true)
cp_dropped=$(grep -c 'internal/controlplane/api/' "$OUT" || true)
check "filter keeps hand-written packages" "$kept" 1
check "filter drops internal/starfleet/byoc/api" "$byoc_dropped" 0
check "filter drops internal/controlplane/api" "$cp_dropped" 0

# T11: the computed number reaches the log, so CI shows what was gated.
rc=0; run_gate "$TOTAL_OK" || rc=$?
check "log reports the computed coverage" \
    "$(grep -c '91.0' "$LOG" || true)" 1

# T12/T13: the DEFAULT threshold, exercised with no third argument.
#
# Every check above passes 90.0 explicitly, so none of them touches the
# default. That was harmless while ci.yml named the number too; it is not
# now. Both `make test` and CI call this script with no arguments, so
# THRESHOLD="${3:-90.0}" is the only record of what the project enforces,
# and an edit lowering it would go unnoticed everywhere at once.
rc=0; write_raw
STUB_COVER_OUT="$TOTAL_LOW" "$GATE" "$RAW" "$OUT" >/dev/null 2>&1 || rc=$?
check "89.5% is rejected by the default threshold" "$rc" 1

# The paired positive control: without it, a default accidentally set
# absurdly high would also reject 89.5% and look like a pass above.
rc=0; write_raw
STUB_COVER_OUT="$TOTAL_EDGE" "$GATE" "$RAW" "$OUT" >/dev/null 2>&1 || rc=$?
check "90.0% is accepted by the default threshold" "$rc" 0

if [ "$fails" -ne 0 ]; then
    echo "${fails} test(s) failed" >&2
    exit 1
fi
echo "all coverage-gate tests passed"
