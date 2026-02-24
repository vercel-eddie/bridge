#!/bin/sh
set -e

REPO="vercel/bridge"
INSTALL_DIR="/usr/local/bin"

# Detect OS
get_os() {
    case "$(uname -s)" in
        Linux)  echo "linux" ;;
        Darwin) echo "darwin" ;;
        *)      echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
    esac
}

# Detect architecture
get_arch() {
    case "$(uname -m)" in
        x86_64)        echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *)             echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
    esac
}

main() {
    local os arch binary_name download_url

    os=$(get_os)
    arch=$(get_arch)

    binary_name="bridge-${os}-${arch}"
    download_url="https://github.com/${REPO}/releases/download/edge/${binary_name}"

    echo "Downloading bridge edge (${os}/${arch})..."

    curl -fsSL -o bridge "${download_url}"
    chmod +x bridge

    # Remove macOS quarantine attribute to prevent Gatekeeper from killing the binary
    if [ "$os" = "darwin" ]; then
        xattr -d com.apple.quarantine bridge 2>/dev/null || true
    fi

    if [ -w "$INSTALL_DIR" ]; then
        mv bridge "${INSTALL_DIR}/bridge"
    else
        echo "Moving binary to ${INSTALL_DIR} (requires sudo)..."
        sudo mv bridge "${INSTALL_DIR}/bridge"
    fi

    echo "bridge (edge) installed to ${INSTALL_DIR}/bridge"
}

main
