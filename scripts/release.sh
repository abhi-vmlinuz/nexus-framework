#!/bin/bash
set -e

# scripts/release.sh - Nexus OSS Release Automation
# Usage: ./scripts/release.sh [patch|feature]

VERSION_FILE="nexus-installer/internal/installer.go"

if [ ! -f "$VERSION_FILE" ]; then
    echo "Error: $VERSION_FILE not found."
    exit 1
fi

# Extract current version (e.g., v0.1.0-alpha or v0.1.0)
CURRENT_VERSION=$(grep -oP 'const Version = "\K[^"]+' "$VERSION_FILE")
echo "Current Version: $CURRENT_VERSION"

# Strip 'v' and any suffix like '-alpha'
CLEAN_VERSION=$(echo "$CURRENT_VERSION" | sed 's/^v//' | sed 's/-.*//')

# Split into array
IFS='.' read -r -a VERSION_PARTS <<< "$CLEAN_VERSION"
MAJOR=${VERSION_PARTS[0]}
MINOR=${VERSION_PARTS[1]}
PATCH=${VERSION_PARTS[2]}

case "$1" in
    patch)
        PATCH=$((PATCH + 1))
        ;;
    feature)
        MINOR=$((MINOR + 1))
        PATCH=0
        ;;
    *)
        echo "Usage: $0 [patch|feature]"
        exit 1
        ;;
esac

NEW_VERSION="v$MAJOR.$MINOR.$PATCH"
NEW_VERSION_CLEAN="$MAJOR.$MINOR.$PATCH"
echo "Target Version: $NEW_VERSION"

# 1. Update Installer
sed -i "s/const Version = \"$CURRENT_VERSION\"/const Version = \"$NEW_VERSION\"/" "$VERSION_FILE"

# 2. Update Engine
ENGINE_VERSION_FILE="nexus-engine/cmd/version.go"
if [ -f "$ENGINE_VERSION_FILE" ]; then
    sed -i "s/var Version = \".*\"/var Version = \"$NEW_VERSION\"/" "$ENGINE_VERSION_FILE"
fi

# 3. Update CLI
CLI_VERSION_FILE="nexus-cli/cmd/version.go"
if [ -f "$CLI_VERSION_FILE" ]; then
    sed -i "s/var Version = \".*\"/var Version = \"$NEW_VERSION\"/" "$CLI_VERSION_FILE"
fi

# 4. Update Node Agent (Cargo.toml)
CARGO_FILE="nexus-node-agent/Cargo.toml"
if [ -f "$CARGO_FILE" ]; then
    # Matches version = "x.y.z"
    sed -i "s/^version = \".*\"/version = \"$NEW_VERSION_CLEAN\"/" "$CARGO_FILE"
fi

# Git Flow
echo "Committing version bump for all components..."
git add "$VERSION_FILE" "$ENGINE_VERSION_FILE" "$CLI_VERSION_FILE" "$CARGO_FILE"
git commit -m "chore: bump version to $NEW_VERSION across all components"

echo "Tagging $NEW_VERSION..."
git tag "$NEW_VERSION"

echo "Pushing to origin..."
git push origin main
git push origin "$NEW_VERSION"

echo "------------------------------------------------"
echo "Successfully released $NEW_VERSION"
echo "GitHub Actions will now build and upload assets."
echo "------------------------------------------------"
