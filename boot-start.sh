#!/bin/sh
# Called in the background by /lge/app_rw/ssh/boot-hook.sh.
set -eu

SMARTLIGHT_BOOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SMARTLIGHT_BOOT_UNIT=smartlight-carplay.service

case "${1:-}" in
    '')
        if /bin/systemctl is-active --quiet "$SMARTLIGHT_BOOT_UNIT"; then
            exit 0
        fi
        # Keep the monitor independent of the SSH hook; retry service startup
        # if navigation/network services are not ready yet. A known request
        # requiring cleanup still blocks --apply until explicitly recovered.
        # The target's systemd 239 cannot marshal RestartPreventExitStatus
        # through systemd-run. Write a runtime unit using its normal parser.
        umask 077
        SMARTLIGHT_BOOT_UNIT_PATH="/run/systemd/system/$SMARTLIGHT_BOOT_UNIT"
        mkdir -p /run/systemd/system
        cat > "$SMARTLIGHT_BOOT_UNIT_PATH.new-$$" <<EOF
[Unit]
Description=SmartLight CarPlay navigation monitor

[Service]
Type=simple
User=root
ExecStart=/bin/sh "$SMARTLIGHT_BOOT_DIR/boot-start.sh" --run
Restart=on-failure
RestartSec=5s
RestartPreventExitStatus=78
KillMode=mixed
TimeoutStopSec=45s
EOF
        chmod 644 "$SMARTLIGHT_BOOT_UNIT_PATH.new-$$"
        mv -f "$SMARTLIGHT_BOOT_UNIT_PATH.new-$$" "$SMARTLIGHT_BOOT_UNIT_PATH"
        /bin/systemctl daemon-reload
        exec /bin/systemctl start --no-block "$SMARTLIGHT_BOOT_UNIT"
        ;;
    --run) ;;
    *) echo 'Usage: boot-start.sh [--run]' >&2; exit 2 ;;
esac

umask 077
cd "$SMARTLIGHT_BOOT_DIR"
SMARTLIGHT_BOOT_LOG=/tmp/carplay-light.log
# The service runs as root; do not follow pre-existing symlinks in shared /tmp.
if [ -L "$SMARTLIGHT_BOOT_LOG" ] || [ -L "$SMARTLIGHT_BOOT_LOG.1" ]; then
    echo 'Refusing symlink at CarPlay log path.' >&2
    exit 1
fi
if [ -f "$SMARTLIGHT_BOOT_LOG" ] && [ "$(wc -c < "$SMARTLIGHT_BOOT_LOG")" -ge 262144 ]; then
    mv -f "$SMARTLIGHT_BOOT_LOG" "$SMARTLIGHT_BOOT_LOG.1"
fi
exec >> "$SMARTLIGHT_BOOT_LOG" 2>&1

HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"
REGISTRY='http://[::1]:80'
# Confirmed from the local registry's case-sensitive DisplaySpeed service.
# Use the returned collection, rather than an element UUID or the legacy tree.
SPEED_URI='http://[fd53:7cb8:383:3::73]:29721/DisplaySpeed/speeds/'

# Values below --interval 2 or --hold 3 may cause display flicker.
# Adaptive follow is on by default; add --adaptive-follow=false to disable it.
# If speed reading fails, follow-distance falls back to 150 meters.
exec /bin/sh "$SMARTLIGHT_BOOT_DIR/carplay_light.sh" \
    --base "$BASE" --nav-registry "$REGISTRY" \
    --speed-uri "$SPEED_URI" \
    --apply --interval 2 --hold 3 --timeout 5 --state-dir /tmp/smartlight-carplay
