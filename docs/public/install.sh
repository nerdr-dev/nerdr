#!/bin/sh
# Installs nerdr from its GitHub releases, on Linux or macOS:
#
#   curl -fsSL https://nerdr-dev.github.io/nerdr/install.sh | sh
#
# NERDR_VERSION=v0.1.0 picks a release instead of the latest, and
# NERDR_INSTALL_DIR a folder instead of ~/.local/bin.
set -eu

REPO="nerdr-dev/nerdr"
INSTALL_DIR="${NERDR_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${NERDR_VERSION:-latest}"

main() {
    case "$(uname -s)" in
        Linux)  os="linux" ;;
        Darwin) os="darwin" ;;
        *)      err "nerdr runs on Linux and macOS, not $(uname -s)" ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64)  arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)             err "no nerdr build for $(uname -m)" ;;
    esac

    need curl
    need tar
    if command -v sha256sum >/dev/null 2>&1; then
        sha256() { sha256sum "$1" | awk '{ print $1 }'; }
    elif command -v shasum >/dev/null 2>&1; then
        sha256() { shasum -a 256 "$1" | awk '{ print $1 }'; }
    else
        err "needs sha256sum or shasum to check the download"
    fi

    if [ "$VERSION" = "latest" ]; then
        base="https://github.com/${REPO}/releases/latest/download"
    else
        base="https://github.com/${REPO}/releases/download/${VERSION}"
    fi
    archive="nerdr-${os}-${arch}.tar.gz"

    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT

    log "downloading ${archive} (${VERSION})"
    fetch "${base}/${archive}" "${tmp}/${archive}"
    fetch "${base}/checksums.txt" "${tmp}/checksums.txt"

    want="$(awk -v f="$archive" '$2 == f { print $1 }' "${tmp}/checksums.txt")"
    [ -n "$want" ] || err "checksums.txt has no entry for ${archive}"
    [ "$(sha256 "${tmp}/${archive}")" = "$want" ] || err "checksum of ${archive} does not match"

    tar -xzf "${tmp}/${archive}" -C "$tmp" nerdr
    mkdir -p "$INSTALL_DIR"
    # Move, not copy, so a running nerdr keeps its old binary.
    mv "${tmp}/nerdr" "${INSTALL_DIR}/nerdr"
    chmod +x "${INSTALL_DIR}/nerdr"
    log "installed $("${INSTALL_DIR}/nerdr" version) to ${INSTALL_DIR}/nerdr"

    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            warn "${INSTALL_DIR} is not on your PATH; add this to your shell's config:"
            echo ""
            echo "    export PATH=\"${INSTALL_DIR}:\$PATH\""
            echo ""
            ;;
    esac
    log "start it in the folder that holds your repositories: cd ~/develop && nerdr"
}

fetch() {
    curl -fsSL --retry 3 --connect-timeout 10 "$1" -o "$2" || err "could not download $1"
}

log()  { printf '  \033[32m>\033[0m %s\n' "$1"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$1"; }
err()  { printf '  \033[31mx\033[0m %s\n' "$1" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || err "needs $1; install it, or download nerdr from https://github.com/${REPO}/releases"
}

main "$@"
