# Go design

cmd/smartlight-agl: signal-aware CLI. internal/smartlight/client.go: stdlib HTTP,
URI/origin/prefix/page bounds, registry and GET-only retries. state.go: real
purpose/preflight, exclusive v1/v2 state, Location first, exact rollback.
navigation.go: source snapshot/semantic revalidation, strict units, bounded turn
window and lock. app.go: separate credentials and lifecycle contexts.

POST body is real purpose name+relative validated path. State0600 intent precedes
mutation; Location validated/synced before JSON. Malformed/HTTP500 replies still
preserve cleanup identity. Unknown v2 identity remains pending; no shared-name
deletion or mutation retry. Legacy unique-name v1 keeps baseline/metadata limits.

Cleanup checks saved service/prefix/purpose/non-overlap/available metadata,
DELETEs exact URI then confirms absence. 404/410 may mean completed one-shot.
Signals cannot skip bounded POST persistence or independent cleanup context;
SIGKILL, power loss and unknown server completion remain explicit limits.

HTTP: no environment proxy/redirect/insecureTLS mode, body2MiB/header64KiB,
GET transport at most3 attempts, no status or mutation retry. Registry/light/nav
auth never inherit. Page links stay same-origin/resource, max100 and cycle check.

Controller follows continuous semantic context+derived direction, not mutable
element id. Latest-main repeat+near-follow policy stays, with intentional unit/
invalid-value/non-resetting-window corrections. Poll deadline follows pulse
expiry; DELETE precedes new POST. Same-direction turns with no observable boundary
share the window rather than inventing turn identity. Network affects timing.

Python/vendor remain unchanged explicit legacy fallback. Go package needs neither.
See go-migration.md for CLI/build/behavior differences.
