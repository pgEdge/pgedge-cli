#!/bin/sh
# Tests for install.sh's agent-skills offer. Sources install.sh as a
# library (PGEDGE_INSTALL_SH_LIB=1), stubs npx as a function, and
# stands a plain file in for the terminal.
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
INSTALL_SH="${SCRIPT_DIR}/../install.sh"

# shellcheck disable=SC1090
PGEDGE_INSTALL_SH_LIB=1 . "$INSTALL_SH"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# A home with one agent in it, and a working directory with no skills,
# so the offer is made unless a test changes either.
unset CLAUDE_CONFIG_DIR CODEX_HOME XDG_CONFIG_HOME
HOME="${WORK}/home"
mkdir -p "${HOME}/.claude" "${WORK}/proj"
cd "${WORK}/proj"

fails=0
check() { # description  actual  want
    if [ "$2" = "$3" ]; then
        echo "ok   - $1"
    else
        echo "FAIL - $1 (got '$2', want '$3')"
        fails=$((fails + 1))
    fi
}

CALLS="${WORK}/npx-calls"
NPX_RC=0
# shellcheck disable=SC2317,SC2329 # called by the sourced offer_skills
npx() {
    printf '%s\n' "$*" >> "$CALLS"
    stdin=""
    read -r stdin || true
    printf 'stdin=%s\n' "$stdin" >> "$CALLS"
    return "$NPX_RC"
}

TTY="${WORK}/tty"
WANT_CMD="npx -y skills add 'pgEdge/pgedge-cli#v1.2.3' --global"

# run_offer ANSWER: offer_skills with ANSWER on the stand-in terminal.
# Sets OUT, ERR and RC, and clears the call log first. The script's own
# stdin is empty, so npx reading it instead of the terminal shows.
run_offer() {
    : > "$CALLS"
    printf '%s\n' "$1" > "$TTY"
    set +e
    OUT=$(offer_skills v1.2.3 "$TTY" 2>"${WORK}/err" </dev/null); RC=$?
    set -e
    ERR=$(cat "${WORK}/err")
}

for answer in "" y Y yes YES; do
    run_offer "$answer"
    check "answer '${answer}' installs from the terminal" \
        "$(cat "$CALLS")" \
        "-y skills add pgEdge/pgedge-cli#v1.2.3 --global
stdin=${answer}"
    check "answer '${answer}' exits 0" "$RC" 0
done

for answer in n N no later; do
    run_offer "$answer"
    check "answer '${answer}' installs nothing" "$(cat "$CALLS")" ""
    check "answer '${answer}' prints the command" \
        "$(printf '%s\n' "$OUT" | grep -cF "$WANT_CMD")" 1
done

# An empty terminal (EOF) is a no, not the default yes.
: > "$CALLS"; : > "$TTY"
set +e
OUT=$(offer_skills v1.2.3 "$TTY" 2>&1); RC=$?
set -e
check "EOF on the terminal installs nothing" "$(cat "$CALLS")" ""
check "EOF on the terminal exits 0" "$RC" 0

# A failed skills install is reported and never fails the CLI install.
NPX_RC=1
run_offer y
NPX_RC=0
check "failed install exits 0" "$RC" 0
check "failed install says so on stderr" \
    "$(printf '%s\n' "$ERR" | grep -c 'did not install')" 1
check "failed install prints the command on stderr" \
    "$(printf '%s\n' "$ERR" | grep -cF "$WANT_CMD")" 1

# A terminal that cannot be opened gets the command and no prompt.
: > "$CALLS"
set +e
OUT=$(offer_skills v1.2.3 "${WORK}/no-such-tty" 2>&1); RC=$?
set -e
check "unopenable terminal installs nothing" "$(cat "$CALLS")" ""
check "unopenable terminal prints the command" \
    "$(printf '%s\n' "$OUT" | grep -cF "$WANT_CMD")" 1
check "unopenable terminal does not prompt" \
    "$(printf '%s\n' "$OUT" | grep -c 'now?')" 0

