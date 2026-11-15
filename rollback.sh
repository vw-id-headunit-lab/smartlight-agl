#!/bin/sh
# Delete only the unique request recorded in --state; never reset other lights.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec sh "$HERE/trigger_light.sh" cleanup "$@"
