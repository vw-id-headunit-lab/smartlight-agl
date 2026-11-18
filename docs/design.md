# Go design

cmd/smartlight-agl: signal-aware CLI. internal/smartlight/client.go: stdlib HTTP,
URI/origin/prefix/page bounds, registry and GET-only retries. state.go: real
purpose/preflight, exclusive v1/v2 state, Location first, exact rollback.
navigation.go: source snapshot/semantic revalidation, strict units, bounded turn
window and lock. app.go: separate credentials and lifecycle contexts.

POST body is real purpose name+validated service-side path. Explicit proxy prefix
mapping changes transport URLs, not the upstream purpose reference in JSON;
state and cleanup retain the mapped transport identity. State0600 intent precedes
mutation; Location validated/synced before JSON. Malformed/HTTP500 replies still
preserve cleanup identity. Unknown v2 identity remains pending; no shared-name
deletion or mutation retry. Legacy unique-name v1 keeps baseline/metadata limits.

Cleanup checks saved service/prefix/purpose/non-overlap/available metadata,
DELETEs exact URI then confirms absence. 404/410 may mean completed one-shot.
Signals cannot skip bounded POST persistence or independent cleanup context;
SIGKILL, power loss and unknown server completion remain explicit limits.

CarPlay can ignore an individual POST failure after exact-URI cleanup succeeds,
or archive a no-Location intent as ignored-post-*.json without guessing a DELETE.
The evidence is retained, not falsely marked cleaned. Successful queue ownership
and known-identity cleanup failure still block new requests and return exit 78.
Manual trigger and standalone cleanup keep their stricter recovery semantics.
Skipped/failed pulses share the hold cadence so polling cannot hammer POST.
The optional boot-start.sh systemd unit uses RestartPreventExitStatus=78.

HTTP: no environment proxy/redirect/insecureTLS mode, body2MiB/header64KiB,
GET transport at most3 attempts, no status or mutation retry. Registry/light/nav
auth never inherit. Page links stay same-origin/resource, max100 and cycle check.

Controller follows continuous semantic context+derived direction, not mutable
element id. Repeat mode retains a successful request through follow/execute and
ID refreshes while its exact queue identity remains present. Hold sets the minimum
completion-check/retry cadence, not a forced revoke deadline. Only confirmed
service-side removal allows settling the old receipt and starting another request.
One-shot and skipped/failed attempts retain the hold deadline. Active repeat
requests, navigation reads and completion checks honor the original turn-window
deadline; DELETE/confirmed cleanup still precedes a replacement POST.
Expired preflight work must not start a new POST. A POST already in flight still
finishes under its bounded timeout to preserve Location before independent
cleanup, so network/persistence/cleanup latency can extend actual visible timing.
Recoverable snapshot changes wait at the normal polling cadence, with cancellable
waits rather than a busy loop. Same-direction turns with no observable boundary
share the window rather than inventing turn identity.

The pre-POST recheck reads context, maneuver, then context again. A stopped route,
empty reference, changed context or changed maneuver reference skips that attempt;
malformed or cross-origin references remain errors. A same-direction element ID
refresh before this recheck is allowed, but a change during its reads is not.

The independent manual Python tool lives in python-trigger/ with its parsers,
licenses, tests and package builder. Root launchers and CarPlay are Go-only;
Go packages need none of the Python files. See go-migration.md for layout.

Speed adaptation is enabled by default but can be disabled with
`--adaptive-follow=false`. A read-only VIWI tree discovers Displayspeed links on
the configured origin, or an explicit speed URI bypasses discovery. Only the
unambiguous velocityPrimary.currentValue is accepted, with response/configured
units normalized to km/h. The default formula is speed/3.6*12s, clamped 50..500m;
invalid/unavailable/stale samples fall back to the fixed follow-distance. Reads
are rate-limited and bounded by both a speed budget and active pulse deadlines.
No speed credentials inherit from navigation or lighting. Speed-only threshold
changes revoke ineligible pulses without rearming the same turn's window budget.
