# SmartLight via CarPlay — CarPlay navigation to ID.Light

[简体中文](README.md) | [English](README.en.md)

Connect **turn guidance from iPhone CarPlay navigation, including AMap, to the ID.Light strip**.
The program reads the head unit's existing `simplenavigation` service and requests effects through
SmartLight. It does not capture the display, decode raw iAP2, or modify the phone app.

**Normal setup: deploy the Go runtime → configure endpoints → dry-run → enable integration.**
The target head unit needs no Python, curl, or Go compiler. Manual triggering is only a debugging
tool for the lighting path, not the normal workflow; see [debugging](docs/debugging.md) (Chinese).

> This independent community tool uses existing service access. It does not modify firmware,
> brightness, or feature switches. The Go implementation has been tested with local mocks,
> not validated on a physical vehicle. Start with stationary checks.

## 1. Prepare and deploy

Requirements:

- An iPhone connected through CarPlay, with navigation actively providing route guidance.
- An accessible `simplenavigation` service and SmartLight service on the head unit.
- The required ID.Light navigation/turn features enabled in the vehicle HMI.
- A runtime built for the target architecture; the default package is static Linux ARM64.

On a development machine with Go 1.22+, build from the repository root:

```sh
go run ./cmd/build-release
```

This creates `dist/smartlight-agl-go-linux-arm64.tar.gz` and its `.sha256` file.
Copy the package to your chosen head-unit directory and extract it. **Run the remaining commands
inside the extracted directory**:

```sh
tar -xzf smartlight-agl-go-linux-arm64.tar.gz
cd smartlight-agl
```

Keep the `smartlight-agl` binary, `carplay_light.sh`, `rollback.sh`, `trigger_light.sh`, and optional
`boot-start.sh` together. Do not deploy a Windows executable to AGL.

### Download a GitHub Release

