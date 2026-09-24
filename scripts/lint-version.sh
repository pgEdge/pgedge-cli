#!/usr/bin/env bash
# Warn when the locally installed golangci-lint is not the version CI
# pins in .golangci-version.
#
# It warns rather than fails on purpose. CI reads the same file, so the
# build is deterministic either way; failing here would stop a developer
# linting at all over a patch-version difference, which is a worse trade
# than a line of output. What it prevents is the silent case — a local
# `make lint` passing clean while CI's pinned version reports issues, or
# the reverse, with nothing saying the two are different.
#
# Exits 0 in every case, including when golangci-lint is absent: the
# lint target itself is what should fail then, with its own message.
set -uo pipefail

# CDPATH='' because `cd` with a relative path echoes the resolved
# directory to stdout when CDPATH is set, which lands inside this
# command substitution and garbles root — turning the check into one
# that can never fire. The other three suites guard the same way.
root="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
pin_file="${root}/.golangci-version"

if [ ! -f "${pin_file}" ]; then
    echo "warning: ${pin_file} is missing; cannot check the" \
        "golangci-lint version CI will use" >&2
    exit 0
fi

pinned="$(tr -d '[:space:]' < "${pin_file}")"
if [ -z "${pinned}" ]; then
    echo "warning: .golangci-version is empty" >&2
    exit 0
fi

if ! command -v golangci-lint >/dev/null 2>&1; then
    exit 0
fi

# `golangci-lint has version 2.8.0 built with go1.25.8 from ...`
local_version="$(golangci-lint --version 2>/dev/null |
    sed -n 's/.*has version \([0-9][0-9.]*\).*/\1/p')"

if [ -z "${local_version}" ]; then
    echo "warning: could not parse the local golangci-lint version;" \
        "CI will use ${pinned}" >&2
    exit 0
fi

if [ "${local_version}" != "${pinned}" ]; then
    echo "warning: golangci-lint ${local_version} is installed but CI" \
        "pins ${pinned} (.golangci-version)." >&2
    echo "         A clean run here does not guarantee a clean run in" \
        "CI, or the reverse." >&2
fi

exit 0
