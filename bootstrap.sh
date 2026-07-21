#!/bin/bash
set -e

# ──────────────────────────────────────────────────────────────────────────────
# Nexus Framework Bootstrap Installer
#
# Usage:
#   curl -fsSL https://gitlab.com/abhi-vmlinuz/nexus-framework/-/raw/main/bootstrap.sh | sudo bash
#
# Channels:
#   (default)          Install latest stable release (auto-detected)
#   --dev              Install latest development build (rolling)
#   --tag <version>    Install a specific version (e.g., --tag v0.1.1-beta)
#
# Environment overrides:
#   RELEASE_TAG=v0.1.1-beta    Force a specific tag (overrides channel flags)
# ──────────────────────────────────────────────────────────────────────────────

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

CHANNEL="stable"
SPECIFIED_TAG=""

# ── Parse arguments ──────────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
    case "$1" in
        --dev)
            CHANNEL="dev"
            shift
            ;;
        --tag)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}Error: --tag requires a version argument (e.g., --tag v0.1.1-beta)${NC}"
                exit 1
            fi
            CHANNEL="specific"
            SPECIFIED_TAG="$2"
            shift 2
            ;;
        --stable)
            CHANNEL="stable"
            shift
            ;;
        --help|-h)
            echo "Nexus Framework Installer"
            echo ""
            echo "Usage: bootstrap.sh [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  (default)          Install latest stable release"
            echo "  --stable           Install latest stable release (explicit)"
            echo "  --dev              Install latest development build"
            echo "  --tag <version>    Install a specific version (e.g., v0.1.1-beta)"
            echo "  --help, -h         Show this help"
            echo ""
            echo "Environment variables:"
            echo "  RELEASE_TAG        Override tag (e.g., RELEASE_TAG=v0.1.1-beta)"
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            echo "Run 'bootstrap.sh --help' for usage."
            exit 1
            ;;
    esac
done

echo -e "${BLUE}Nexus Framework - One-Click Bootstrapper${NC}"

# ── Request sudo ─────────────────────────────────────────────────────────────
echo -e "${BLUE}The installer requires root privileges for system configuration.${NC}"
sudo -v

# Keep-alive: update existing sudo time stamp until the script has finished
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
KEEPALIVE_PID=$!

TEMP_DIR=$(mktemp -d)

cleanup() {
    if [ -n "$KEEPALIVE_PID" ]; then
        kill "$KEEPALIVE_PID" 2>/dev/null || true
    fi
    if [ -d "$TEMP_DIR" ]; then
        rm -rf "$TEMP_DIR"
    fi
}
trap cleanup EXIT SIGINT SIGTERM

# ── Detect architecture ──────────────────────────────────────────────────────
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *)       echo -e "${RED}Error: Unsupported architecture: $ARCH. Only x86_64 (amd64) and aarch64 (arm64) are supported.${NC}"; exit 1 ;;
esac

# ── Check for curl ───────────────────────────────────────────────────────────
if ! command -v curl &>/dev/null; then
    echo -e "${BLUE}curl not found. Installing...${NC}"
    if command -v apt-get &>/dev/null; then sudo DEBIAN_FRONTEND=noninteractive apt-get update && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y curl;
    elif command -v dnf &>/dev/null; then sudo dnf install -y curl;
    elif command -v pacman &>/dev/null; then sudo pacman -S --noconfirm --needed curl;
    elif command -v zypper &>/dev/null; then sudo zypper install -y curl;
    else echo -e "${RED}Error: Package manager not found. Please install curl manually.${NC}"; exit 1;
    fi
fi

# ── Resolve release tag ──────────────────────────────────────────────────────
# Priority: RELEASE_TAG env var > --tag flag > channel detection
GITLAB_PROJECT="abhi-vmlinuz%2Fnexus-framework"
GITLAB_API="https://gitlab.com/api/v4/projects/${GITLAB_PROJECT}"

if [ -n "${RELEASE_TAG:-}" ]; then
    # Env var takes highest priority
    echo -e "${BLUE}Using release tag from environment: ${RELEASE_TAG}${NC}"

elif [ "$CHANNEL" = "specific" ]; then
    RELEASE_TAG="$SPECIFIED_TAG"
    echo -e "${BLUE}Using specified tag: ${RELEASE_TAG}${NC}"

elif [ "$CHANNEL" = "dev" ]; then
    RELEASE_TAG="latest-dev"
    echo -e "${BLUE}Channel: development (latest-dev)${NC}"

