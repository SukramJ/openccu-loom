# ADR 0057 — CCU add-on self-update

- Status: accepted
- Date: 2026-07-29

## Context

Installing or updating the CCU add-on requires the manual WebUI round
trip (download → upload → install). On OpenCCU the
WebUI is only a frontend: it stages the archive as
`/usr/local/tmp/new_addon.tar.gz` and runs the firmware's
`/bin/install_addon`, which unpacks, optionally verifies embedded
`*.sha256` files, executes the add-on's `update_script`, and finishes
without a reboot (the script's exit code decides). The daemon already
runs as root in the add-on context, so it can drive that same chain
itself. Stock eQ-3 CCU3 firmware has no such installer — its add-on
install is coupled to a reboot.

## Decision

1. **Self-update exists only where the firmware installer exists.**
   Capability `addon_self_update` = add-on build (`build.AddonBuild`)
   AND an executable `/bin/install_addon`. Everything — SPA card, REST
   verbs, WS broadcast, MQTT update entity — is derived from that
   capability; on unsupported platforms the surfaces are absent (REST
   404), not disabled-looking.
2. **Trust model: release-pinned, checksum-verified.** The updater
   only talks HTTPS to the project's own GitHub releases. The add-on
   tarball's SHA256 is verified against the release's `checksums.txt`
   BEFORE the archive is staged; the tarball additionally embeds
   `*.sha256` files so `install_addon` performs the firmware-side
   second check. No third-party feeds, no unsigned artefacts.
3. **Detached install.** The add-on's `update_script` stops the daemon
   that triggered the install (self-replacement), so the daemon spawns
   `install_addon` in a detached session (`setsid`) that survives its
   parent. Success is observed by the restarted daemon reporting the
   new version; the exit-0 path avoids any reboot.
4. **Check cadence: manual button + boot check (delayed) + every 24 h
   with random jitter (≤1 h).** The interval is configurable via
   `addon_update.check_interval` (zero falls back to the 24 h default);
   `addon_update.enabled=false` silences the background checking
   entirely while the manual verbs stay available.
   Updates here are operator conveniences, not security push-outs; the
   jitter keeps the fleet from stamping GitHub simultaneously.
5. **Surfaces.** REST `GET /system/addon-update` +
   `POST …/check|install` (operator-gated), WS broadcast
   `addon_update.state_changed`, and an MQTT Home Assistant `update`
   entity mirroring the hub firmware-update entity — so HA shows and
   triggers the add-on update natively.

## Consequences

- Stock CCU3 keeps the manual WebUI flow; the daemon's UI shows no
  update mechanism there. A reboot-coupled fallback was considered and
  rejected — restarting the whole CCU for an add-on update is a
  decision the operator should make in the CCU's own UI.
- A failed download/verify leaves the system untouched (staging is
  atomic: verify first, then move). A failed `update_script` surfaces
  through the firmware's exit codes and the journal; the previous
  binary keeps running unless the script already swapped it.
- The updater runs as root by nature of the add-on platform; the
  checksum pinning is the integrity boundary. Release-signing (beyond
  checksums) can be layered on later without changing the surfaces.
