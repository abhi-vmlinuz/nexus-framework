#!/usr/bin/env bash
#
# bump-version.sh — Update version across all Nexus Framework components.
#
# Usage:
#   ./bump-version.sh 0.1.2        # set all components to 0.1.2
#   ./bump-version.sh              # read from VERSION file
#   ./bump-version.sh --dry-run 0.1.2  # show what would change without writing
#
# Components updated:
#   - VERSION (repo root)
#   - nexus-engine/cmd/version.go
#   - nexus-cli/cmd/version.go
#   - nexus-installer/internal/installer.go
#   - nexus-node-agent/Cargo.toml
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
VERSION_FILE="$REPO_ROOT/VERSION"
DRY_RUN=false

# Parse args
NEW_VERSION=""
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        *) NEW_VERSION="$arg" ;;
    esac
done

# If no version argument, read from VERSION file
if [ -z "$NEW_VERSION" ]; then
    if [ ! -f "$VERSION_FILE" ]; then
        echo "ERROR: No version argument and no VERSION file found."
        echo "Usage: $0 [--dry-run] <version>"
        exit 1
    fi
    NEW_VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
fi

# Validate semver (basic check: X.Y.Z)
if ! echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$'; then
    echo "ERROR: Invalid version format: $NEW_VERSION"
    echo "Expected: X.Y.Z or X.Y.Z-tag (e.g., 0.1.2 or 0.1.2-beta)"
    exit 1
fi

# Strip leading 'v' if provided
NEW_VERSION="${NEW_VERSION#v}"

echo "Bumping version to: $NEW_VERSION"
echo "=================================="
echo ""

# Files to update
GO_VERSION="v$NEW_VERSION"
CARGO_VERSION="$NEW_VERSION"

# 1. VERSION file
if [ "$DRY_RUN" = true ]; then
    echo "[DRY RUN] VERSION -> $NEW_VERSION"
else
    echo "$NEW_VERSION" > "$VERSION_FILE"
    echo "  VERSION -> $NEW_VERSION"
fi

# 2. nexus-engine/cmd/version.go
ENGINE_FILE="$REPO_ROOT/nexus-engine/cmd/version.go"
if [ -f "$ENGINE_FILE" ]; then
    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] $ENGINE_FILE -> $GO_VERSION"
    else
        sed -i "s/var Version = \".*\"/var Version = \"$GO_VERSION\"/" "$ENGINE_FILE"
        echo "  nexus-engine -> $GO_VERSION"
    fi
else
    echo "  SKIP: $ENGINE_FILE not found"
fi

# 3. nexus-cli/cmd/version.go
CLI_FILE="$REPO_ROOT/nexus-cli/cmd/version.go"
if [ -f "$CLI_FILE" ]; then
    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] $CLI_FILE -> $GO_VERSION"
    else
        sed -i "s/var Version = \".*\"/var Version = \"$GO_VERSION\"/" "$CLI_FILE"
        echo "  nexus-cli -> $GO_VERSION"
    fi
else
    echo "  SKIP: $CLI_FILE not found"
fi

# 4. nexus-installer/internal/installer.go
INSTALLER_FILE="$REPO_ROOT/nexus-installer/internal/installer.go"
if [ -f "$INSTALLER_FILE" ]; then
    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] $INSTALLER_FILE -> $GO_VERSION"
    else
        sed -i "s/var Version = \".*\"/var Version = \"$GO_VERSION\"/" "$INSTALLER_FILE"
        echo "  nexus-installer -> $GO_VERSION"
    fi
else
    echo "  SKIP: $INSTALLER_FILE not found"
fi

# 5. nexus-node-agent/Cargo.toml (no 'v' prefix in Rust)
CARGO_FILE="$REPO_ROOT/nexus-node-agent/Cargo.toml"
if [ -f "$CARGO_FILE" ]; then
    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] $CARGO_FILE -> $CARGO_VERSION"
    else
        # Only replace the package version line (line 3), not dependency versions
        sed -i "0,/^version = \".*\"/s/^version = \".*\"/version = \"$CARGO_VERSION\"/" "$CARGO_FILE"
        echo "  nexus-node-agent -> $CARGO_VERSION"
    fi
else
    echo "  SKIP: $CARGO_FILE not found"
fi

echo ""
if [ "$DRY_RUN" = true ]; then
    echo "Dry run complete. No files were modified."
else
    echo "Version bumped to $NEW_VERSION across all components."
    echo ""
    echo "Next steps:"
    echo "  git add -A && git commit -m \"chore: bump version to $NEW_VERSION\""
    echo "  git tag v$NEW_VERSION"
    echo "  git push sync main --tags"
fi
