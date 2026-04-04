#!/usr/bin/env bash
set -euo pipefail

# IaC Studio Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/your-org/iac-studio/main/scripts/install.sh | bash

REPO="your-org/iac-studio"
INSTALL_DIR="${IAC_STUDIO_INSTALL_DIR:-/usr/local/bin}"
BINARY="iac-studio"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}[info]${NC}  $1"; }
ok()    { echo -e "${GREEN}[ok]${NC}    $1"; }
warn()  { echo -e "${YELLOW}[warn]${NC}  $1"; }
err()   { echo -e "${RED}[error]${NC} $1"; exit 1; }

# Detect OS and architecture
detect_platform() {
    OS="$(uname -s)"
    ARCH="$(uname -m)"

    case "$OS" in
        Linux)   OS="linux" ;;
        Darwin)  OS="darwin" ;;
        MINGW*|MSYS*|CYGWIN*) OS="windows" ;;
        *)       err "Unsupported OS: $OS" ;;
    esac

    case "$ARCH" in
        x86_64|amd64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)             err "Unsupported architecture: $ARCH" ;;
    esac

    PLATFORM="${OS}-${ARCH}"
    FILENAME="${BINARY}-${PLATFORM}"
    if [ "$OS" = "windows" ]; then
        FILENAME="${FILENAME}.exe"
    fi
}

# Get latest release tag
get_latest_version() {
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        err "Could not determine latest version"
    fi
    info "Latest version: $VERSION"
}

# Download and install with checksum verification
install() {
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

    info "Downloading ${FILENAME}..."
    TMPDIR=$(mktemp -d)
    trap "rm -rf $TMPDIR" EXIT

    if ! curl -fsSL "$DOWNLOAD_URL" -o "${TMPDIR}/${BINARY}"; then
        err "Download failed. Check https://github.com/${REPO}/releases for available binaries."
    fi

    # Verify checksum
    info "Verifying checksum..."
    if curl -fsSL "$CHECKSUMS_URL" -o "${TMPDIR}/checksums.txt" 2>/dev/null; then
        EXPECTED=$(grep "${FILENAME}" "${TMPDIR}/checksums.txt" | awk '{print $1}')
        if [ -n "$EXPECTED" ]; then
            if command -v sha256sum &>/dev/null; then
                ACTUAL=$(sha256sum "${TMPDIR}/${BINARY}" | awk '{print $1}')
            elif command -v shasum &>/dev/null; then
                ACTUAL=$(shasum -a 256 "${TMPDIR}/${BINARY}" | awk '{print $1}')
            else
                warn "No sha256sum or shasum found — skipping checksum verification"
                ACTUAL="$EXPECTED"
            fi
            if [ "$EXPECTED" != "$ACTUAL" ]; then
                err "Checksum mismatch!\n  Expected: ${EXPECTED}\n  Got:      ${ACTUAL}\n  The downloaded binary may be corrupted or tampered with."
            fi
            ok "Checksum verified"
        else
            warn "Binary not found in checksums.txt — skipping verification"
        fi
    else
        warn "Could not download checksums.txt — skipping verification"
    fi

    chmod +x "${TMPDIR}/${BINARY}"

    # Install to target directory
    if [ -w "$INSTALL_DIR" ]; then
        mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    else
        info "Installing to ${INSTALL_DIR} (requires sudo)..."
        sudo mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    fi

    ok "Installed to ${INSTALL_DIR}/${BINARY}"
}

# Verify installation
verify() {
    if command -v "$BINARY" &>/dev/null; then
        ok "$($BINARY --version)"
    else
        warn "Binary installed but not in PATH. Add ${INSTALL_DIR} to your PATH."
    fi
}

# Check for optional dependencies
check_deps() {
    echo ""
    info "Checking optional dependencies..."

    for cmd in terraform tofu ansible ollama; do
        if command -v "$cmd" &>/dev/null; then
            ok "$cmd found"
        else
            warn "$cmd not found (optional)"
        fi
    done
}

# Main
main() {
    echo ""
    echo "  ◆ IaC Studio Installer"
    echo "  ───────────────────────"
    echo ""

    detect_platform
    info "Platform: ${PLATFORM}"

    get_latest_version
    install
    verify
    check_deps

    echo ""
    ok "Installation complete!"
    echo ""
    echo "  Run:  ${BINARY}"
    echo "  Then open: http://localhost:3000"
    echo ""
    echo "  For AI features, install Ollama:"
    echo "    curl -fsSL https://ollama.com/install.sh | sh"
    echo "    ollama pull deepseek-coder:6.7b"
    echo ""
}

main "$@"
