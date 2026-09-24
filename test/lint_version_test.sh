#!/bin/sh
# Unit tests for scripts/lint-version.sh.
#
# The script's healthy state is SILENCE, which is indistinguishable
# from a script that does nothing — the same argument the coverage gate
# makes for living in a script rather than inline. So every branch is
# asserted here, including the two that must stay quiet.
#
# It must exit 0 in every case: `make lint` calls it first, and a
# non-zero would stop the lint it is only meant to annotate.
set -u

root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
script="${root}/scripts/lint-version.sh"
failures=0

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

# Runs the script against a throwaway repo root holding the given pin,
# with a stub golangci-lint reporting the given version. An empty
# stub_version means "no golangci-lint on PATH at all".
#
# The script derives its root from its own location, so the stub tree
# gets a copy of the script in a scripts/ dir beside the pin file.
run_case() {
    pin_contents="$1"
    stub_version="$2"

    rm -rf "${work}/case"
    mkdir -p "${work}/case/scripts" "${work}/case/bin"
    cp "${script}" "${work}/case/scripts/lint-version.sh"
    chmod +x "${work}/case/scripts/lint-version.sh"

    if [ "${pin_contents}" != "__MISSING__" ]; then
        printf '%s' "${pin_contents}" > "${work}/case/.golangci-version"
    fi

    if [ -n "${stub_version}" ]; then
        cat > "${work}/case/bin/golangci-lint" <<STUB
#!/bin/sh
echo "golangci-lint has version ${stub_version} built with go1.25.8"
STUB
        chmod +x "${work}/case/bin/golangci-lint"
    fi

    PATH="${work}/case/bin:/usr/bin:/bin" \
        "${work}/case/scripts/lint-version.sh" 2>&1
}

expect() {
    name="$1"
    pin="$2"
    stub="$3"
    want="$4" # substring, or "" to require NO output

    out="$(run_case "${pin}" "${stub}")"
    rc=$?

    if [ "${rc}" -ne 0 ]; then
        echo "FAIL - ${name}: exit ${rc}, want 0"
        failures=$((failures + 1))
        return
    fi

    if [ -z "${want}" ]; then
        if [ -n "${out}" ]; then
            echo "FAIL - ${name}: want silence, got: ${out}"
            failures=$((failures + 1))
            return
        fi
    else
        case "${out}" in
            *"${want}"*) ;;
            *)
                echo "FAIL - ${name}: want ${want}, got: ${out}"
                failures=$((failures + 1))
                return
                ;;
        esac
    fi
    echo "ok   - ${name}"
}

expect "matching version is silent"          "2.8.0" "2.8.0" ""
# Phrases, not bare numbers: both numbers appear either way round, so a
# swapped message — telling you to install the version you already have
# — passed a substring check on the digits alone.
expect "drift names what is installed"       "2.8.0" "2.9.1" "2.9.1 is installed"
expect "drift names what CI pins"            "2.8.0" "2.9.1" "pins 2.8.0"
expect "missing pin file warns"              "__MISSING__" "2.8.0" "is missing"
expect "empty pin file warns"                ""      "2.8.0" "is empty"
expect "absent golangci-lint is silent"      "2.8.0" ""      ""
expect "whitespace in the pin is tolerated"  "  2.8.0
"                                                    "2.8.0" ""

# A stub that prints nothing parseable must warn rather than compare
# against an empty string — which would otherwise read as a drift from
# "" and print a nonsense message.
rm -rf "${work}/case"
mkdir -p "${work}/case/scripts" "${work}/case/bin"
cp "${script}" "${work}/case/scripts/lint-version.sh"
chmod +x "${work}/case/scripts/lint-version.sh"
printf '2.8.0' > "${work}/case/.golangci-version"
cat > "${work}/case/bin/golangci-lint" <<'STUB'
#!/bin/sh
echo "something else entirely"
exit 3
STUB
chmod +x "${work}/case/bin/golangci-lint"
out="$(PATH="${work}/case/bin:/usr/bin:/bin" \
    "${work}/case/scripts/lint-version.sh" 2>&1)"
rc=$?
if [ "${rc}" -ne 0 ]; then
    echo "FAIL - unparseable version: exit ${rc}, want 0"
    failures=$((failures + 1))
else
    case "${out}" in
        *"could not parse"*) echo "ok   - unparseable version warns" ;;
        *)
            echo "FAIL - unparseable version: got: ${out}"
            failures=$((failures + 1))
            ;;
    esac
fi

# The pre-commit hook builds its own golangci-lint from `rev`, so it is
# a third record of the version that lint-version.sh structurally cannot
# see — it inspects PATH, and that binary never lands there. Nothing but
# this assertion keeps it in step, and the review that caught it drifting
# to v2.1.6 is the argument for gating it rather than commenting it.
pin="$(tr -d '[:space:]' < "${root}/.golangci-version")"
hook_rev="$(awk '
    /repo: https:\/\/github.com\/golangci\/golangci-lint$/ { found = 1; next }
    found && /rev:/ { gsub(/[[:space:]]|rev:|v/, "", $0); print; exit }
' "${root}/.pre-commit-config.yaml")"

if [ -z "${hook_rev}" ]; then
    echo "FAIL - pre-commit rev: could not read it from .pre-commit-config.yaml"
    failures=$((failures + 1))
elif [ "${hook_rev}" != "${pin}" ]; then
    echo "FAIL - pre-commit rev: v${hook_rev} but .golangci-version is ${pin}"
    failures=$((failures + 1))
else
    echo "ok   - pre-commit rev matches .golangci-version"
fi

if [ "${failures}" -ne 0 ]; then
    echo "${failures} lint-version test(s) failed"
    exit 1
fi
echo "all lint-version tests passed"
