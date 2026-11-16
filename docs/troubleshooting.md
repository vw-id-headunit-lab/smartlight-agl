# Go troubleshooting

- Missing/incorrect binary: build Linux ARM64 static package on developer host;
  keep binary and wrappers together. Cross-build is not target execution.
- HTTP307: registry != service. Use nav-registry GET /?name=simplenavigation or
  exact nav-base; mandatory one of them, not both.
- Auth: separate auth-file/nav-auth-file/nav-registry-auth-file; no inheritance
  into returned service, no state/log credentials. Use existing service access.
- Source: active current appleCarPlay required; Android local nav is not CarPlay.
  Multiple active contexts are ambiguous. No raw-iAP2 decoder is involved.
- Follow: valid units and finite nonnegative number within meter threshold;
  0.2km=200m. follow-distance0 deliberately disables follow triggering.
- Window: repeat is bounded, one-shot opts out. ID refresh cannot reset maximum;
  far/inactive/noneligible/direction/context change ends continuous turn.
- cleanup_pending: retain state, use cleanup with original service/prefix/auth.
  No Location after uncertain POST means no name-based delete, no POST retry.
- Stale lock: verify stopped process, clean unfinished states, then manually
  remove empty carplay-monitor.lock. No second-directory workaround.
- Hold is client timing, not serverTTL. Power loss/SIGKILL/failed network needs
  independent recovery. Local player state is not proof of visible light.

See go-migration.md for explicit Python fallback and compatible state recovery.
