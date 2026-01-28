#!/usr/bin/env bash
set -euo pipefail

REPO="RichardAnderson/vito-local"
BINARY_NAME="vito-root-service"
INSTALL_DIR="/usr/local/bin"
SYSTEMD_DIR="/etc/systemd/system"
SERVICE_USER="${VITO_USER:-vito}"

# Check root
if [[ $EUID -ne 0 ]]; then
    echo "Error: This script must be run as root" >&2
    exit 1
fi

# Check dependencies
for cmd in curl tar; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "Error: '$cmd' is required but not installed" >&2
        exit 1
    fi
done

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  GOARCH="amd64" ;;
    aarch64) GOARCH="arm64" ;;
    *)
        echo "Error: Unsupported architecture '$ARCH'. Supported: x86_64, aarch64" >&2
        exit 1
        ;;
esac

# Verify user exists
if ! id "$SERVICE_USER" &>/dev/null; then
    echo "Error: User '$SERVICE_USER' does not exist." >&2
    echo "Create the user first, or set VITO_USER to the desired username:" >&2
    echo "  VITO_USER=myuser $0" >&2
    exit 1
fi

# Determine version to install
if [[ -n "${VITO_VERSION:-}" ]]; then
    VERSION="$VITO_VERSION"
    echo "Installing $BINARY_NAME $VERSION (from VITO_VERSION)..."
else
    echo "Fetching latest release..."
    VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name"' | head -1 | cut -d'"' -f4)"
    if [[ -z "$VERSION" ]]; then
        echo "Error: Could not determine latest release version" >&2
        exit 1
    fi
    echo "Installing $BINARY_NAME $VERSION..."
fi

# Download release
TARBALL="vito-root-service-${VERSION}-linux-${GOARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/${VERSION}/${TARBALL}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading $DOWNLOAD_URL..."
if ! curl -fsSL -o "$TMPDIR/$TARBALL" "$DOWNLOAD_URL"; then
    echo "Error: Failed to download release. Check that version '$VERSION' exists for linux/$GOARCH." >&2
    echo "  Available releases: https://github.com/$REPO/releases" >&2
    exit 1
fi

# Extract
echo "Extracting..."
tar xzf "$TMPDIR/$TARBALL" -C "$TMPDIR"

# Stop existing service if running
if systemctl is-active --quiet vito-root.service 2>/dev/null; then
    echo "Stopping existing service..."
    systemctl stop vito-root.socket vito-root.service
fi

# Install binary
echo "Installing binary to $INSTALL_DIR/$BINARY_NAME..."
install -m 0755 "$TMPDIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"

# Install systemd units
echo "Installing systemd units..."
install -m 0644 "$TMPDIR/systemd/vito-root.socket" "$SYSTEMD_DIR/"
install -m 0644 "$TMPDIR/systemd/vito-root.service" "$SYSTEMD_DIR/"

# Update service files with correct user if not default
if [[ "$SERVICE_USER" != "vito" ]]; then
    sed -i "s/-user vito/-user $SERVICE_USER/" "$SYSTEMD_DIR/vito-root.service"
    sed -i "s/SocketGroup=vito/SocketGroup=$SERVICE_USER/" "$SYSTEMD_DIR/vito-root.socket"
fi

# Reload and enable
systemctl daemon-reload
systemctl enable vito-root.socket
systemctl start vito-root.socket

echo ""
echo "Installation complete."
echo "  Binary:  $INSTALL_DIR/$BINARY_NAME"
echo "  Socket:  /run/vito-root.sock"
echo "  User:    $SERVICE_USER"
echo "  Version: $VERSION"
echo ""
echo "The socket is active. The service starts on first connection."
