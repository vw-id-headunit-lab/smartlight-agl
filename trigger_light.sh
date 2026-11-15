#!/bin/sh
# POSIX shell launcher; firmware includes /usr/bin/python2.7.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if command -v python2.7 >/dev/null 2>&1; then
    exec python2.7 "$HERE/smartlight_trigger.py" "$@"
elif command -v python3 >/dev/null 2>&1; then
    exec python3 "$HERE/smartlight_trigger.py" "$@"
else
    echo 'Install/select Python 2.7 or Python 3 first.' >&2
    exit 1
fi
