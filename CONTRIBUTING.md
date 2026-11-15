# Contributing

Use Python 3.10 or newer and curl on a POSIX development machine. Runtime files
must remain compatible with Python 2.7 and Python 3, using only modules present
on the minimal target or the documented vendored modules.

```sh
python3 tests/test_trigger.py
sh -n trigger_light.sh rollback.sh
python3 tools/build_release.py
```

Tests run against a workstation loopback mock. Reports are written under
`artifacts/` and are ignored by Git. The optional remote interpreter harness is
described in [testing](docs/testing.md). It requires an explicit SSH target and
must never contact a vehicle SmartLight service.

When changing queue handling, cover the service's real purpose name, relative
purpose reference, status-only response, and server-assigned `Location` URI.
Keep POST retries disabled, default to a dry run, and preserve requests that
were already queued. A missing identity must never become permission to delete
items by their shared purpose name.

Use placeholder addresses in documentation. Do not commit credentials, vehicle
identifiers, captures, deployment backups, runtime state, or extracted firmware
specifications. Preserve `vendor/LICENSE` and validate `vendor/SOURCES.json` if
updating the vendored files.
