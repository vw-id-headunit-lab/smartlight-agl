#!/bin/sh
# CarPlay monitor uses the Go runtime only.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN=${SMARTLIGHT_BIN:-"$HERE/smartlight-agl"}
if [ ! -x "$BIN" ]; then
    echo 'Build/install the Go smartlight-agl binary first.' >&2
    exit 1
fi
exec "$BIN" carplay "$@"
