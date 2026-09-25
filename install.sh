#!/bin/sh
set -e

REPO="pgEdge/pgedge-cli"
BINARY="pgedge"

detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        *)       echo "unsupported" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *)             echo "unsupported" ;;
    esac
}

# sha256_of prints the SHA-256 of a file using whichever tool is
# present. It returns non-zero (printing nothing) when no sha256 tool
# is available — it never invents or echoes back an expected value.
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        return 1
    fi
}

# verify_checksum aborts (non-zero) unless file's SHA-256 matches the
# entry for archive_name in checksums_file. It FAILS CLOSED: a missing
# checksum entry, or the absence of any sha256 tool, is a hard error —
# never a silently skipped verification.
verify_checksum() {
    file="$1"
    archive_name="$2"
    checksums_file="$3"

    # Anchored on the filename field (awk $2), not a substring grep:
    # checksums.txt also carries SBOM sidecar entries named
    # "<archive_name>.sbom.json", and an unanchored grep for
    # archive_name matches both lines, turning $expected into two
    # newline-separated hashes and failing every install.
    expected=$(awk -v n="$archive_name" '$2 == n {print $1}' "$checksums_file")
    if [ -z "$expected" ]; then
        echo "Error: checksum not found for ${archive_name}" >&2
        return 1
    fi

    if ! actual=$(sha256_of "$file"); then
        echo "Error: no sha256 tool found (need sha256sum or shasum)." >&2
        echo "  Cannot verify download integrity; refusing to install." >&2
        echo "  Install a sha256 tool and retry, or download and verify" >&2
        echo "  the release archive manually." >&2
        return 1
    fi

    if [ "$expected" != "$actual" ]; then
        echo "Error: checksum mismatch" >&2
        echo "  expected: ${expected}" >&2
        echo "  actual:   ${actual}" >&2
        return 1
    fi
}

# warn_if_not_on_path prints a two-line PATH warning to stderr when
# install_dir is absent from PATH. Instructions only: it never writes
# to a shell rc file, since a `curl | sh` install is non-interactive
# and has no reliable rc file to target.
warn_if_not_on_path() {
    install_dir="$1"
    case ":${PATH}:" in
        *":${install_dir}:"*) return 0 ;;
    esac
    echo "Warning: ${install_dir} is not on your PATH." >&2
    echo "  Add it: export PATH=\"${install_dir}:\$PATH\"" >&2
}