else
    # Stable channel: auto-detect latest stable release from GitLab API
    echo -e "${BLUE}Channel: stable (detecting latest release...)${NC}"

    LATEST_STABLE=$(curl -sf --max-time 10 \
        "${GITLAB_API}/releases?per_page=20" 2>/dev/null \
        | python3 -c "
import sys, json
try:
    releases = json.load(sys.stdin)
    for r in releases:
        tag = r.get('tag_name', '')
        # Skip rolling dev tags — stable releases match semver (vX.Y.Z or vX.Y.Z-*)
        if tag and tag != 'latest-dev' and not tag.startswith('main'):
            print(tag)
            break
except:
    pass
" 2>/dev/null || true)

    if [ -n "$LATEST_STABLE" ]; then
        RELEASE_TAG="$LATEST_STABLE"
        echo -e "${GREEN}Latest stable release: ${RELEASE_TAG}${NC}"
    else
        echo -e "${YELLOW}Warning: Could not detect latest stable release from GitLab API.${NC}"
        echo -e "${YELLOW}Falling back to latest-dev. Use --tag <version> to specify a release.${NC}"
        RELEASE_TAG="latest-dev"
    fi
fi

# ── Download installer ───────────────────────────────────────────────────────
REGISTRY_URL="${GITLAB_API}/packages/generic/nexus-framework/${RELEASE_TAG}"
INSTALLER_BIN="${TEMP_DIR}/nexus-installer"

echo -e "${BLUE}Downloading prebuilt installer for Linux ${ARCH} (Tag: ${RELEASE_TAG})...${NC}"
if ! curl --fail --retry 3 -sSL "${REGISTRY_URL}/nexus-installer-linux-${ARCH}" -o "${INSTALLER_BIN}"; then
    # Fallback to legacy package name "nexus-oss"
    REGISTRY_URL_FALLBACK="${GITLAB_API}/packages/generic/nexus-oss/${RELEASE_TAG}"
    echo -e "${YELLOW}Retrying with legacy registry URL (nexus-oss)...${NC}"
    if ! curl --fail --retry 3 -sSL "${REGISTRY_URL_FALLBACK}/nexus-installer-linux-${ARCH}" -o "${INSTALLER_BIN}"; then
        echo -e "${RED}Error: Failed to download installer for tag '${RELEASE_TAG}'.${NC}"
        echo -e "${RED}Check available releases at: https://gitlab.com/abhi-vmlinuz/nexus-framework/-/releases${NC}"
        exit 1
    fi
    REGISTRY_URL="${REGISTRY_URL_FALLBACK}"
fi

# ── Verify checksum ──────────────────────────────────────────────────────────
if ! command -v sha256sum &>/dev/null; then
    echo -e "${RED}Error: sha256sum is required for secure installation.${NC}"
    echo -e "${RED}Install coreutils package and retry.${NC}"
    exit 1
fi

echo -e "${BLUE}Verifying checksum...${NC}"
if ! curl --fail --retry 3 -sSL "${REGISTRY_URL}/checksums.txt" -o "${TEMP_DIR}/checksums.txt"; then
    echo -e "${RED}Error: Failed to download checksums.txt. Cannot verify installer integrity.${NC}"
    exit 1
fi
EXPECTED_SHA=$(grep "nexus-installer-linux-${ARCH}" "${TEMP_DIR}/checksums.txt" | cut -d' ' -f1 || true)
if [ -z "$EXPECTED_SHA" ]; then
    echo -e "${RED}Error: Checksum for installer not found in checksums.txt. Cannot verify installer integrity.${NC}"
    exit 1
fi
if ! (cd "${TEMP_DIR}" && echo "${EXPECTED_SHA}  nexus-installer" | sha256sum -c -); then
    echo -e "${RED}Error: Checksum verification failed. The downloaded installer may be corrupted or tampered with.${NC}"
    exit 1
fi

chmod +x "${INSTALLER_BIN}"

# ── Launch installer ─────────────────────────────────────────────────────────
echo -e "${GREEN}Launching Nexus Installer TUI...${NC}"
if "${INSTALLER_BIN}"; then
    echo -e "${GREEN}Bootstrap finished successfully.${NC}"
else
    echo -e "${RED}Installer exited with error.${NC}"
    echo -e "Please check /var/log/nexus-install.log for details."
    exit 1
fi
