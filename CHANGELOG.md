# Changelog

## Unreleased

- Derive the maneuver stage from the distance when the head unit omits `stage`
  (observed empty even at zero meters on HCN1.1.134), instead of aborting the
  monitor; stages that are provided keep their published meaning. A head unit that reports CarLife sessions as
  `appleCarPlay` therefore gets CarLife support from the same fallback.
- Keep repeat-mode CarPlay requests while their exact queue item remains present,
  avoiding three-second revoke/recreate interruptions during execute and across
  follow-to-execute. Replace only after service-side removal; retain one-shot
  holds, cleanup ownership, retry cadence and the original turn-window limit.
- Merge main boot startup/failure cadence into the Go monitor without restoring
  the deleted Python CarPlay implementation; retain no-receipt audit evidence
  and stop with exit 78 when known request cleanup requires recovery.
- Remove the duplicate Python CarPlay monitor/tests; root launchers are Go-only.
- Move the standalone manual Python trigger, dependencies, tests and builder to
  python-trigger/ with independent usage, rollback, packaging and CI.
- Migrate default runtime to standard-library Go and static ARM64 packaging.
- Preserve real-purpose request shape, Location ownership and v1/v2 recovery.
- Normalize CarPlay distance units/reject invalid values and retain semantic max window across refreshed ids.
- Document repeat/one-shot policy and separate registry credentials.

- Add Python 2.7/3 CarPlay navigation monitoring through the existing
  `simplenavigation` HTTP service; default to read-only polling.
- Reuse real-purpose selection, relative references, durable Location identity
  and standalone rollback; preserve the existing manual commands.
- Add one-pulse-per-current-maneuver lifecycle, source revalidation, interruption
  cleanup, local HTTP regression tests, launcher and release packaging.

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
