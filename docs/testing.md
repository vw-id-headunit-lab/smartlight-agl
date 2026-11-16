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

Original Python tests remain legacy reference, not Go validation; shell cases
require SMARTLIGHT_RUNTIME=python from a source checkout. tools/build_release.py
builds the legacy Python package. Go package uses go run ./cmd/build-release.
