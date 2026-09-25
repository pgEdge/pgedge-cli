#!/bin/sh
# Tests for install.sh's checksum verification, PATH warning and
# version resolution.
# Sources install.sh as a library (PGEDGE_INSTALL_SH_LIB=1) so the
# functions run without the installer body. Checksum focus: the
# fail-closed contract — no sha256 tool must ABORT,
# never skip verification.
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
INSTALL_SH="${SCRIPT_DIR}/../install.sh"

# shellcheck disable=SC1090
PGEDGE_INSTALL_SH_LIB=1 . "$INSTALL_SH"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fails=0
check() { # description  actual_exit  want_exit
    if [ "$2" -eq "$3" ]; then
        echo "ok   - $1"
    else
        echo "FAIL - $1 (exit $2, want $3)"
        fails=$((fails + 1))
    fi
}

ARCHIVE="pgedge_1.2.3_linux_amd64.tar.gz"
FILE="${WORK}/${ARCHIVE}"
printf 'release-bytes\n' > "$FILE"
GOOD=$(sha256_of "$FILE")
CHECKSUMS="${WORK}/checksums.txt"
printf '%s  %s\n' "$GOOD" "$ARCHIVE" > "$CHECKSUMS"

# T1: sha256_of produces a real 64-hex-char digest.
case "$GOOD" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f]*) len=${#GOOD} ;;
    *) len=0 ;;
esac
check "sha256_of returns a 64-char hex digest" "$len" 64

# T2: sha256_of fails (non-zero, no output) when no tool is on PATH.
set +e
OUT=$(PATH='' sha256_of "$FILE" 2>/dev/null); rc=$?
set -e
check "sha256_of exits non-zero with no tool" "$( [ $rc -ne 0 ] && echo 1 || echo 0 )" 1
check "sha256_of prints nothing with no tool" "$( [ -z "$OUT" ] && echo 1 || echo 0 )" 1

# T3: verify_checksum passes on a matching digest.
set +e
verify_checksum "$FILE" "$ARCHIVE" "$CHECKSUMS" >/dev/null 2>&1; rc=$?
set -e
check "verify_checksum passes on match" "$rc" 0

# T4: verify_checksum fails on a mismatched digest.
BADSUMS="${WORK}/bad.txt"
printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$ARCHIVE" > "$BADSUMS"
set +e
verify_checksum "$FILE" "$ARCHIVE" "$BADSUMS" >/dev/null 2>&1; rc=$?
set -e
check "verify_checksum fails on mismatch" "$( [ $rc -ne 0 ] && echo 1 || echo 0 )" 1

# T5: verify_checksum fails when the archive has no checksum entry.
set +e
verify_checksum "$FILE" "missing.tar.gz" "$CHECKSUMS" >/dev/null 2>&1; rc=$?
set -e
check "verify_checksum fails on missing entry" "$( [ $rc -ne 0 ] && echo 1 || echo 0 )" 1

# T6: goreleaser's sboms: stanza adds a
# "<archive>.sbom.json" sidecar line to checksums.txt alongside the
# archive's own line. An unanchored `grep "$archive_name"` matches
# BOTH lines (the sidecar name contains the archive name as a
# substring), so $expected becomes two newline-separated hashes and
# every install fails with a checksum mismatch. The lookup must be
# anchored on the exact filename field.
SBOMSUMS="${WORK}/sbom.txt"
{
    printf '%s  %s\n' "$GOOD" "$ARCHIVE"
    printf '%s  %s.sbom.json\n' \
        "1111111111111111111111111111111111111111111111111111111111111111" \
        "$ARCHIVE"
} > "$SBOMSUMS"
set +e
verify_checksum "$FILE" "$ARCHIVE" "$SBOMSUMS" >/dev/null 2>&1; rc=$?
set -e
check "verify_checksum ignores a .sbom.json sidecar entry" "$rc" 0

# T7: with no sha256 tool, verify_checksum FAILS CLOSED
# rather than skipping. Shadow sha256_of to simulate no tool present.
sha256_of() { return 1; }
set +e
verify_checksum "$FILE" "$ARCHIVE" "$CHECKSUMS" >/dev/null 2>&1; rc=$?
set -e
check "verify_checksum fails closed with no sha256 tool" "$( [ $rc -ne 0 ] && echo 1 || echo 0 )" 1

