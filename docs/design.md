# Design

## Runtime boundaries

`trigger_light.sh` selects Python 2.7 when installed, otherwise Python 3.
`smartlight_trigger.py` implements exact command parsing, URI checks, paginated
reads, selection, preflight, state transitions, and cleanup. `agl_runtime.py`
implements private temporary files, random identifiers, and curl execution
without a shell. `rollback.sh` delegates to the same cleanup implementation.

Minimal Python 2.7 images may lack `io`, `json`, `urlparse`, `urllib2`, `socket`,
`ssl`, `subprocess`, `uuid`, and `tempfile`. The client uses `codecs`,
`os.urandom`, and application-owned temporary directories. Unmodified CPython
2.7.18 JSON and URL parser sources supply missing pure-Python modules.
ASCII/percent-encoded service URIs avoid a dependency on `unicodedata`.

curl is invoked with an argument vector and bounded connection/request timeouts.
Its default configuration, proxy variables, redirects, and retries are disabled.
TLS verification remains enabled. Authorization values are written into a mode
600 config file within a mode 700 temporary directory and are removed afterwards.

## Purpose selection and POST contract

`--direction` matches live metadata exactly: `navigation`, `direction`, and the
selected `left`/`right` value. Names and resource UUIDs are discovered. Name/id/URI
selectors remain available for other effects. Every selector must match exactly
one element.

The validated request body is:

```json
{
  "name": "NAME_FROM_LIVE_PURPOSE",
  "purpose": "/smartlight/purposes/UUID_FROM_LIVE_PURPOSE"
}
```

The tested service rejects arbitrary random request names. The client uses the
real purpose name and a validated relative purpose path. It checks configuration
and the matching setting group without changing switches or brightness.

## Request identity and state

The purpose name is shared by other clients, so it is not a safe cleanup
identity. New runs use `smartlight-short-request-v2` state and store the exact
queue URI from the POST response's `Location` header.

State is created exclusively before POST. Updates are written to a new file,
flushed, and renamed over the previous state. Responses are recorded before
interpreting JSON, including a response containing only `{"status":"ok"}`.
A random local run id never becomes the service request name.

Cleanup validates the URI's origin and resource, rejects overlap with the
baseline queue, checks available name/purpose metadata, and deletes that exact
URI. It verifies absence afterwards. An item that already ended can return
404/410. Other items, including another client's same-name request, remain.

Without a trustworthy saved Location after an uncertain POST, cleanup retains
state and fails rather than attributing shared-name items to this run.
`--confirm-absent` does not override the v2 identity requirement. Explicit
validation rejections without Location are recorded as rejected and need no
queue mutation. POST is never automatically retried.

Legacy v1 states remain supported with their original unique random name,
purpose URI, and baseline queue boundary. Legacy ambiguity confirmation applies
only to that old identity model.

## Interruptions and verification limits

During POST, SIGINT/SIGTERM are deferred until the bounded request finishes and
response identity is saved; then cleanup runs. During cleanup, further such
signals are ignored. This cannot survive SIGKILL, power loss, an unreachable
service, or a crash before response identity was saved.

The hold duration is a client wait before DELETE, not a server TTL. Latency and
playback scheduling affect visible timing. A player response can indicate which
request is selected; it does not prove the physical light is visible or that a
completed light effect is reversible.
