#!/bin/bash
# Build script for ctdbtools
# Creates cross-platform binaries in dist/

set -e

# Get the script directory and project root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# Get version from version.go
VERSION=$(grep 'const Version' internal/version/version.go | sed 's/.*"\(.*\)"/\1/')
echo "Building ctdbtools v${VERSION}"

# Create dist directory
DIST_DIR="$PROJECT_ROOT/dist"
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

# Build targets
TARGETS=(
    "darwin/amd64"
    "darwin/arm64"
    "linux/amd64"
    "linux/arm64"
    "windows/amd64"
)

for target in "${TARGETS[@]}"; do
    GOOS="${target%/*}"
    GOARCH="${target#*/}"

    OUTPUT_NAME="ctdbtools-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
        OUTPUT_NAME="${OUTPUT_NAME}.exe"
    fi

    echo "Building ${OUTPUT_NAME}..."
    GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags="-s -w" -o "$DIST_DIR/$OUTPUT_NAME" ./cmd/ctdbtools
done

echo ""
echo "Build complete! Binaries in dist/:"
ls -lh "$DIST_DIR"
