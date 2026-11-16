#!/bin/sh
# Keep the same minimal runtime selection as trigger_light.sh.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if command -v python2.7 >/dev/null 2>&1; then
    exec python2.7 "$HERE/carplay_monitor.py" "$@"
elif command -v python3 >/dev/null 2>&1; then
    exec python3 "$HERE/carplay_monitor.py" "$@"
else
    echo 'Select Python 2.7 or Python 3 first.' >&2
    exit 1
fi
