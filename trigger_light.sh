#!/bin/sh
# Go runtime only. Standalone Python trigger lives under python-trigger/.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN=${SMARTLIGHT_BIN:-"$HERE/smartlight-agl"}
if [ ! -x "$BIN" ]; then
    echo 'Build/install the Go smartlight-agl binary. For Python use python-trigger/trigger_light.sh.' >&2
    exit 1
fi
exec "$BIN" "$@"
