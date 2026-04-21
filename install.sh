#!/bin/sh
# wuu installer — downloads the latest release binary.
# Usage: curl -fsSL https://raw.githubusercontent.com/blueberrycongee/wuu/main/install.sh | sh

set -e

REPO="blueberrycongee/wuu"
INSTALL_DIR="/usr/local/bin"

# Detect OS.
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin) OS="darwin" ;;
  linux)  OS="linux" ;;
  *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture.
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)             echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version.
echo "Fetching latest wuu release..."
VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')"
if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest version."
  exit 1
fi
echo "Latest version: v${VERSION}"

# Download.
FILENAME="wuu_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${FILENAME}"
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt"
TMPDIR="$(mktemp -d)"
echo "Downloading ${URL}..."
curl -fsSL "$URL" -o "${TMPDIR}/${FILENAME}"

# Verify checksum. The release artifact lives on GitHub behind HTTPS,
# but a tampered or partially uploaded tarball would still be accepted
# without this check. Fail hard if the checksum doesn't match.
echo "Verifying checksum..."
curl -fsSL "$CHECKSUMS_URL" -o "${TMPDIR}/checksums.txt"
EXPECTED="$(grep "  ${FILENAME}$" "${TMPDIR}/checksums.txt" | awk '{print $1}')"
if [ -z "$EXPECTED" ]; then
  echo "No checksum entry for ${FILENAME} in checksums.txt"
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMPDIR}/${FILENAME}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${TMPDIR}/${FILENAME}" | awk '{print $1}')"
else
  echo "No sha256sum or shasum available; cannot verify checksum."
  exit 1
fi
if [ "$ACTUAL" != "$EXPECTED" ]; then
  echo "Checksum mismatch for ${FILENAME}"
  echo "  expected: ${EXPECTED}"
  echo "  got:      ${ACTUAL}"
  exit 1
fi

# Extract.
tar -xzf "${TMPDIR}/${FILENAME}" -C "$TMPDIR"

# Install.
# Note: chmod must run with the same privilege as mv. Otherwise a
# non-sudo chmod against a root-owned file fails under `set -e`,
# producing a "fake failure" after the binary is already in place.
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMPDIR}/wuu" "${INSTALL_DIR}/wuu"
  chmod +x "${INSTALL_DIR}/wuu"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "${TMPDIR}/wuu" "${INSTALL_DIR}/wuu"
  sudo chmod +x "${INSTALL_DIR}/wuu"
fi

# Cleanup.
rm -rf "$TMPDIR"

echo ""
echo "wuu v${VERSION} installed to ${INSTALL_DIR}/wuu"
echo "Run 'wuu' to start."
