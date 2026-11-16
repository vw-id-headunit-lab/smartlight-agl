# Contributing

Default runtime: Go1.22+ standard library only. Static AGL ARM64 CGO_ENABLED=0.
Run go test -count=1 ./..., go vet ./..., go test -race ./... on supported hosts,
and go run ./cmd/build-release. Tests use local fixtures, never vehicle targets.

Preserve real name+relative purpose POST, status-only replies, Location identity,
existing requests and independent rollback. Default dry-run, no mutation retries;
retain unknown identity instead of shared-name deletion. Test/document deliberate
semantic changes. No credentials, state, captures, firmware or local backups in
Git. Keep vendor licenses/hashes; Python fallback tests do not validate Go.
README host is the user-requested example; tests use fixture hosts.