# resolve_version prints the release tag to install: PGEDGE_VERSION
# when set, otherwise the newest release. A pin skips the API call,
# which also keeps CI runners clear of its unauthenticated rate limit.
resolve_version() {
    if [ -n "${PGEDGE_VERSION:-}" ]; then
        if ! printf '%s\n' "$PGEDGE_VERSION" \
            | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'; then
            echo "Error: PGEDGE_VERSION must be a release tag such as" \
                "v0.5.0-beta.2, got: ${PGEDGE_VERSION}" >&2
            return 1
        fi
        printf '%s\n' "$PGEDGE_VERSION"
        return 0
    fi

    # The newest release, not releases/latest: GitHub never counts a
    # pre-release as latest, and every release so far is one.
    releases=$(curl -fsSL \
        "https://api.github.com/repos/${REPO}/releases?per_page=1") \
        || releases=""
    latest=$(printf '%s\n' "$releases" | grep '"tag_name"' | head -n 1 \
        | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$latest" ]; then
        echo "Error: could not find a release of ${REPO} on GitHub" >&2
        return 1
    fi
    printf '%s\n' "$latest"
}

# When sourced by the test harness (PGEDGE_INSTALL_SH_LIB=1), stop
# here so the functions above can be exercised without running the
# installer body.
if [ "${PGEDGE_INSTALL_SH_LIB:-}" = "1" ]; then
    # Sourced by the test harness: return to it. The exit is a fallback
    # for the odd case of being executed (not sourced) with LIB set.
    # shellcheck disable=SC2317
    return 0 2>/dev/null || exit 0
fi

OS=$(detect_os)
ARCH=$(detect_arch)

if [ "$OS" = "unsupported" ] || [ "$ARCH" = "unsupported" ]; then
    echo "Error: unsupported platform $(uname -s)/$(uname -m)" >&2
    exit 1
fi

echo "Detected platform: ${OS}/${ARCH}"

VERSION=$(resolve_version)

echo "Version: ${VERSION}"
VERSION_NUM="${VERSION#v}"

ARCHIVE="${BINARY}_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
BUNDLE_URL="${CHECKSUM_URL}.sigstore.json"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading ${ARCHIVE}..."
if ! curl -fsSL -o "${TMPDIR}/${ARCHIVE}" "$URL"; then
    echo "Error: could not download ${ARCHIVE} from release ${VERSION}" >&2
    exit 1
fi

echo "Verifying checksum..."
curl -fsSL -o "${TMPDIR}/checksums.txt" "$CHECKSUM_URL"

# When cosign is available, verify the Sigstore signature on
# checksums.txt before trusting it. This proves the checksum list was
# produced by our release workflow, not just that the archive matches
# whatever list was served. Without cosign we fall back to checksum-
# only verification (integrity, not authenticity).
if command -v cosign >/dev/null 2>&1; then
    echo "Verifying signature with cosign..."
    if ! curl -fsSL -o "${TMPDIR}/checksums.txt.sigstore.json" \
        "$BUNDLE_URL"; then
        echo "Error: release ${VERSION} has no signature bundle to verify" >&2
        exit 1
    fi
    if ! cosign verify-blob \
        --bundle "${TMPDIR}/checksums.txt.sigstore.json" \
        --certificate-identity-regexp \
            "^https://github\.com/${REPO}/\.github/workflows/release\.yml@refs/tags/" \
        --certificate-oidc-issuer \
            "https://token.actions.githubusercontent.com" \
        "${TMPDIR}/checksums.txt" >/dev/null 2>&1; then
        echo "Error: cosign signature verification failed" >&2
        exit 1
    fi
    echo "Signature verified."
else
    echo "Note: cosign not found — skipping signature verification." >&2
    echo "  Install cosign for full supply-chain verification." >&2
fi

# set -e turns a non-zero return from verify_checksum into an abort.
verify_checksum "${TMPDIR}/${ARCHIVE}" "${ARCHIVE}" \
    "${TMPDIR}/checksums.txt"

echo "Extracting..."
tar -xzf "${TMPDIR}/${ARCHIVE}" -C "$TMPDIR"

INSTALL_DIR="/usr/local/bin"
if [ ! -w "$INSTALL_DIR" ]; then
    INSTALL_DIR="${HOME}/.local/bin"
    mkdir -p "$INSTALL_DIR"
fi

mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

echo "Installed ${BINARY} ${VERSION} to ${INSTALL_DIR}/${BINARY}"

warn_if_not_on_path "$INSTALL_DIR"

# Agent skills ship in the repo, not the binary. Point users at the
# install docs rather than installing anything here.
echo "AI-agent skills are available separately (SKILL.md format)."
echo "To install them for your agent, see:"
echo "  https://github.com/pgEdge/pgedge-cli#ai-agent-skills"

# Enable shell completion (bash, zsh, fish, PowerShell). Best-effort:
# the binary is already installed, so a failure here must never fail
# the install. Running under `curl | sh` is non-interactive, so this
# writes the script and prints instructions without editing any rc
# file. CI runners set CI and have no shell to detect.
if [ -n "${CI:-}" ]; then
    echo "Skipping shell completion on CI."
else
    echo "Setting up shell completion..."
    if ! "${INSTALL_DIR}/${BINARY}" completion install; then
        echo "Note: automatic completion setup did not run." >&2
        echo "Enable it later with: ${BINARY} completion install" >&2
    fi
fi