# T8: warn_if_not_on_path prints nothing when install_dir is already
# on PATH.
DIR_ON_PATH="/already/on/path"
set +e
OUT=$(PATH="/usr/bin:${DIR_ON_PATH}:/bin" warn_if_not_on_path "$DIR_ON_PATH" 2>&1); rc=$?
set -e
check "warn_if_not_on_path is silent when dir is on PATH" \
    "$( [ -z "$OUT" ] && echo 1 || echo 0 )" 1
check "warn_if_not_on_path returns 0 when dir is on PATH" "$rc" 0

# T9: warn_if_not_on_path prints a two-line warning to stderr,
# including the exact export line to add, when install_dir is
# absent from PATH.
DIR_OFF_PATH="/not/on/path"
set +e
OUT=$(PATH="/usr/bin:/bin" warn_if_not_on_path "$DIR_OFF_PATH" 2>&1); rc=$?
set -e
LINES=$(printf '%s\n' "$OUT" | wc -l | tr -d ' ')
check "warn_if_not_on_path returns 0 when dir is off PATH" "$rc" 0
check "warn_if_not_on_path prints two lines when dir is off PATH" \
    "$LINES" 2
case "$OUT" in
    *"not on your PATH"*) has_warning=1 ;;
    *) has_warning=0 ;;
esac
check "warn_if_not_on_path names the missing directory" "$has_warning" 1
EXPECT_EXPORT="export PATH=\"${DIR_OFF_PATH}:\$PATH\""
case "$OUT" in
    *"$EXPECT_EXPORT"*) has_export=1 ;;
    *) has_export=0 ;;
esac
check "warn_if_not_on_path prints the exact export line" "$has_export" 1

# T10: a pinned PGEDGE_VERSION is returned as given, with no API
# call. curl is shadowed to fail, so any call would surface.
# shellcheck disable=SC2329 # called by the sourced resolve_version
curl() { echo "curl called" >&2; return 22; }
set +e
OUT=$(PGEDGE_VERSION=v0.5.0-beta.2 resolve_version 2>&1); rc=$?
set -e
check "resolve_version returns a pinned tag" "$rc" 0
check "resolve_version prints the pinned tag unchanged" \
    "$( [ "$OUT" = "v0.5.0-beta.2" ] && echo 1 || echo 0 )" 1

# T11: a pin that is not a release tag is refused before any
# download, so a typo cannot fall through to the newest release.
for bad in 0.5.0 v0.5 latest "v0.5.0 beta"; do
    set +e
    OUT=$(PGEDGE_VERSION="$bad" resolve_version 2>&1); rc=$?
    set -e
    check "resolve_version refuses PGEDGE_VERSION='${bad}'" \
        "$( [ $rc -ne 0 ] && echo 1 || echo 0 )" 1
    case "$OUT" in
        *"curl called"*) called=1 ;;
        *) called=0 ;;
    esac
    check "resolve_version makes no API call for '${bad}'" "$called" 0
done

# T12: unset, the newest release's tag comes from the API, which
# pretty-prints one field per line (measured 2026-09-25).
# shellcheck disable=SC2329 # called by the sourced resolve_version
curl() {
    printf '[\n  {\n    "url": "x",\n    "tag_name": "v9.8.7-rc.1",\n    "name": "n"\n  }\n]\n'
}
set +e
OUT=$(unset PGEDGE_VERSION; resolve_version 2>/dev/null); rc=$?
set -e
check "resolve_version falls back to the API" "$rc" 0
check "resolve_version reads the newest tag" \
    "$( [ "$OUT" = "v9.8.7-rc.1" ] && echo 1 || echo 0 )" 1

# T13: an unreachable API is an error, not an empty version.
# shellcheck disable=SC2329 # called by the sourced resolve_version
curl() { return 22; }
set +e
OUT=$(unset PGEDGE_VERSION; resolve_version 2>/dev/null); rc=$?
set -e
check "resolve_version fails when the API is unreachable" \
    "$( [ $rc -ne 0 ] && echo 1 || echo 0 )" 1
unset -f curl

if [ "$fails" -ne 0 ]; then
    echo "${fails} test(s) failed" >&2
    exit 1
fi
echo "all install.sh checksum tests passed"
