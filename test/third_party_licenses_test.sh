#!/bin/sh
# Unit tests for scripts/third-party-licenses.sh.
#
# The script's job is an ORDER and a SHAPE: every file under the save
# trees, byte-sorted by path, each under a heading naming it. A wrong
# order would make the committed file churn between machines and fail
# the CI drift check for no reason, so the order is what most of these
# cases pin, including under a locale whose collation disagrees with
# byte order.
set -u

root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
script="${root}/scripts/third-party-licenses.sh"
failures=0

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

pass() { echo "ok   - $1"; }
fail() { echo "FAIL - $1"; failures=$((failures + 1)); }

# Fixture created in an order that is NOT sorted, so a script that
# emitted directory order would show it. Zeta and alpha sort the
# opposite way under byte order (Z before a) and under en_US collation
# (a before Z), which is what the locale case below turns on.
mkdir -p "${work}/save/Zeta.example/last" "${work}/save/alpha.example/first" \
    "${work}/save/m.example/mid"
printf 'LAST LICENCE\n' > "${work}/save/Zeta.example/last/LICENSE"
printf 'MID NOTICE\n' > "${work}/save/m.example/mid/NOTICE"
printf 'MID LICENCE\n' > "${work}/save/m.example/mid/LICENSE"
printf 'FIRST LICENCE\n' > "${work}/save/alpha.example/first/LICENSE"

if sh "${script}" "${work}/out.txt" "${work}/save"; then
    pass "runs against a fixture tree"
else
    fail "runs against a fixture tree"
fi

want="Zeta.example/last/LICENSE alpha.example/first/LICENSE m.example/mid/LICENSE m.example/mid/NOTICE "
order="$(grep -E '^[A-Za-z]+\.example/' "${work}/out.txt" | tr '\n' ' ')"
if [ "${order}" = "${want}" ]; then
    pass "headings are in byte order"
else
    fail "headings are in byte order (got: ${order})"
fi

# The same tree under a locale that collates a before Z. Where that
# locale is not installed the shell falls back to C and the case
# cannot fail, so it is a real check only on machines that have it.
LC_ALL=en_US.UTF-8 sh "${script}" "${work}/out-locale.txt" "${work}/save"
order="$(grep -E '^[A-Za-z]+\.example/' "${work}/out-locale.txt" | tr '\n' ' ')"
if [ "${order}" = "${want}" ]; then
    pass "byte order holds under en_US collation"
else
    fail "byte order holds under en_US collation (got: ${order})"
fi

# Every file's contents are present.
for text in 'FIRST LICENCE' 'MID LICENCE' 'MID NOTICE' 'LAST LICENCE'; do
    if grep -q "^${text}\$" "${work}/out.txt"; then
        pass "carries ${text}"
    else
        fail "carries ${text}"
    fi
done

# A second run over the same tree is byte-identical.
sh "${script}" "${work}/out2.txt" "${work}/save"
if cmp -s "${work}/out.txt" "${work}/out2.txt"; then
    pass "output is deterministic"
else
    fail "output is deterministic"
fi

# Two trees are unioned: a file in both appears once, a file in one
# appears, and the first tree's copy is the one used.
mkdir -p "${work}/save2/m.example/mid" "${work}/save2/w.example/only"
printf 'SECOND COPY\n' > "${work}/save2/m.example/mid/LICENSE"
printf 'WINDOWS ONLY\n' > "${work}/save2/w.example/only/LICENSE"
sh "${script}" "${work}/out-union.txt" "${work}/save" "${work}/save2"
if [ "$(grep -c '^m.example/mid/LICENSE$' "${work}/out-union.txt")" = 1 ]; then
    pass "a file in both trees appears once"
else
    fail "a file in both trees appears once"
fi
if grep -q '^WINDOWS ONLY$' "${work}/out-union.txt"; then
    pass "a file in the second tree only appears"
else
    fail "a file in the second tree only appears"
fi
if grep -q '^MID LICENCE$' "${work}/out-union.txt" &&
    ! grep -q '^SECOND COPY$' "${work}/out-union.txt"; then
    pass "the first tree's copy wins"
else
    fail "the first tree's copy wins"
fi

# A file with no trailing newline still gets a blank line before the
# next heading.
mkdir -p "${work}/save3/a.example/x" "${work}/save3/b.example/y"
printf 'NO NEWLINE' > "${work}/save3/a.example/x/LICENSE"
printf 'NEXT\n' > "${work}/save3/b.example/y/LICENSE"
sh "${script}" "${work}/out-nl.txt" "${work}/save3"
if grep -A2 '^NO NEWLINE$' "${work}/out-nl.txt" | sed -n '2p' | grep -q '^$'; then
    pass "a file without a trailing newline is followed by a blank line"
else
    fail "a file without a trailing newline is followed by a blank line"
fi

# No temp file is left beside the output on success.
if [ ! -e "${work}/out.txt.tmp" ]; then
    pass "temp file removed"
else
    fail "temp file removed"
fi

# A failure mid-write removes the temp file and leaves the output
# untouched. An unreadable licence is the failure; skipped as root,
# who can read anything.
if [ "$(id -u)" != 0 ]; then
    mkdir -p "${work}/save4/a.example/x"
    printf 'SECRET\n' > "${work}/save4/a.example/x/LICENSE"
    chmod 000 "${work}/save4/a.example/x/LICENSE"
    printf 'OLD\n' > "${work}/out-fail.txt"
    if sh "${script}" "${work}/out-fail.txt" "${work}/save4" 2>/dev/null; then
        fail "an unreadable file fails the run"
    else
        pass "an unreadable file fails the run"
    fi
    if [ "$(cat "${work}/out-fail.txt")" = OLD ] &&
        [ ! -e "${work}/out-fail.txt.tmp" ]; then
        pass "a failed run leaves the output and no temp file"
    else
        fail "a failed run leaves the output and no temp file"
    fi
    chmod 644 "${work}/save4/a.example/x/LICENSE"
fi

# A missing tree fails and writes nothing.
if sh "${script}" "${work}/out3.txt" "${work}/absent" 2>/dev/null; then
    fail "missing save dir fails"
else
    pass "missing save dir fails"
fi
if [ ! -e "${work}/out3.txt" ]; then
    pass "missing save dir writes no output"
else
    fail "missing save dir writes no output"
fi

# An empty tree fails: a dependency set with no licences is a broken
# save, not a clean one.
mkdir -p "${work}/empty"
if sh "${script}" "${work}/out4.txt" "${work}/empty" 2>/dev/null; then
    fail "empty save dir fails"
else
    pass "empty save dir fails"
fi

# Too few arguments is a usage error, exit 2.
sh "${script}" "${work}/out5.txt" 2>/dev/null
if [ "$?" -eq 2 ]; then
    pass "too few arguments exits 2"
else
    fail "too few arguments exits 2"
fi

if [ "${failures}" -ne 0 ]; then
    echo "${failures} third-party-licenses test(s) failed"
    exit 1
fi
echo "all third-party-licenses tests passed"
