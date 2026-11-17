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
