#!/bin/sh
set -e

# EarthBuild installer script
# Usage: curl -fsSL https://www.earthbuild.dev/install.sh | sh

GITHUB_REPO="EarthBuild/earthbuild"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="earth"
TMP_DIR=""
TMP_FILE=""

log() {
    echo "[earth] $1"
}

error() {
    echo "[earth] ERROR: $1" >&2
    exit 1
}

cleanup() {
    if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
        rm -rf "$TMP_DIR"
    fi
}

trap cleanup EXIT INT TERM

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

    if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
        error "A SHA-256 tool is required (sha256sum or shasum)."
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

    RELEASES_LATEST_API_URL="https://api.github.com/repos/$GITHUB_REPO/releases/latest"
    RELEASES_LIST_API_URL="https://api.github.com/repos/$GITHUB_REPO/releases"

    if command -v curl >/dev/null 2>&1; then
        VERSION=$(
            {
                curl -fsSL "$RELEASES_LATEST_API_URL" 2>/dev/null ||
                curl -fsSL "$RELEASES_LIST_API_URL"
            } | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
        )
    elif command -v wget >/dev/null 2>&1; then
        VERSION=$(
            {
                wget -qO- "$RELEASES_LATEST_API_URL" ||
                wget -qO- "$RELEASES_LIST_API_URL"
            } | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
        )
    fi

    if [ -z "$VERSION" ]; then
        error "Failed to determine latest version. Check your internet connection."
    fi

    log "Latest version: $VERSION"
}

extract_expected_hash() {
    CHECKSUM_PATH="$1"
    TARGET_FILE="$2"

    awk -v target_file="$TARGET_FILE" '
        {
            gsub("\r", "")

            # GNU format: <hash>  <filename> OR <hash> *<filename>
            if ($1 ~ /^[A-Fa-f0-9]{64}$/ && ($2 == target_file || $2 == "*" target_file)) {
                print tolower($1)
                exit
            }

            # BSD format: SHA256 (<filename>) = <hash>
            if ($1 == "SHA256" && $2 == "(" target_file ")" && $3 == "=" && $4 ~ /^[A-Fa-f0-9]{64}$/) {
                print tolower($4)
                exit
            }
        }
    ' "$CHECKSUM_PATH"
}

calculate_sha256() {
    FILE_TO_HASH="$1"

    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$FILE_TO_HASH" | awk '{ print tolower($1) }'
    else
        shasum -a 256 "$FILE_TO_HASH" | awk '{ print tolower($1) }'
    fi
}

verify_checksum() {
    CHECKSUM_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/checksum.asc"
    CHECKSUM_FILE="$TMP_DIR/checksum.asc"

    log "Downloading checksum file..."
    if ! download_file "$CHECKSUM_URL" "$CHECKSUM_FILE"; then
        error "Failed to download checksum.asc for release $VERSION."
    fi

    EXPECTED_HASH=$(extract_expected_hash "$CHECKSUM_FILE" "$BINARY_FILENAME")
    if [ -z "$EXPECTED_HASH" ]; then
        error "Could not find checksum entry for $BINARY_FILENAME in checksum.asc."
    fi

    ACTUAL_HASH=$(calculate_sha256 "$TMP_FILE")
    if [ "$ACTUAL_HASH" != "$EXPECTED_HASH" ]; then
        error "Checksum verification failed for $BINARY_FILENAME."
    fi

    log "Checksum verified"
}

download_binary() {
    BINARY_FILENAME="${BINARY_NAME}-${OS}-${ARCH}"
    DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/$BINARY_FILENAME"

    log "Downloading from: $DOWNLOAD_URL"

    TMP_DIR=$(mktemp -d)
    TMP_FILE="$TMP_DIR/$BINARY_NAME"

    if ! download_file "$DOWNLOAD_URL" "$TMP_FILE"; then
        error "Failed to download binary. The release asset may not exist for your platform ($OS/$ARCH)."
    fi

    verify_checksum

    log "Download complete"
}

install_binary() {
    log "Installing to $INSTALL_DIR/$BINARY_NAME..."

    chmod +x "$TMP_FILE"

    if [ ! -d "$INSTALL_DIR" ]; then
        if [ -w "$(dirname "$INSTALL_DIR")" ]; then
            mkdir -p "$INSTALL_DIR"
        else
            if ! command -v sudo >/dev/null 2>&1; then
                error "Install directory $INSTALL_DIR does not exist and sudo is not available to create it."
            fi
            log "Requesting sudo access to create $INSTALL_DIR..."
            sudo mkdir -p "$INSTALL_DIR"
        fi
    fi

    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
    else
        if ! command -v sudo >/dev/null 2>&1; then
            error "No write permission for $INSTALL_DIR and sudo is not available."
        fi
        log "Requesting sudo access to install to $INSTALL_DIR..."
        sudo mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
    fi
    chmod +x "$INSTALL_DIR/$BINARY_NAME" 2>/dev/null || sudo chmod +x "$INSTALL_DIR/$BINARY_NAME"

    log "Installation complete"
}

verify_installation() {
    log "Verifying installation..."

    if [ ! -x "$INSTALL_DIR/$BINARY_NAME" ]; then
        error "Installation failed. $INSTALL_DIR/$BINARY_NAME is not executable."
    fi

    INSTALLED_VERSION=$("$INSTALL_DIR/$BINARY_NAME" --version 2>/dev/null || echo "unknown")
    log "Installed: $INSTALLED_VERSION"

    if ! command -v "$BINARY_NAME" >/dev/null 2>&1; then
        log "Note: '$BINARY_NAME' is not currently on your PATH. You can run it as $INSTALL_DIR/$BINARY_NAME."
    fi
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
