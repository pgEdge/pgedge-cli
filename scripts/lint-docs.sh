#!/usr/bin/env bash
# Runs Vale over the repo's prose (Markdown, llms.txt, Go comments).
#
# Bash, not /bin/sh like this repo's other scripts: the file list below
# is walked NUL-delimited (`git ls-files -z` / `read -r -d ''`) so a
# path containing a space or newline can't split into two entries or
# otherwise corrupt the loop, and POSIX `read` has no `-d` option —
# dash (CI's /bin/sh) rejects it outright.
#
# Two regions must never be linted: the generated command-reference
# blocks between <!-- BEGIN GENERATED --> / <!-- END GENERATED --> in
# llms.txt (machine output `make docs` overwrites, some table lines
# past 400 characters), and the `<!-- doc-gate: ... -->` marker itself
# (the repo's deliberately-wrong-phrase convention for conformance
# tests — see CLAUDE.md and internal/clitest/skills_docs_test.go's
# marker-semantics comment). Only the marker's own HTML-comment span
# is blanked, not the rest of its line or sentence: a doc-gate marker
# excuses one named phrase for the field-claims gate in
# internal/clitest, a completely different check, and real prose
# sharing a line with a marker (as in ROADMAP.md's table rows, where a
# marker sits mid-cell) still needs Vale's rules applied to it. None
# of today's excused phrases (task_id, refresh_token, token_exists,
# server_version, revision_time, patroni_port, postgres_version,
# registered, active, completed, available) collide with the
# PostgreSQL/BannedWords/ChangeMe styles, so leaving them lintable
# does not reintroduce a false failure.
#
# Vale's own BlockIgnores/TokenIgnores directives were tried first and
# do not work here: both only match within a single Markdown block,
# but a generated block always mixes headings, tables and code fences
# (several blocks), and a real doc-gate marker often sits inside a
# table cell (itself a sub-block Vale scans independently of the row
# around it). A regex spanning BEGIN...END or anchored `^...$` around
# doc-gate never lines up with block boundaries in either case, so it
# silently fails to match — proven with planted violations inside a
# GENERATED table and inside a doc-gate-marked table cell, both of
# which still fired. See .vale.ini for the same finding, and the
# vale-prose-linting PR description for the exact probes and output.
#
# So this script does the skip itself, line-based, before Vale ever
# parses the file: it copies every file Vale would lint into a
# throwaway directory, blanking (not deleting — this keeps line
# numbers intact, so a real violation still reports its true line)
# every line in a GENERATED region and exactly the doc-gate marker's
# own span (which may wrap across two or three physical lines — see
# the awk state machine below), then runs `vale` against the copy.
set -eu

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO_ROOT"

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/pgedge-lint-docs.XXXXXX")
trap 'rm -rf "$WORKDIR"' EXIT INT TERM

filter_generated_and_gate() {
	awk '
		BEGIN { ingen = 0; inmarker = 0 }
		/<!-- BEGIN GENERATED:/ { ingen = 1 }
		{
			line = $0
			if (ingen) {
				# Whole generated region: machine output, never
				# prose. Blank the entire line.
				print ""
			} else if (inmarker) {
				# Continuation of a marker that opened on an
				# earlier line and has not closed yet: this line
				# is entirely inside the HTML comment until (and
				# if) "-->" appears.
				p = index(line, "-->")
				if (p > 0) {
					print " " substr(line, p + 3)
					inmarker = 0
				} else {
					print ""
				}
			} else {
				p = index(line, "<!-- doc-gate:")
				if (p > 0) {
					head = substr(line, 1, p - 1)
					rest = substr(line, p)
					q = index(rest, "-->")
					if (q > 0) {
						# Marker opens and closes on this
						# same line: remove just its span,
						# keep the prose on both sides.
						print head " " substr(rest, q + 3)
					} else {
						# Marker opens here but wraps to a
						# later line: keep the prose before
						# it, blank from the opener on.
						print head
						inmarker = 1
					}
				} else {
					print line
				}
			}
		}
		/<!-- END GENERATED -->/ { ingen = 0 }
	'
}

# Everything Vale might touch: its own config, the styles it loads,
# and the file types named in .vale.ini's [formats]/section keys.
# Tracked files plus untracked-but-not-ignored ones (so a file staged
# for this same PR, not yet committed, is still linted) — gitignored
# scratch such as .superpowers/ is correctly never included.
# NUL-delimited throughout so a path with a space or newline in it
# cannot split into two entries or otherwise corrupt the loop.
git ls-files -z --cached --others --exclude-standard \
	-- '.vale.ini' 'styles' '*.md' '*.txt' '*.go' |
	while IFS= read -r -d '' f; do
		mkdir -p "$WORKDIR/$(dirname "$f")"
		case "$f" in
		*.md | *.txt)
			filter_generated_and_gate <"$f" >"$WORKDIR/$f"
			;;
		*)
			cp "$f" "$WORKDIR/$f"
			;;
		esac
	done

cd "$WORKDIR"
exec vale .
