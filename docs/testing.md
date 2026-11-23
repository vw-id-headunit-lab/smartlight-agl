# Go runtime tests

```sh
go test -count=1 -v ./...
go test -race ./...       # Linux CI; supported local race toolchain required
go vet ./...
go run ./cmd/build-release
```

Only localhost httptest services, no vehicle/SSH. Tests cover default dry-run,
exact POST/name/path/Location, status-only201, error/malformed body rollback,
unknown identity, existing queue and disabled groups, URI/prefix/IPv6/traversal,
pagination cycles, GET-only transport retry, redirects, credential isolation,
units and invalid values, semantic max-window across id churn, one-shot/session
reset, complete CLI polling/cleanup, cancellation and locks.

Review regressions also cover snapshot-change polling cadence and cancellation,
stage windows shorter than hold/interval, slow preflight at the stage deadline,
service-side POST references through a path-rewriting proxy, and routes that
stop or change their maneuver reference during the final pre-POST recheck.
Repeat regressions keep the exact owned request through both stage deadlines,
traffic delays and ID refreshes. They cover a new execute window after follow
expires, replacement after confirmed removal, hold pacing, out-of-window queue
checks, accepted in-flight POSTs, and cleanup at navigation boundaries.

Speed tests cover default-on/explicit-off options, unit conversion and malformed
samples, bounded read-only discovery and cached URIs, rate limits/fallback/age,
and complete high/low-speed CarPlay mock requests with exact cleanup. The monitor
regression checks that an accepted request pauses sampling through replacement,
sample expiry, and stage expiry, then resumes at a turn boundary.
All tests use local speed endpoints or explicitly disable adaptation.

CI records go-test-json and cross-builds static ARM64. Local outputs/hashes stay
under ignored artifacts/dist. Cross-build is not target execution or light proof.

The independent manual Python suite is separate from Go validation:

```sh
python3 python-trigger/tests/test_trigger.py
python3 python-trigger/tools/build_release.py
sh -n python-trigger/trigger_light.sh
sh -n python-trigger/rollback.sh
```

Its report/package stay under python-trigger/artifacts and python-trigger/dist.
The whole directory can run without root runtime files. Duplicate Python
CarPlay code/tests were removed; Go CarPlay is covered by the Go suite.
