#!/usr/bin/env bash
set -euo pipefail

BINARY_NAME="vito-root-service"
INSTALL_DIR="/usr/local/bin"
SYSTEMD_DIR="/etc/systemd/system"

# Check root
if [[ $EUID -ne 0 ]]; then
    echo "Error: This script must be run as root" >&2
    exit 1
fi

echo "Uninstalling $BINARY_NAME..."

# Stop and disable services
echo "Stopping services..."
systemctl stop vito-root.socket vito-root.service 2>/dev/null || true
systemctl disable vito-root.socket vito-root.service 2>/dev/null || true

# Remove files
echo "Removing files..."
rm -f "$INSTALL_DIR/$BINARY_NAME"
rm -f "$SYSTEMD_DIR/vito-root.socket"
rm -f "$SYSTEMD_DIR/vito-root.service"
rm -f /run/vito-root.sock

# Reload systemd
systemctl daemon-reload

echo "Uninstallation complete."
