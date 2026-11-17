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
max-duration shorter than hold/interval, slow preflight at the window deadline,
service-side POST references through a path-rewriting proxy, and routes that
stop or change their maneuver reference during the final pre-POST recheck.

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