# Without npx, the command is printed and nothing is asked.
printf 'y\n' > "$TTY"
set +e
OUT=$(unset -f npx; PATH='' offer_skills v1.2.3 "$TTY" 2>&1); RC=$?
set -e
check "no npx exits 0" "$RC" 0
check "no npx prints the command" \
    "$(printf '%s\n' "$OUT" | grep -cF "$WANT_CMD")" 1
check "no npx does not prompt" \
    "$(printf '%s\n' "$OUT" | grep -c 'now?')" 0

# skills_step asks only when CI is unset and stdout is a terminal.
# Captured stdout is never a terminal, so neither case may prompt.
for ci in 1 ""; do
    : > "$CALLS"; printf 'y\n' > "$TTY"
    OUT=$(CI="$ci" skills_step v1.2.3 "$TTY" 2>&1 </dev/null)
    check "skills_step with CI='${ci}' and captured stdout installs nothing" \
        "$(cat "$CALLS")" ""
    check "skills_step with CI='${ci}' prints the command" \
        "$(printf '%s\n' "$OUT" | grep -cF "$WANT_CMD")" 1
done

# The offer names the agents it found.
run_offer y
check "the offer names the agent it found" \
    "$(printf '%s\n' "$OUT" | grep -c '^Found Claude Code\.$')" 1
mkdir -p "${HOME}/.cursor" "${HOME}/.config/amp"
run_offer y
check "the offer names every agent it found" \
    "$(printf '%s\n' "$OUT" | grep -c '^Found Claude Code, Cursor, Amp\.$')" 1
rm -rf "${HOME}/.cursor" "${HOME}/.config"

# With no agent installed, nothing is asked and the command is printed.
mv "${HOME}/.claude" "${WORK}/claude-away"
run_offer y
mv "${WORK}/claude-away" "${HOME}/.claude"
check "no agent installs nothing" "$(cat "$CALLS")" ""
check "no agent does not prompt" \
    "$(printf '%s\n' "$OUT" | grep -c 'now?')" 0
check "no agent prints the command" \
    "$(printf '%s\n' "$OUT" | grep -cF "$WANT_CMD")" 1

# CLAUDE_CONFIG_DIR and CODEX_HOME move where the agents live.
mv "${HOME}/.claude" "${WORK}/claude-away"
mkdir -p "${WORK}/elsewhere/codex"
OUT=$(CODEX_HOME="${WORK}/elsewhere/codex" detect_agents)
check "CODEX_HOME finds Codex" "$OUT" "Codex"
OUT=$(CLAUDE_CONFIG_DIR="${WORK}/claude-away" detect_agents)
check "CLAUDE_CONFIG_DIR finds Claude Code" "$OUT" "Claude Code"
mv "${WORK}/claude-away" "${HOME}/.claude"

# Skills already in any place an agent loads them from are not offered
# again, even at a terminal: skills_step prints a line and stops.
check "no skills anywhere is not installed" \
    "$(skills_installed && echo yes || echo no)" no
for place in "${HOME}/.agents/skills/pgedge/SKILL.md" \
    "${WORK}/proj/.agents/skills/pgedge/SKILL.md" \
    "${HOME}/.claude/skills/pgedge/SKILL.md" \
    "${HOME}/.claude/plugins/cache/pgedge-cli/pgedge/" \
    "${HOME}/.codex/plugins/cache/pgedge-cli/pgedge/"; do
    mkdir -p "$(dirname "$place")"
    case "$place" in */) mkdir -p "$place" ;; *) : > "$place" ;; esac
    : > "$CALLS"; printf 'y\n' > "$TTY"
    OUT=$(skills_step v1.2.3 "$TTY" 2>&1 </dev/null)
    check "skills in ${place#"$WORK"/} are not offered again" \
        "$OUT" "The pgedge agent skills are already installed."
    check "skills in ${place#"$WORK"/} install nothing" "$(cat "$CALLS")" ""
    rm -rf "${HOME}/.agents" "${WORK}/proj/.agents" \
        "${HOME}/.claude/skills" "${HOME}/.claude/plugins" "${HOME}/.codex"
done

if [ "$fails" -ne 0 ]; then
    echo "${fails} install skills test(s) failed"
    exit 1
fi
