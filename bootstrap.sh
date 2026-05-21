#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}Nexus OSS - One-Click Bootstrapper${NC}"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *)       echo -e "${RED}Error: Unsupported architecture: $ARCH. Only x86_64 (amd64) and aarch64 (arm64) are supported.${NC}"; exit 1 ;;
esac

# Check for curl
if ! command -v curl &>/dev/null; then
    echo -e "${BLUE}curl not found. Installing...${NC}"
    if command -v apt-get &>/dev/null; then sudo apt-get update && sudo apt-get install -y curl;
    elif command -v dnf &>/dev/null; then sudo dnf install -y curl;
    elif command -v pacman &>/dev/null; then sudo pacman -S --noconfirm --needed curl;
    elif command -v zypper &>/dev/null; then sudo zypper install -y curl;
    else echo -e "${RED}Error: Package manager not found. Please install curl manually.${NC}"; exit 1;
    fi
fi

# Define release tag (can be overridden by environment variable)
RELEASE_TAG="${RELEASE_TAG:-latest-dev}"
REGISTRY_URL="https://gitlab.com/api/v4/projects/abhi-vmlinuz%2Fnexus-oss/packages/generic/nexus-oss/${RELEASE_TAG}"

TEMP_DIR=$(mktemp -d)
INSTALLER_BIN="${TEMP_DIR}/nexus-installer"

echo -e "${BLUE}Downloading prebuilt installer for Linux ${ARCH} (Tag: ${RELEASE_TAG})...${NC}"
curl --fail --retry 3 -sSL "${REGISTRY_URL}/nexus-installer-linux-${ARCH}" -o "${INSTALLER_BIN}"

# Download checksums and verify if sha256sum is available
if command -v sha256sum &>/dev/null; then
    echo -e "${BLUE}Verifying checksum...${NC}"
    if curl --fail --retry 3 -sSL "${REGISTRY_URL}/checksums.txt" -o "${TEMP_DIR}/checksums.txt" 2>/dev/null; then
        EXPECTED_SHA=$(grep "nexus-installer-linux-${ARCH}" "${TEMP_DIR}/checksums.txt" | cut -d' ' -f1)
        if [ -n "$EXPECTED_SHA" ]; then
            (cd "${TEMP_DIR}" && echo "${EXPECTED_SHA}  nexus-installer" | sha256sum -c -)
        else
            echo -e "${RED}Warning: Checksum for installer not found in checksums.txt. Skipping verification.${NC}"
        fi
    else
        echo -e "${RED}Warning: Failed to download checksums.txt. Skipping verification.${NC}"
    fi
fi

chmod +x "${INSTALLER_BIN}"

echo -e "${GREEN}Launching Nexus Installer TUI...${NC}"
"${INSTALLER_BIN}"

INSTALL_STATUS=$?

# Cleanup
rm -rf "${TEMP_DIR}"

if [ $INSTALL_STATUS -eq 0 ]; then
    echo -e "${GREEN}Bootstrap finished successfully.${NC}"
else
    echo -e "${RED}Installer exited with error code ${INSTALL_STATUS}.${NC}"
    exit 1
fi
