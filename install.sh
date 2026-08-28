#!/usr/bin/env bash
set -euo pipefail

REPO="khanhbkqt/agyent"
GITHUB_URL="https://github.com/${REPO}"
BINARY_NAME="agyent"

echo "========================================================="
echo "   🚀 Installing agyent (Autonomous AI Assistant Gateway)"
echo "========================================================="

# 1. Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${OS}" in
    linux*)     TARGET_OS="linux" ;;
    darwin*)    TARGET_OS="darwin" ;;
    *)          echo "❌ Unsupported operating system: ${OS}"; exit 1 ;;
esac

# 2. Detect Architecture
ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64|amd64)   TARGET_ARCH="amd64" ;;
    arm64|aarch64)  TARGET_ARCH="arm64" ;;
    *)              echo "❌ Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

echo "Detected platform: ${TARGET_OS}-${TARGET_ARCH}"

# 3. Determine latest version if not specified
if [ -z "${AGYENT_VERSION:-}" ]; then
    echo "Fetching latest release metadata from GitHub..."
    LATEST_TAG=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "${LATEST_TAG}" ]; then
        LATEST_TAG="v1.0.0"
    fi
    VERSION="${LATEST_TAG}"
else
    VERSION="${AGYENT_VERSION}"
    if [[ ! "${VERSION}" =~ ^v ]]; then
        VERSION="v${VERSION}"
    fi
fi

echo "Selected version: ${VERSION}"

# 4. Construct download URL
ARCHIVE_NAME="agyent-${VERSION}-${TARGET_OS}-${TARGET_ARCH}.tar.gz"
DOWNLOAD_URL="${GITHUB_URL}/releases/download/${VERSION}/${ARCHIVE_NAME}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "Downloading ${ARCHIVE_NAME}..."
if ! curl -fsSL "${DOWNLOAD_URL}" -o "${TMP_DIR}/${ARCHIVE_NAME}"; then
    echo "❌ Failed to download release from ${DOWNLOAD_URL}"
    echo "Please check if the release exists on GitHub: ${GITHUB_URL}/releases"
    exit 1
fi

echo "Extracting archive..."
tar -xzf "${TMP_DIR}/${ARCHIVE_NAME}" -C "${TMP_DIR}"

# 5. Determine installation target directory
INSTALL_DIR="/usr/local/bin"
USE_SUDO=false

if [ ! -w "${INSTALL_DIR}" ]; then
    if command -v sudo >/dev/null 2>&1 && [ "$(id -u)" -ne 0 ]; then
        USE_SUDO=true
    else
        INSTALL_DIR="${HOME}/.local/bin"
        mkdir -p "${INSTALL_DIR}"
    fi
fi

echo "Installing ${BINARY_NAME} to ${INSTALL_DIR}..."
if [ "${USE_SUDO}" = true ]; then
    sudo cp "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
else
    cp "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
fi

# 6. Verify installation
echo "Verifying installation..."
if command -v agyent >/dev/null 2>&1; then
    agyent version
elif [ -x "${INSTALL_DIR}/${BINARY_NAME}" ]; then
    "${INSTALL_DIR}/${BINARY_NAME}" version
    echo ""
    echo "⚠️ Note: ${INSTALL_DIR} is not in your PATH."
    echo "Add it by running:"
    echo "  export PATH=\"\$PATH:${INSTALL_DIR}\""
    echo "  echo 'export PATH=\"\$PATH:${INSTALL_DIR}\"' >> ~/.bashrc (or ~/.zshrc)"
fi

echo ""
echo "========================================================="
echo "   🎉 agyent has been successfully installed!"
echo "========================================================="
echo ""
echo "Next steps:"
echo "  1. Run the setup wizard:    agyent init"
echo "  2. Register bot commands:  agyent register-commands"
echo "  3. Start the gateway:      agyent run"
echo ""
