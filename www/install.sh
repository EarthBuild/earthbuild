#!/bin/sh
set -e

# EarthBuild installer script
# Usage: curl -fsSL https://www.earthbuild.dev/install.sh | sh

GITHUB_REPO="EarthBuild/earthbuild"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="earth"

log() {
    echo "[earth] $1"
}

error() {
    echo "[earth] ERROR: $1" >&2
    exit 1
}

detect_os() {
    log "Detecting operating system..."
    OS="$(uname -s)"
    case "$OS" in
        Linux*)
            OS="linux"
            ;;
        Darwin*)
            OS="darwin"
            ;;
        MINGW*|MSYS*|CYGWIN*)
            error "Native Windows is not supported. Please use WSL 2."
            ;;
        *)
            error "Unsupported operating system: $OS"
            ;;
    esac
    log "Detected OS: $OS"
}

detect_arch() {
    log "Detecting architecture..."
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        *)
            error "Unsupported architecture: $ARCH"
            ;;
    esac
    log "Detected architecture: $ARCH"
}

check_dependencies() {
    log "Checking dependencies..."

    if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
        error "Either curl or wget is required to download EarthBuild."
    fi

    log "Dependencies OK"
}

download_file() {
    URL="$1"
    OUTPUT="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$URL" -o "$OUTPUT"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$URL" -O "$OUTPUT"
    fi
}

get_latest_version() {
    log "Fetching latest version..."

    LATEST_URL="https://github.com/$GITHUB_REPO/releases/latest"

    if command -v curl >/dev/null 2>&1; then
        VERSION=$(curl -fsSI -o /dev/null -w '%{url_effective}' "$LATEST_URL" | sed 's|.*/||')
    elif command -v wget >/dev/null 2>&1; then
        VERSION=$(wget --spider --max-redirect=0 "$LATEST_URL" 2>&1 | grep -o 'releases/tag/[^"]*' | head -1 | sed 's|releases/tag/||')
    fi

    if [ -z "$VERSION" ]; then
        error "Failed to determine latest version. Check your internet connection."
    fi

    log "Latest version: $VERSION"
}

download_binary() {
    BINARY_FILENAME="${BINARY_NAME}-${OS}-${ARCH}"
    DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/$BINARY_FILENAME"

    log "Downloading from: $DOWNLOAD_URL"

    TMP_DIR=$(mktemp -d)
    TMP_FILE="$TMP_DIR/$BINARY_NAME"

    if ! download_file "$DOWNLOAD_URL" "$TMP_FILE"; then
        rm -rf "$TMP_DIR"
        error "Failed to download binary. The release asset may not exist for your platform ($OS/$ARCH)."
    fi

    log "Download complete"
}

install_binary() {
    log "Installing to $INSTALL_DIR/$BINARY_NAME..."

    chmod +x "$TMP_FILE"

    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
    else
        log "Requesting sudo access to install to $INSTALL_DIR..."
        sudo mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
    fi

    rm -rf "$TMP_DIR"

    log "Installation complete"
}

verify_installation() {
    log "Verifying installation..."

    if ! command -v "$BINARY_NAME" >/dev/null 2>&1; then
        error "Installation failed. $BINARY_NAME not found in PATH."
    fi

    INSTALLED_VERSION=$("$BINARY_NAME" --version 2>/dev/null || echo "unknown")
    log "Installed: $INSTALLED_VERSION"
}

main() {
    echo ""
    echo "=========================================="
    echo "  EarthBuild Installer"
    echo "=========================================="
    echo ""

    detect_os
    detect_arch
    check_dependencies
    get_latest_version
    download_binary
    install_binary
    verify_installation

    echo ""
    echo "=========================================="
    echo "  Installation successful!"
    echo "=========================================="
    echo ""
    echo "Next steps:"
    echo ""
    echo "  1. Run 'earth bootstrap' to complete setup"
    echo "  2. Visit https://docs.earthbuild.dev/getting-started"
    echo ""
}

main
