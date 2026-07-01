#!/bin/sh
# dnspick installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/lvjiaxuan/dnspick/main/install.sh | sh
#
# Optional environment variables:
#   DNSPICK_VERSION   version to install (default: latest), e.g. v2.0.0
#   DNSPICK_BIN_DIR   install directory (default: /usr/local/bin, falls back to $HOME/.local/bin without permission)

set -eu

REPO="lvjiaxuan/dnspick"
APP="dnspick"
VERSION="${DNSPICK_VERSION:-latest}"

# Colors (enabled only when stdout is a tty)
if [ -t 1 ]; then
	RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
else
	RED=''; GREEN=''; YELLOW=''; BLUE=''; NC=''
fi
info() { printf '%b\n' "${BLUE}==>${NC} $1"; }
ok()   { printf '%b\n' "${GREEN}✓${NC} $1"; }
warn() { printf '%b\n' "${YELLOW}⚠${NC} $1"; }
die()  { printf '%b\n' "${RED}✗ $1${NC}" >&2; exit 1; }

# 1. Detect the operating system
os=$(uname -s)
case "$os" in
	Linux)  OS=linux ;;
	Darwin) OS=darwin ;;
	*) die "unsupported OS: $os (on Windows, download the .zip package, see README)" ;;
esac

# 2. Detect the CPU architecture
arch=$(uname -m)
case "$arch" in
	x86_64|amd64)  ARCH=amd64 ;;
	arm64|aarch64) ARCH=arm64 ;;
	*) die "unsupported architecture: $arch" ;;
esac

# 3. Pick a download tool
if command -v curl >/dev/null 2>&1; then
	DL='curl -fsSL -o'
elif command -v wget >/dev/null 2>&1; then
	DL='wget -qO'
else
	die "curl or wget is required"
fi

# 4. Build the download URL
ASSET="${APP}-${OS}-${ARCH}.tar.gz"
if [ "$VERSION" = "latest" ]; then
	URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
	URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

info "platform: ${OS}/${ARCH}, version: ${VERSION}"

# 5. Download and extract into a temp directory
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
info "downloading ${ASSET} ..."
$DL "$TMP/$ASSET" "$URL" || die "download failed: $URL"
tar -xzf "$TMP/$ASSET" -C "$TMP" || die "extraction failed"

# The binary in the archive is named dnspick-<os>-<arch>; rename it to dnspick.
BIN_SRC="$TMP/${APP}-${OS}-${ARCH}"
[ -f "$BIN_SRC" ] || BIN_SRC="$TMP/$APP"
[ -f "$BIN_SRC" ] || die "no executable found in the archive"
chmod +x "$BIN_SRC"

# 6. Choose the install directory
if [ -n "${DNSPICK_BIN_DIR:-}" ]; then
	BIN_DIR="$DNSPICK_BIN_DIR"
elif [ -w /usr/local/bin ] 2>/dev/null || [ "$(id -u)" = "0" ]; then
	BIN_DIR=/usr/local/bin
else
	BIN_DIR="$HOME/.local/bin"
fi
mkdir -p "$BIN_DIR"

DEST="$BIN_DIR/$APP"
if mv "$BIN_SRC" "$DEST" 2>/dev/null; then :; else
	warn "administrator privileges are required to write to $BIN_DIR"
	sudo mv "$BIN_SRC" "$DEST" || die "installation failed"
fi

# 7. macOS: clear the Gatekeeper quarantine flag
if [ "$OS" = "darwin" ] && command -v xattr >/dev/null 2>&1; then
	xattr -dr com.apple.quarantine "$DEST" 2>/dev/null || true
fi

ok "installed to ${DEST}"

# 8. PATH hint
case ":$PATH:" in
	*":$BIN_DIR:"*) ;;
	*) warn "${BIN_DIR} is not in PATH; add it, for example:"
	   printf '    export PATH="%s:$PATH"\n' "$BIN_DIR" ;;
esac

printf '%b\n' "${GREEN}Done!${NC} Run ${BLUE}${APP} --help${NC} to get started."
