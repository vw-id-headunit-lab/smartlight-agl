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
- carplay_post_ignored: the monitor skipped one failed pulse and continues at
  normal hold cadence after exact cleanup or no-Location evidence archival.
  ignored-post-*.json retains an uncertain receipt; it is not proof of absence.
- Exit78: known cleanup ownership remains pending. The optional boot service
  deliberately stops restart loops until its state is recovered.
- Stale lock: verify stopped process, clean unfinished states, then manually
  remove empty carplay-monitor.lock. No second-directory workaround.
- Hold is client timing, not serverTTL. Power loss/SIGKILL/failed network needs
  independent recovery. Local player state is not proof of visible light.

For the independent manual Python tool, use python-trigger/trigger_light.sh and
python-trigger/rollback.sh; root launchers are Go-only. State recovery remains
compatible across tools. See go-migration.md and ../python-trigger/README.md.

boot-start.sh writes/starts a runtime systemd unit with --apply; it is opt-in,
not a probe. Do not run it merely to check installation. See carplay.md for
the exact service name, log path, stop command and recovery boundaries.