Every push to `main`, or manual CI run on `main`, publishes a
[GitHub Release](https://github.com/vw-id-headunit-lab/smartlight-agl/releases) only after both
the Go and Python checks pass. Choose the `main-<full commit SHA>` release. For normal CarPlay
integration, download **`smartlight-agl-go-linux-arm64.tar.gz`** and its `.sha256` file,
not the separate Python debugging package.

```sh
sha256sum -c smartlight-agl-go-linux-arm64.tar.gz.sha256
```

Pull requests, other branches, tag pushes, and failed CI runs do not publish. Re-running the same
commit updates its release and same-named assets rather than creating duplicate versions.
Test logs remain Actions artifacts and are not attached to the release. Automatic publication
does not establish physical vehicle validation; perform the dry-run below first.

## 2. Configure the lighting endpoint and navigation discovery

Set these variables in the current shell:

```sh
# The supplied SmartLight endpoint
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"

# Query navigation discovery from the head unit's AGL environment
REGISTRY='http://[::1]:80'
```

Keep IPv6 brackets and write `http://` directly, without a backslash.
`--nav-registry` queries `/?name=simplenavigation` and uses the returned service URI.
**Do not reuse SmartLight port 49520 as the navigation port, or treat the registry as the service.**

`[::1]` is the current system's loopback. This registry address refers to the head-unit environment,
not your workstation or its Android partition. The tool does not create cross-system AGL/QNX forwarding.

## 3. Start with a read-only dry-run

Connect CarPlay and start a route in the phone's navigation app, then run:

```sh
# Read 10 polling rounds and exit. Without --apply, no effect is created.
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" --iterations 10
```

Inspect the JSON logs:

| Event | What to check |
|---|---|
| `carplay_started` | `apply` must be `false`; the input is the existing navigation service |
| `carplay_snapshot` | `active`, `stage`, `direction`, and `distance_m` when available |
| `plan` / `dry_run` | A real effect was selected when eligible, without a write |

`active=false` means no active CarPlay route was found; it is not an instruction to trigger a turn
manually. If the current maneuver is absent, check CarPlay guidance, the registry result,
credentials, and network context before adding `--apply`.

## 4. Enable automatic integration

After the dry-run matches the current route, run:

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --interval 2 --hold 3 --max-duration 30 \
  --state-dir /tmp/smartlight-carplay
```

The direct binary command is equivalent:

```sh
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --interval 2 --hold 3 --max-duration 30 \
  --state-dir /tmp/smartlight-carplay
```

> **Display flicker: use `--interval 2 --hold 3` (also the current defaults).**
> An `--interval` below 2 seconds or a `--hold` below 3 seconds may cause the display to flicker.

This consumes navigation from an **iPhone through CarPlay**, not a standalone Android AMap APK.
Only the active `appleCarPlay` context's current `nextManeuver` is followed, not future maneuvers.
Lighting purposes are matched by direction from the live catalog; movie numbers and resource UUIDs
are not hard-coded.

### Default trigger policy

- Trigger during `execute`, or during `follow` with a valid remaining distance at or below the current threshold.
- **Speed adaptation is enabled by default**: `distance = speed(km/h) / 3.6 * 12 seconds`, clamped to **50–500 meters**; unavailable speed falls back to **150 meters**.
- Normalize `m/km/mi/yd/ft`. Do not guess turns from straight/unknown directions or invalid distances.
- Each pulse has a three-second target hold. Revoke the old request before creating another eligible pulse.
- Limit a continuous semantic turn window to 30 seconds; refreshed element IDs do not reset it.
- A far/inactive/noneligible state, direction change, or context change ends the window.

These are current S2995 platform semantics, not universal vehicle or CarPlay values.
`--hold` is a client cleanup deadline, not a server TTL; network and cleanup confirmation affect timing.

### Optional adjustments

| Flag | Default | Purpose |
|---|---|---|
| `--interval` | `2` seconds | Delay between polling rounds; HTTP requests take additional time; below 2 may cause flicker |
| `--hold` | `3` seconds | Target hold for an individual request, range 0.1–5 seconds; below 3 may cause flicker |
| `--max-duration` | `30` seconds | Continuous semantic turn window, range 1–300 seconds |
| `--follow-distance` | `150` meters | Fallback when speed is unavailable, or fixed threshold when adaptation is disabled; zero permits only execute |
| `--adaptive-follow` | On | Adjust follow distance by speed; `--adaptive-follow=false` disables it |
| `--one-shot` | Off | One pulse per semantic turn window instead of repeating |
| `--timeout` | `2` seconds | Per-request timeout; GET transport failures get at most three attempts |
| `--iterations` | `0` | Zero runs continuously; a positive integer bounds polling rounds |
| `--state-dir` | Temporary directory + `smartlight-carplay` | Request ownership records and monitor lock |

### Default speed adaptation

| Current speed | Default follow threshold |
|---|---|
| 0–15 km/h | 50 meters (minimum) |
| 30 km/h | 100 meters |
| 60 km/h | 200 meters |
| 90 km/h | 300 meters |
| 120 km/h | 400 meters |
| ≥150 km/h | 500 meters (maximum) |

This is a tunable project policy, not an OEM rule or road-type detector. The same
speed produces the same threshold regardless of road classification.
The default read-only discovery entry is `http://[fd53:7cb8:383:3::99]:8080/`.
It follows returned URIs under `Displayspeed` and selects `type=velocityPrimary` /
`currentValue`, without guessing resource names or UUIDs. `--speed-unit auto`
requires an explicit response `unit=km/h|mph|m/s`, and also accepts VIWI enums
`kmPerHour` (km/h) and `milesPerHour` (mph). Missing or unknown units cause fallback.
Configured and response units are compared after alias normalization; conflicting
km/h and mph values are still rejected.

A read-only vehicle check on 2026-11-17 found that the default entry times out.
The local registry query `GET http://[::1]/?name=DisplaySpeed` returns
`::73:29721/DisplaySpeed/`; its returned collection is `/DisplaySpeed/speeds/`,
with unit `kmPerHour`. Registry names are case-sensitive: `name=Displayspeed`
returns no match. `boot-start.sh` uses this vehicle's confirmed collection;
query the registry and follow returned service URIs again for other vehicles.

With a confirmed speed URI and unit, skip discovery and dry-run first:

```sh
SPEED_URI='http://[fd53:7cb8:383:3::73]:29721/DisplaySpeed/speeds/'
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --speed-uri "$SPEED_URI" --iterations 10
```

Default `auto` works for this vehicle; explicit `--speed-unit km/h` also matches
`kmPerHour`. `mph` and `m/s` are also supported and must match the confirmed unit.
Use `--speed-auth-file` for separate speed credentials; lighting
and navigation credentials are not inherited. `adaptive_follow` and `carplay_snapshot`
logs expose `speed_available`, `speed_kph`, and `follow_distance_m`.

| Flag | Default | Meaning |
|---|---|---|
| `--speed-base` | The `::99:8080` entry above | Service tree when `--speed-uri` is absent |
| `--speed-unit` | `auto` | Read response unit or specify a confirmed unit |
| `--follow-lead-time` | 12 seconds | Speed-based lead time, range 1–30 seconds |
| `--follow-min-distance` / `--follow-max-distance` | 50 / 500 meters | Adaptive limits, range 1–1000 meters |
| `--speed-interval` | 2 seconds | Minimum wait after a speed read completes; at least 1 second |
| `--speed-timeout` | 1 second | Total speed read/discovery budget including GET retries |
| `--speed-max-age` | 5 seconds | Local receipt-age limit; at least interval+timeout |

Failed reads invalidate the old speed immediately and fall back. Freshness uses
local receipt time and **does not prove the upstream sensor is still updating**.
Speed affects only follow eligibility, not execute, the three-second hold, or the
30-second window. Lower thresholds revoke ineligible pulses without resetting the
same turn's one-shot/max-duration budget solely because speed fluctuated.

Disable adaptation and use a fixed 150-meter threshold (no speed requests):

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --adaptive-follow=false --follow-distance 150 --iterations 10
```

For execute-only, one-pulse-per-window behavior:

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --follow-distance 0 --one-shot --state-dir /tmp/smartlight-carplay
```

## 5. Stop and recover

Press **Ctrl-C** in the foreground, or send SIGTERM to the monitor. It stops reading and attempts
to revoke its own request without clearing other services' queue entries.

Power loss, SIGKILL, or a network failure can leave recovery state. Keep the `state_saved` /
`cleanup_pending` logs and use the **actual state path**:

```sh
sh rollback.sh --state '/tmp/smartlight-carplay/carplay-ACTUAL_RUN_ID.json'
```

Add `--auth-file /path/to/light-header.txt` if the lighting service requires credentials.
Do not run multiple instances or switch state directories to bypass pending requests or stale locks.

- `carplay_post_ignored`: continue at the normal cadence after a failed attempt. A no-Location intent
  is archived as `ignored-post-*.json`; **this does not prove that the server rejected it**.
- Known Location with failed cleanup: preserve the ownership record and exit **78**; recover first.
- Before removing an empty stale lock, confirm the old process has stopped and recover pending items.
  Follow [recovery details](docs/carplay.md) (Chinese), never delete by a shared purpose name.

## 6. Optional startup service

Complete foreground dry-run and integration checks first. An existing boot hook may invoke
`boot-start.sh`, or you can explicitly start its independent systemd service on the head unit:

```sh
# Creates/starts a service with --apply. This is not a read-only check.
sh boot-start.sh
```

It writes `/run/systemd/system/smartlight-carplay.service`, uses the HOST and registry configured
in the script, and logs to `/tmp/carplay-light.log` (rotated to `.1` at startup above 256 KiB). Ordinary failures restart
after five seconds; exit 78 suppresses restarts. It does not modify the existing boot hook, and the
unit under `/run` does not survive a reboot. For credentials or custom endpoints, adjust the script
arguments before using its credential-free default configuration.

Logs under `/tmp` may disappear on reboot. Existing deployment-directory logs
are not moved or deleted. Speed HTTP logs include `source=speed`;
`transport_error.error_kind` distinguishes timeout, connection refusal, unreachable
network, DNS failure, and other transport failures. `speed_read_timeout` means the
total speed-read budget expired. A navigation GET 200 is not proof of a speed read.

```sh
systemctl status smartlight-carplay.service
systemctl stop smartlight-carplay.service
```

Skip `boot-start.sh` if automatic startup is not needed. See [CarPlay details](docs/carplay.md) (Chinese)
for startup and recovery boundaries.

## 7. Authentication or an explicit navigation URI

When required, supply separate mode-600 files containing complete Authorization values:

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --auth-file /path/to/light-header.txt \
  --nav-auth-file /path/to/navigation-header.txt \
  --nav-registry-auth-file /path/to/registry-header.txt --apply
```

Specify only the required credentials. Lighting, navigation, and registry credentials do not inherit
from one another and are not stored in logs or state. An actual, confirmed navigation URI can
**replace** registry discovery:

```sh
# Replace with the confirmed navigation host/port, never guess a port.
NAV_BASE='http://HEADUNIT_HOST:NAV_PORT/simplenavigation/'
sh carplay_light.sh --base "$BASE" --nav-base "$NAV_BASE" --iterations 10
```

Choose exactly one of `--nav-base` and `--nav-registry`. If an existing proxy/forwarder advertises
another URI prefix, configure `--advertised-base` / `--nav-advertised-base` explicitly.
The tool does not create a forwarder.

## Debugging and development

**Manual lighting commands are debugging tools, not normal CarPlay setup steps.**

- [Debugging](docs/debugging.md) (Chinese): catalog/probe commands, short manual triggers, and the independent Python tool.
- [CarPlay details](docs/carplay.md) (Chinese): source validation, platform mappings, ownership, and startup boundaries.
- [Testing](docs/testing.md) · [Design](docs/design.md) · [Migration](docs/go-migration.md) · [Troubleshooting](docs/troubleshooting.md).

Developer checks: `go test -count=1 ./...`, `go vet ./...`, and `go test -race ./...` on a supported
toolchain. They use local mock services; cross-compilation and passing tests do not validate a physical light strip.

## License

Project-authored code uses [MIT](LICENSE). The Go package does not depend on Python
and includes Go/standard-library notices and source hashes from the actual build
toolchain. The separate Python debugging package retains its CPython parser
licenses. See [third-party notices](THIRD_PARTY_NOTICES.md) for each package's scope.
