#!/bin/bash
# Installs the pgedge cask with real Homebrew and checks the result.
#
#   usage: cask-install-test.sh local <patched-cask> <dist-dir>
#          cask-install-test.sh tap <version>
#
# local installs a snapshot cask from a throwaway tap, its URLs pointed
# at the archives in dist-dir, so a PR proves the cask installs before
# any tag exists. tap installs the published cask from pgEdge/homebrew-tap
# and expects <version> (no leading v).
#
# Never run this against a Homebrew you care about: it installs and
# uninstalls pgedge, and local mode adds a tap.
set -euo pipefail

export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1

fail() {
    echo "::error::$*" >&2
    exit 1
}

case "${1:-}" in
local)
    [ $# -eq 3 ] || fail "usage: $0 local <patched-cask> <dist-dir>"
    dist=$(cd "$3" && pwd)
    want=$(sed -n 's/^  version "\(.*\)"$/\1/p' "$2")
    [ -n "$want" ] || fail "no version line in $2"
    brew tap-new --no-git pgedge-ci/local >/dev/null
    tap="$(brew --repository pgedge-ci/local)/Casks"
    mkdir -p "$tap"
    # Every release URL becomes the matching local archive; the sha256
    # lines already describe those archives.
    sed -E "s#https://github.com/pgEdge/pgedge-cli/releases/download/[^/]+/#file://$dist/#" \
        "$2" >"$tap/pgedge.rb"
    grep -q "file://$dist/" "$tap/pgedge.rb" || fail "no URL was rewritten"
    ref=pgedge-ci/local/pgedge
    ;;
tap)
    [ $# -eq 2 ] || fail "usage: $0 tap <version>"
    want=$2
    ref=pgedge/tap/pgedge
    ;;
*)
    fail "usage: $0 local <patched-cask> <dist-dir> | tap <version>"
    ;;
esac

# A deprecated stanza still installs, with a warning: that is how
# beta.3's postflight block shipped. Any warning fails, bar the sandbox
# notice a container prints.
brew_clean() {
    "$@" 2>&1 | tee brew.log
    if grep '^Warning:' brew.log | grep -v 'Sandbox unavailable'; then
        fail "Homebrew warned during: $*"
    fi
}

brew_clean brew install --cask "$ref"

prefix=$(brew --prefix)
[ "$(command -v pgedge)" = "$prefix/bin/pgedge" ] ||
    fail "pgedge resolves to $(command -v pgedge), not $prefix/bin/pgedge"

got=$(pgedge version | head -n 1)
[ "$got" = "$want" ] || fail "pgedge version printed $got, want $want"

for f in etc/bash_completion.d/pgedge share/zsh/site-functions/_pgedge \
    share/fish/vendor_completions.d/pgedge.fish; do
    [ -s "$prefix/$f" ] || fail "completion $prefix/$f is missing or empty"
done

# Homebrew quarantines a cask download; the postflight must clear it,
# or Gatekeeper refuses the unnotarized binary.
if [ "$(uname -s)" = Darwin ]; then
    # Positive control: an unquarantined download proves nothing here.
    xattr -p com.apple.quarantine "$(brew --cache --cask "$ref")" \
        >/dev/null 2>&1 || fail "Homebrew did not quarantine the download"
    bin=$(realpath "$prefix/bin/pgedge")
    if xattr -p com.apple.quarantine "$bin" >/dev/null 2>&1; then
        fail "$bin still carries com.apple.quarantine"
    fi
fi

# doctor exits non-zero on a failed check (no credentials here), so only
# its report is asserted.
pgedge doctor -o json >doctor.json || true
grep -Eq '"install_method": *"homebrew"' doctor.json ||
    fail "doctor did not report a Homebrew install: $(cat doctor.json)"

if pgedge self update >self-update.log 2>&1; then
    fail "self update ran on a Homebrew install"
fi
grep -Fq "installed via Homebrew" self-update.log ||
    fail "self update failed for another reason: $(cat self-update.log)"

brew_clean brew uninstall --cask "$ref"
[ ! -e "$prefix/bin/pgedge" ] || fail "uninstall left $prefix/bin/pgedge"

echo "cask install verified: $ref $want"
