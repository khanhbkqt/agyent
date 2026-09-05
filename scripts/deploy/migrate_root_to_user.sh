#!/usr/bin/env bash
# ==============================================================================
# agyent - Migration script: root to dedicated unprivileged agyent user
# ==============================================================================
set -euo pipefail

echo "[+] Starting agyent security migration: root -> unprivileged system user"

# 1. Ensure running as root
if [ "$(id -u)" -ne 0 ]; then
    echo "[-] Error: This script must be run as root." >&2
    exit 1
fi

TARGET_USER="agyent"
TARGET_GROUP="agyent"
TARGET_HOME="/home/agyent"

# 2. Create agyent system group and user if they do not exist
if ! getent group "$TARGET_GROUP" >/dev/null 2>&1; then
    echo "[+] Creating system group: $TARGET_GROUP"
    groupadd --system "$TARGET_GROUP"
fi

if ! id -u "$TARGET_USER" >/dev/null 2>&1; then
    echo "[+] Creating system user: $TARGET_USER"
    useradd --system \
        --gid "$TARGET_GROUP" \
        --home-dir "$TARGET_HOME" \
        --create-home \
        --shell /usr/sbin/nologin \
        --comment "agyent daemon service user" \
        "$TARGET_USER"
else
    echo "[*] User $TARGET_USER already exists"
fi

# Ensure home directory exists with 0700 permissions
mkdir -p "$TARGET_HOME"
chmod 0700 "$TARGET_HOME"

# 3. Migrate ~/.agyent directory from root if present
if [ -d "/root/.agyent" ] && [ ! -d "$TARGET_HOME/.agyent" ]; then
    echo "[+] Migrating /root/.agyent to $TARGET_HOME/.agyent"
    cp -a /root/.agyent "$TARGET_HOME/.agyent"
fi

# 4. Migrate ~/.gemini credentials from root if present
if [ -d "/root/.gemini" ] && [ ! -d "$TARGET_HOME/.gemini" ]; then
    echo "[+] Migrating /root/.gemini to $TARGET_HOME/.gemini"
    cp -a /root/.gemini "$TARGET_HOME/.gemini"
fi

# 5. Fix permissions on migrated directories
echo "[+] Enforcing 0700 / 0600 permissions on $TARGET_HOME"
chown -R "$TARGET_USER:$TARGET_GROUP" "$TARGET_HOME"
find "$TARGET_HOME" -type d -exec chmod 0700 {} +
find "$TARGET_HOME" -type f -exec chmod 0600 {} +

# 6. Update systemd service file if deployed
SERVICE_FILE="/etc/systemd/system/agyent.service"
if [ -f "$SERVICE_FILE" ]; then
    echo "[+] Updating $SERVICE_FILE with unprivileged user settings"
    sed -i 's/^User=root/User=agyent/' "$SERVICE_FILE"
    sed -i 's|^WorkingDirectory=/root|WorkingDirectory=/home/agyent|' "$SERVICE_FILE"
    sed -i 's|/root/\.agyent|/home/agyent/.agyent|g' "$SERVICE_FILE"
    
    echo "[+] Reloading systemd daemon"
    systemctl daemon-reload
    if systemctl is-active --quiet agyent; then
        echo "[+] Restarting agyent service"
        systemctl restart agyent
    fi
fi

echo "[✓] Migration to user '$TARGET_USER' completed successfully."
