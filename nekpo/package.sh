#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -lt 3 || "$#" -gt 4 ]]; then
  echo "Usage: $0 <binary_path> <output_dir> <conf_file> [version]" >&2
  exit 1
fi

BINARY_PATH="$1"
OUTPUT_DIR="$2"
CONF_FILE="$3"
ARG_VERSION="${4:-}"

[[ -f "$BINARY_PATH" ]] || { echo "Error: Binary not found: $BINARY_PATH" >&2; exit 1; }
[[ -x "$BINARY_PATH" ]] || { echo "Error: Binary is not executable: $BINARY_PATH" >&2; exit 1; }
[[ -f "$CONF_FILE" ]] || { echo "Error: Config file not found: $CONF_FILE" >&2; exit 1; }

command -v tar >/dev/null 2>&1 || { echo "Error: tar is required." >&2; exit 1; }
command -v dpkg-deb >/dev/null 2>&1 || { echo "Error: dpkg-deb is required." >&2; exit 1; }

PKG_NAME=""
PKG_VERSION=""
PKG_ARCH=""
PKG_MAINTAINER=""
PKG_DESCRIPTION=""
PKG_SECTION="utils"
PKG_PRIORITY="optional"
PKG_DEPENDS=""
PKG_RECOMMENDS=""
PKG_SUGGESTS=""
PKG_CONFLICTS=""
PKG_REPLACES=""
PKG_HOMEPAGE=""
INSTALL_PREFIX="/usr/local"

set -a
source "$CONF_FILE"
set +a

if [[ -n "$ARG_VERSION" ]]; then
  PKG_VERSION="$ARG_VERSION"
fi

[[ -n "$PKG_NAME" ]] || { echo "Error: PKG_NAME is required in conf file." >&2; exit 1; }
[[ -n "$PKG_VERSION" ]] || { echo "Error: Version must be provided via argument or PKG_VERSION in conf file." >&2; exit 1; }
[[ -n "$PKG_ARCH" ]] || { echo "Error: PKG_ARCH is required in conf file." >&2; exit 1; }
[[ -n "$PKG_MAINTAINER" ]] || { echo "Error: PKG_MAINTAINER is required in conf file." >&2; exit 1; }
[[ -n "$PKG_DESCRIPTION" ]] || { echo "Error: PKG_DESCRIPTION is required in conf file." >&2; exit 1; }

mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

BINARY_NAME="$(basename "$BINARY_PATH")"
ARTIFACT_BASENAME="${PKG_NAME}_${PKG_VERSION}_${PKG_ARCH}"

TAR_STAGE="$WORK_DIR/tar_stage"
mkdir -p "$TAR_STAGE/bin"
cp -p "$BINARY_PATH" "$TAR_STAGE/bin/$BINARY_NAME"
chmod 0755 "$TAR_STAGE/bin/$BINARY_NAME"

TARBALL_PATH="$OUTPUT_DIR/${ARTIFACT_BASENAME}.tar.gz"
tar -C "$TAR_STAGE" -czf "$TARBALL_PATH" bin

DEB_STAGE="$WORK_DIR/deb_stage"
DEB_BIN_DIR="${DEB_STAGE}${INSTALL_PREFIX}/bin"
DEB_DEBIAN_DIR="$DEB_STAGE/DEBIAN"

mkdir -p "$DEB_BIN_DIR" "$DEB_DEBIAN_DIR"
cp -p "$BINARY_PATH" "$DEB_BIN_DIR/$BINARY_NAME"
chmod 0755 "$DEB_BIN_DIR/$BINARY_NAME"

INSTALLED_SIZE="$(du -sk "$DEB_STAGE" | cut -f1)"

{
  printf "Package: %s\n" "$PKG_NAME"
  printf "Version: %s\n" "$PKG_VERSION"
  printf "Section: %s\n" "$PKG_SECTION"
  printf "Priority: %s\n" "$PKG_PRIORITY"
  printf "Architecture: %s\n" "$PKG_ARCH"
  printf "Installed-Size: %s\n" "$INSTALLED_SIZE"
  printf "Maintainer: %s\n" "$PKG_MAINTAINER"
  [[ -n "$PKG_HOMEPAGE" ]] && printf "Homepage: %s\n" "$PKG_HOMEPAGE"
  [[ -n "$PKG_DEPENDS" ]] && printf "Depends: %s\n" "$PKG_DEPENDS"
  [[ -n "$PKG_RECOMMENDS" ]] && printf "Recommends: %s\n" "$PKG_RECOMMENDS"
  [[ -n "$PKG_SUGGESTS" ]] && printf "Suggests: %s\n" "$PKG_SUGGESTS"
  [[ -n "$PKG_CONFLICTS" ]] && printf "Conflicts: %s\n" "$PKG_CONFLICTS"
  [[ -n "$PKG_REPLACES" ]] && printf "Replaces: %s\n" "$PKG_REPLACES"
  printf "Description: %s\n" "$PKG_DESCRIPTION"
} > "$DEB_DEBIAN_DIR/control"

chmod 0644 "$DEB_DEBIAN_DIR/control"

DEB_PATH="$OUTPUT_DIR/${ARTIFACT_BASENAME}.deb"
dpkg-deb --build --root-owner-group "$DEB_STAGE" "$DEB_PATH" >/dev/null

printf "Created: %s\n" "$TARBALL_PATH"
printf "Created: %s\n" "$DEB_PATH"
