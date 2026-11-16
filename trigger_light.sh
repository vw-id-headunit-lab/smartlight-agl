#!/bin/sh
# Go runtime by default; explicit Python compatibility fallback remains available.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "${SMARTLIGHT_RUNTIME:-go}" = python ]; then
    if command -v python2.7 >/dev/null 2>&1; then
        exec python2.7 "$HERE/smartlight_trigger.py" "$@"
    elif command -v python3 >/dev/null 2>&1; then
        exec python3 "$HERE/smartlight_trigger.py" "$@"
    fi
    echo 'Select Python 2.7/3 for the explicit legacy fallback.' >&2
    exit 1
fi
BIN=${SMARTLIGHT_BIN:-"$HERE/smartlight-agl"}
if [ ! -x "$BIN" ]; then
    echo 'Build/install the Go smartlight-agl binary, or explicitly select SMARTLIGHT_RUNTIME=python.' >&2
    exit 1
fi
exec "$BIN" "$@"
