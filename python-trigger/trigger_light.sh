#!/bin/sh
# Independent manual-trigger runtime; does not require or invoke Go.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if command -v python2.7 >/dev/null 2>&1; then
    exec python2.7 "$HERE/smartlight_trigger.py" "$@"
elif command -v python3 >/dev/null 2>&1; then
    exec python3 "$HERE/smartlight_trigger.py" "$@"
else
    echo 'Select Python 2.7 or Python 3 for the standalone trigger.' >&2
    exit 1
fi
