#!/bin/sh
# Concatenate one or more `go-licenses save` trees into one
# THIRD_PARTY_LICENSES file: every licence and NOTICE text, each under a
# heading naming the package it belongs to, in a fixed order so the
# file only changes when the dependency set does.
#
# Usage: third-party-licenses.sh <out_file> <save_dir>...
#
# Several trees, because the binary is built for three operating
# systems and cobra pulls in a Windows-only module: the trees are
# unioned by relative path, and where two carry the same path the
# first wins (they are copies of the same module version). go-licenses
# save is run by the Makefile, not here, so this half can be tested
# against fixture directories without the network or the module cache.
set -eu

if [ "$#" -lt 2 ]; then
    echo "usage: $0 <out_file> <save_dir>..." >&2
    exit 2
fi
out="$1"
shift

for d in "$@"; do
    if [ ! -d "${d}" ]; then
        echo "error: ${d} is not a directory" >&2
        exit 1
    fi
done

# find prints paths in directory order, which differs between
# filesystems; LC_ALL=C sort makes the order byte-wise and identical
# on every machine and runner, whatever locale the shell inherits.
files="$(for d in "$@"; do
    (cd "${d}" && find . -type f)
done | LC_ALL=C sort -u)"
if [ -z "${files}" ]; then
    echo "error: no files under $*" >&2
    exit 1
fi

# Locate the first tree holding rel; the loop above guarantees one does.
first_holder() {
    for d in "$@"; do
        if [ -f "${d}/${rel}" ]; then
            echo "${d}"
            return
        fi
    done
}

tmp="${out}.tmp"
trap 'rm -f "${tmp}"' EXIT
{
    echo "Third-party licences"
    echo
    echo "pgedge is built from the open-source packages below. Each"
    echo "package's licence text, and its NOTICE file where it ships one,"
    echo "is reproduced as the package ships it. The heading is the"
    echo "package path and the file's name within it."
    echo
    printf '%s\n' "${files}" | while IFS= read -r f; do
        rel="${f#./}"
        src="$(first_holder "$@")/${rel}"
        echo "================================================================"
        echo "${rel}"
        echo "================================================================"
        echo
        cat "${src}"
        # A file with no trailing newline would otherwise run its last
        # line into the blank that separates it from the next heading.
        if [ -n "$(tail -c 1 "${src}")" ]; then
            echo
        fi
        echo
    done
} > "${tmp}"
mv "${tmp}" "${out}"
trap - EXIT
