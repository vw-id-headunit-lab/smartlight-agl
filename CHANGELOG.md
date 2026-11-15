# Changelog

## Unreleased

- List all purpose pages and inspect service state with read-only commands.
- Select navigation turns with `--direction left`, `--direction right`, `左转`,
  or `右转`, matched against live service metadata.
- Submit the service's real purpose name and relative URI.
- Save the POST response's `Location` before parsing its body, and clean up only
  that queue item after a bounded hold.
- Support minimal Python 2.7 images with curl and vendored CPython parsers.
- Retain legacy v1 state cleanup, default dry runs, and explicit state files.
- Add loopback tests, an opt-in remote Python harness, CI, and reproducible
  runtime package contents.
