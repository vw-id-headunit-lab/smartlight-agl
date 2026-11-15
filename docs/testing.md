# Testing

## Workstation loopback regression

Use Python 3.10+ and curl on Linux or macOS:

```sh
python3 tests/test_trigger.py
sh -n trigger_light.sh rollback.sh
python3 tools/build_release.py
```

The suite starts an HTTP server bound to `127.0.0.1` on a random port. It never
connects to a vehicle service. It checks real-name/relative-purpose submission,
status-only POST replies, direction aliases, all pages, invalid locations,
dry runs, interrupted POSTs, standalone rollback, credentials, and preservation
of other requests. It verifies vendored hashes against `vendor/SOURCES.json`.

Reports go to `artifacts/mock-verification.json`. Runtime archives and SHA-256
sidecars go under `dist/`. Both directories are ignored. GitHub Actions runs the
suite on Python 3.10, 3.12, and 3.14.

## Optional remote Python 2.7 harness

This opt-in harness tests an embedded interpreter against the workstation mock.
It needs SSH, scp's legacy `-O` mode, remote port forwarding, curl, and a
compatible remote Python runtime:

```sh
python3 tests/test_agl_runtime.py --target root@HEADUNIT_HOST
```

The target is mandatory; no vehicle address is stored in code. Use
`--python /path/to/python2.7` to select another remote executable.

The harness creates a fresh remote temporary directory, copies runtime files,
forwards a loopback mock port over SSH, and runs mocked POST/DELETE tests with
the remote interpreter. It removes its staging directory and closes SSH
afterwards. It never uses a vehicle SmartLight URI.

The report goes to `artifacts/python2-mock.json`. Local reports can contain
paths or SSH diagnostics; inspect them before sharing.

## Field observations

On 2026-11-15, a minimal AGL Python 2.7.15 image with curl 7.63.0 listed all
58 purposes across three pages. Six read-only probe collections worked. A
left-turn request returned 201 with Location, the player selected that item,
the user confirmed visible light, and DELETE returned 200 with verified
absence. Right-turn matching was checked by a dry run.

These observations cover one setup. Original field captures, specifications,
addresses, and backups remain outside this public repository. Default CI
requires no vehicle access.
