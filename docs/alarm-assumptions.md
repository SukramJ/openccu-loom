# Alarm engine: researched assumptions (§18 grounding)

This document records what web/manual/forum research established for the open questions in [docs/alarm-concept.md](./alarm-concept.md) §18, the implementation assumptions the alarm engine derives from that research, and the confidence per claim (`[confirmed]` / `[likely]` / `[contested]` / `[unknown]`). It complements — never replaces — the supervised live tests §18 calls out, which remain parked until the operator schedules them and names the target device (see [CLAUDE.md](../CLAUDE.md), "Live-CCU writes need explicit user approval").

---

## Q1 — ASIR/ASIR-2/ASIR-O firmware duration cap (§18 Q1)

**Established**

- [confirmed] No official eQ-3 manual (indoor ASIR, ASIR-2, outdoor ASIR-O) states a max acoustic/optical duration; the only "3 Minuten" occurrences describe the teach-in/pairing window, not alarm duration. https://www.eq-3.de/downloads/download/homematic_ip/bda/HmIP-ASIR_UM_web.pdf
- [likely] ELV's retailer page for the outdoor ASIR-O states duration is selectable up to a max of 3 minutes, framed as a configurable ceiling, not an automatic legal shutoff. https://de.elv.com/p/homematic-ip-smart-home-alarmsirene-hmip-asir-o-aussen-ip44-solarbetrieben-P153208/
- [contested] The "3-minute German legal requirement" framing appears in a community blog but is uncorroborated by eQ-3 text; a dedicated forum investigation found no statute mandating it — the real basis is the voluntary VdS 2300 guideline (20-180s adjustable range) — one participant called the "legal requirement" framing a myth. https://www.alarmforum.de/showthread.php?tid=15047
- [confirmed] eQ-3's own firmware changelog for the indoor ASIR documents a real bug: durations >65s executed incorrectly before v1.0.9 (fixed 2016-01-19). https://www.eq-3.de/downloads/software/firmware/homematic_ip/changelog_HmIP_ASIR_update_V1_0_009_170119.txt
- [unknown] A claimed CCU2 2.53.27 / CCU3 3.53.26 change introducing a 600s cap could not be independently confirmed from the changelog text.
- [likely] 2023 reports, well after the 2016 fix, still describe ASIR-2 sirens stopping after a few seconds regardless of configured duration; a proposed reorder-the-writes fix is unconfirmed. https://homematic-forum.de/forum/viewtopic.php?t=64165

**Implementation assumption**: no device- or firmware-side duration cap is a safety backstop, confirmed or not. The engine's own stop watchdog is the sole authoritative bound: every siren `TurnOn` unconditionally schedules an engine-side stop at configured-duration + safety margin, sized to comfortably exceed worst-case duty-cycle retry latency (~2×40s under this repo's current retry defaults); an overrun is logged/alerted, never silently tolerated.

**Still needs live verification**: whether indoor ASIR/ASIR-2 firmware silently truncates or extends configured durations on the operator's actual firmware, and whether ASIR-O firmware enforces a hard 3-minute ceiling vs. a configurable range max. → §18 Q1 supervised live test.

---

## Q2 — `DURATION_VALUE = 0` semantics (§18 Q2)

**Established**

- [unknown] No source states or demonstrates what `DURATION_VALUE = 0` does on the device with a tone/optical signal selected — no-op vs. infinite sounding vs. something else is undocumented everywhere searched.
- [confirmed] Community practice and the aiohomematic reference implementation avoid `DURATION_VALUE = 0` entirely: to silence a siren, both community scripts and aiohomematic's `turn_off()` rewrite `ACOUSTIC_ALARM_SELECTION`/`OPTICAL_ALARM_SELECTION` to disable plus a concrete non-zero duration pair. https://forum.iobroker.net/topic/7897/gel%C3%B6st-homeatic-ip-alarm-siren-%C3%BCber-script-steuern
- [confirmed] All four paramset values (`ACOUSTIC_ALARM_SELECTION`, `OPTICAL_ALARM_SELECTION`, `DURATION_UNIT`, `DURATION_VALUE`) must be written together; partial writes are widely reported ignored, or as causing the device to re-execute a previous pattern. https://homematic-forum.de/forum/viewtopic.php?t=76596
- [likely] Direct writes to `ACOUSTIC_ALARM_ACTIVE`/`OPTICAL_ALARM_ACTIVE` are rejected with an XML-RPC error; at least one first-hand report confirms this. https://github.com/SukramJ/aiohomematic/discussions/734

**Implementation assumption**: the engine never emits `DURATION_VALUE = 0`; every `TurnOn` writes a concrete positive duration pair, and every stop is one atomic `putParamset` carrying all four parameters, matching `internal/model/custom/siren/siren.go`'s existing `TurnOff` path, dispatched at `CommandPriority.Critical`. The stop watchdog — not `DURATION_VALUE` semantics — is the sole authoritative bound. `ACOUSTIC_ALARM_ACTIVE`/`OPTICAL_ALARM_ACTIVE` are read-only feedback; the engine never writes them.

**Still needs live verification**: the actual device behavior for `DURATION_VALUE = 0` with a tone selected. Since the engine never relies on it, this is informational only — it would help the reconciliation logic reason about a peer-caused zero-duration write, not change the engine's own write path. → §18 Q2 supervised live test.

---

## Q3 — Sensor→ASIR `:3` direct-link acceptance (§18 Q3)

**Established**: research here is thin — neither the ASIR nor SWSD research pass produced a source that directly tests whether CCU firmware accepts a sensor→ASIR `:3` `LINK`-paramset direct link, or specifically whether KRCA/SWSD pairings are refused. The one adjacent signal:

- [likely] Multiple users report abandoning direct script/`putParamset` siren control in favor of CCU virtual-button direct links, describing this as more reliable — though no thread diagnoses a root cause or tests sensor→ASIR pairing acceptance specifically. https://forum.iobroker.net/topic/38972/steuern-der-hmip-asir-o-sirene

**Implementation assumption**: with no evidence either way, the engine does not assume Tier C (always-armed direct links) is viable on HmIP firmware; Tier C ships gated behind the §18 Q3 live test (feature-catalogue row 24, P3). The adjacent signal (community preference for virtual-button links for siren *reliability*) does not resolve sensor→ASIR pairing acceptance, so it is not acted on architecturally.

**Still needs live verification**: everything — whether CCU firmware accepts a sensor→ASIR `:3` direct link at all, and whether KRCA/SWSD pairings are refused. Untouched by the current research pass. → §18 Q3 supervised live test.

---

## Q4 — WKP integration depth (§18 Q4)

**Established**

- [confirmed] Wire layout: channel `:0` (MAINTENANCE) carries `CODE_ID`, `CODE_STATE`, `BLOCKED_TEMPORARY`, `BLOCKED_PERMANENT`, `SABOTAGE(_STICKY)`, `USER_AUTHORIZATION_01..08`, `CLEAR_ERROR`; channels `:1`-`:16` are 8 `ACCESS_TRANSCEIVER` pairs (odd = `PRESS_LOCK` + `NUMERIC_PIN_CODE`, even = `PRESS_UNLOCK`). https://raw.githubusercontent.com/SukramJ/pydevccu/master/pydevccu/device_descriptions/HmIP-WKP.json
- [confirmed] `PRESS_LOCK`/`PRESS_UNLOCK` are declared `OPERATIONS=4` (EVENT only) — momentary push events, never polled/persisted state.
- [confirmed] By default `PRESS_LOCK`/`PRESS_UNLOCK` value-changes are frequently NOT forwarded via XML-RPC until CCU-side interest is established — typically a dummy WebUI Program referencing the channel. https://github.com/jens-maus/RaspberryMatic/issues/1846
- [likely] Each `ACCESS_TRANSCEIVER` channel exposes a writable MASTER param `ABORT_EVENT_SENDING_CHANNELS` (0-65535, default 1), plausibly the switch behind that quirk, clearable via `putParamset` instead of the WebUI-program workaround.
- [likely] `CODE_ID` emits sentinel values outside its declared paramsetDescription MAX=8 (idle/unknown reports `CODE_ID=32`); the same bug class is confirmed and patched for the sibling HmIP-FWI (`CODE_ID=31` vs. declared MAX=20/21). https://forum.iobroker.net/topic/55966/hmip-fwi-code_id-has-value-31-greater-than-max-20
- [confirmed] `CODE_ID`/`CODE_STATE` alone cannot disambiguate Lock vs. Unlock intent — only the separate per-channel-pair `PRESS_LOCK`/`PRESS_UNLOCK` events carry that.
- [confirmed] The PIN is stored as `NUMERIC_PIN_CODE` — an ordinary MASTER paramset STRING (8-digit pattern), `OPERATIONS=3` (READ+WRITE) — technically writable via any client with device access; the WebUI/App is a friendly wrapper, not a protocol-level exclusivity gate.
- [confirmed] Resetting Sabotage/Temporary/Permanent lockouts requires two steps: a WebUI "Sperren zurücksetzen" action, then physically confirming on the device's "Verriegeln" button — the WebUI action alone is insufficient. https://technikkram.net/blog/2022/05/29/quicktipp-keypad-hmip-wkp-servicemeldungen-zuruecksetzen/
- [contested] Temporary-lockout base delay: 30s per the CCU3 FAQ PDF vs. 15s per the newer (v1.1, 01/2023) App user manual. https://homematic-ip.com/sites/default/files/downloads/faq-servicemeldungen-hmip-wkp.pdf

**Implementation assumption**: the engine correlates `CODE_ID` (channel `:0`) with `PRESS_LOCK`/`PRESS_UNLOCK` (per channel-pair) by channel-pair-index == `CODE_ID` and arrival within a short time window, rather than trusting either signal alone; it gates all `CODE_ID` interpretation on `CODE_STATE == KNOWN_CODE_ID_RECEIVED` and never validates/clamps `CODE_ID` against its declared paramsetDescription MAX. At pairing/startup the engine proactively ensures event delivery for channels 1-16 (clearing `ABORT_EVENT_SENDING_CHANNELS` via `putParamset`). On-device PIN slots stay independent of engine codes: `CODE_ID` is an opaque identity token mapped to operator-assigned channel labels ("User N"), never a PIN value. The engine never writes `NUMERIC_PIN_CODE`, `ACCESS_AUTHORIZATION`, `BLOCKING_*`, or `CLEAR_ERROR` — those stay WebUI/operator-owned. `BLOCKED_TEMPORARY`/`BLOCKED_PERMANENT`/`SABOTAGE(_STICKY)` surface as health/diagnostic signals only.

**Still needs live verification**: whether `CODE_ID`/`PRESS_LOCK`/`PRESS_UNLOCK` events are reliably delivered out of the box on the operator's actual firmware without the `ABORT_EVENT_SENDING_CHANNELS` workaround; the real `CODE_ID` idle sentinel value on the operator's unit; the actual temporary-lockout base delay (30s vs. 15s) — none safe to hard-code from secondhand sources alone.

---

## Q7 — Current-app verification of cited HmIP behaviours (§18 Q7)

**Established**

- [confirmed] "Scharfschalten pro" checks the battery state of security sensors and refuses to arm if low; it also blocks arming on open windows/tamper. https://homematic-ip.com/sites/default/files/downloads/homematic-ip-anwenderhandbuch.pdf
- [likely] The arming-refusal UI names the specific offending sensor by device/room name inline in a "cannot arm" dialog at the moment of the failed attempt.
- [confirmed] In the current app, tapping "Bestätigen" on a triggered-alarm push disarms the mode, while "Abbrechen" closes the message but leaves the mode active and suppresses further pushes for that episode.
- [confirmed] This Bestätigen/Abbrechen behavior does not apply to smoke-detector fire alarms — remote deactivation via the app is not offered, for personal-safety reasons; only physical-detector silencing works.
- [confirmed] The Basis vs. Pro arming-mode distinction is unchanged and applies identically on the HCU1, using the same app flow as the classic Access Point.

**Implementation assumption**: the concept's citations of pre-HCU manual behaviour are confirmed current as of the 2025-06-05 Anwenderhandbuch — §18 Q7 is resolved by documentation research, no live test needed for these claims. The alarm-engine design avoids the literal "Bestätigen"/"Abbrechen" labels (a documented confusion source) in favor of unambiguous verbs ("Disarm" / "Dismiss (stay armed)"). Fire/smoke hazard detection stays always-live independent of arm state and offers no remote silence/disarm from the app surface, mirroring the confirmed personal-safety invariant.

**Still needs live verification**: nothing semantic — Q7 closes on documentation alone for the three cited behaviours. Only exact current UI copy/wording (not semantics) would benefit from a screenshot pass against a live HCU1/app pairing, out of scope for engine behaviour.

---

## Q8 — SWSD smoke-detector sounders: self-termination and propagation (§18 Q8)

**Established**

- [confirmed] `SMOKE_DETECTOR_COMMAND` (channel 1) is a 6-value enum including `1=INTRUSION_ALARM_OFF`, `2=INTRUSION_ALARM`; writing `2` triggers the intrusion sounder, `1` is the documented explicit stop command. https://www.eq-3.de/downloads/download/handbuecher/WebUI_Handbuch_eQ-3.pdf
- [confirmed] `SMOKE_DETECTOR_ALARM_STATUS` is a 4-value enum: `IDLE_OFF`, `PRIMARY_ALARM` (local smoke), `INTRUSION_ALARM` (this device commanded as siren), `SECONDARY_ALARM` (relayed from another grouped device; German UI label "fremdausgelöster Alarm").
- [likely] Whether a commanded `INTRUSION_ALARM` self-terminates after a device-side timer is unconfirmed/undocumented; the strongest evidence is a forum thread literally titled "Dauer Einbruchalarm?" with no verified test result — one operator built their own ~3-minute external cutoff for lack of a confirmed answer. https://homematic-forum.de/forum/viewtopic.php?t=41251
- [contested] A separate thread claims "there is no ALARM_OFF command", contradicting the confirmed enum values above. https://homematic-forum.de/forum/viewtopic.php?t=34685
- [likely] Commanding `INTRUSION_ALARM` on one HmIP-SWSD likely propagates by default to every other HmIP-SWSD auto-linked into the same smoke-detector group (up to 40 devices) — unlike real smoke-alarm forwarding, which is gated by a per-device checkbox.
- [likely] A non-addressed grouped peer reports `SECONDARY_ALARM`, not `INTRUSION_ALARM` — that value is reserved for the directly addressed device.
- [confirmed] Using an HmIP-SWSD as an additional intrusion output measurably shortens its battery life below the rated ~10 years, because the battery is permanently built in and non-user-replaceable; community reports go as low as ~2 years to depletion.

**Implementation assumption**: HmIP-SWSD is modeled as a "no built-in duration parameter" output class (unlike HmIP-ASIR's `DURATION`/`DURATION_UNIT`) — the engine's per-output max-run-time watchdog (S2) is the only bound on how long any SWSD sounds. `SMOKE_DETECTOR_COMMAND = INTRUSION_ALARM` is treated as a level command with no assumed device-side auto-off and is always paired with the stop watchdog explicitly writing `INTRUSION_ALARM_OFF` on timer elapse or disarm. Because group-propagation is only "likely," a single `INTRUSION_ALARM_OFF` write is not trusted to silence a whole group: if the engine tracks per-device alarm state for SWSD outputs, it polls/subscribes `SMOKE_DETECTOR_ALARM_STATUS` on every grouped SWSD and, on stop, explicitly writes `INTRUSION_ALARM_OFF` to every device that was ever the addressed target during the episode. The SWSD-as-output driver stays architecturally separate from the SWSD-as-input hazard path — the derived smoke hazard sensor maps only `PRIMARY_ALARM`/`SECONDARY_ALARM`, never the commanded `INTRUSION_ALARM` (feature-catalogue row 8 in [docs/alarm-concept.md](./alarm-concept.md)). The battery-wear caveat is surfaced wherever the SPA offers an SWSD as a candidate output, and SWSD ships as an engine-watchdog-only opt-in, never on by default, until §18 Q8 resolves.

**Still needs live verification**: whether a commanded `INTRUSION_ALARM` self-terminates after any device-side timer or sounds indefinitely until `INTRUSION_ALARM_OFF`, and whether it actually propagates to grouped detectors as `SECONDARY_ALARM`. Both remain the explicit §18 Q8 gate before smoke sounders can ship on by default. → §18 Q8 supervised live test.

---

## Alarmo / HmIP app design implications

From `research_app-alarmo.md`, informing UX and phasing beyond the numbered §18 questions:

- Model arm-refusal on Alarmo's `FAILED_TO_ARM` event shape (an explicit list of `{sensor, name}` blocking the attempt) rather than an ambiguous status icon — stronger and more directly confirmed than anything documented for the current HmIP app.
- [confirmed] The app's "Rauchwarnmelder-Alarm" option is a single **global** toggle, not configurable per arming mode: enabling it makes all installed smoke detectors sound as an auxiliary siren on intrusion, separate from each detector's own always-on fire detection. Loom's per-mode assignment ([docs/alarm-concept.md](./alarm-concept.md) §7) is therefore a deliberate superset of the app's capability, not parity: the global behaviour is expressible (assign to every mode), the reverse is not. The concept's recommended default — full protection only — stands.
- Ship a per-sensor debounce/hold-time (`delay_on`-equivalent) and a per-sensor entry-delay override as first-class MASTER-editable fields — both are validated, long-requested, now-shipped Alarmo patterns (v1.10.16, closing issue #1289) worth adopting directly.
- No mainstream open-source reference (Alarmo) ships countdown chirp/beep orchestration natively despite a 2+ year open request (#910); if OpenCCU-Loom wants this, design it as its own audio-output-driver subsystem — a genuine differentiator, not a port.
- For multi-area/master-panel arming, adopt Alarmo's "master modes = intersection of all areas" constraint, and design push/actionable notification payloads to carry an area identifier from day one — Alarmo itself cannot target a single area in an actionable notification, a known, still-unaddressed rough edge worth avoiding.
- For the MQTT `alarm_control_panel` surface, explicitly decide and document which contract is followed — HA-core's `code_arm_required`/`custom_bypass` semantics (arm-scoped code flag, first-class custom-bypass state) versus Alarmo's own bridge semantics (single non-arm-scoped "require code" flag, JSON-embedded code/area) — rather than silently conflating the two, since they differ on whether disarm is separately code-gated.

---

## Sources

### ASIR / siren stop semantics

https://homematic-forum.de/forum/viewtopic.php?t=56606 · https://homematic-forum.de/forum/viewtopic.php?t=82178 · https://homematic-forum.de/forum/viewtopic.php?f=58&t=77424 · https://technikkram.net/blog/2020/07/31/quicktipp-ansprechen-einer-hmip-alarmsirene-im-zentralenprogramm/ · https://technikkram.net/blog/2019/06/29/homematic-ip-alarmsirene-aussen-asir-o-endlich-verfuegbar/ · https://community.symcon.de/t/homematic-ip-alarmsirene-schalten-hmip-asir/45005 · https://forum.iobroker.net/topic/38972/steuern-der-hmip-asir-o-sirene · https://forum.iobroker.net/topic/29941/homematic-alarm-sirene-einstellungen-f%C3%BCr-iobroker · https://forum.iobroker.net/topic/66229/homematic-ip-sirene-hmip-asir-auf-laut-oder-still-%C3%A4ndern · https://forum.iobroker.net/topic/7897/gel%C3%B6st-homeatic-ip-alarm-siren-%C3%BCber-script-steuern · https://forum.iobroker.net/topic/14142/gel%C3%B6st-skript-f%C3%BCr-homematic-ip-innensirene · https://github.com/ioBroker/ioBroker.hm-rpc/issues/180 · https://github.com/iobroker-community-adapters/ioBroker.hmip/issues/16 · https://github.com/rdmtc/RedMatic/issues/150 · https://github.com/SukramJ/aiohomematic/discussions/734 · https://www.eq-3.de/downloads/download/homematic_ip/bda/HmIP-ASIR_UM_web.pdf · https://homematic-ip.com/sites/default/files/downloads/hmip-asir-2-um-web.pdf · https://cdn-reichelt.de/documents/datenblatt/E910/HMIP_ASIR-O_ANL-DE.pdf · https://homematic-ip.com/sites/default/files/downloads/hmip-asir-o-153208a0-produktdatenblatt.pdf · https://de.elv.com/p/homematic-ip-smart-home-alarmsirene-hmip-asir-o-aussen-ip44-solarbetrieben-P153208/ · https://de.elv.com/p/homematic-ip-smart-home-alarmsirene-hmip-asir-2-innen-P153825/ · https://www.eq-3.de/downloads/software/firmware/homematic_ip/changelog_HmIP_ASIR_update_V1_0_009_170119.txt · https://www.alarmforum.de/showthread.php?tid=15047 · https://homematic-forum.de/forum/viewtopic.php?t=41251 · https://homematic-forum.de/forum/viewtopic.php?t=76596 · https://homematic-forum.de/forum/viewtopic.php?t=64165 · https://homematic-forum.de/forum/viewtopic.php?t=67947 · https://community.openhab.org/t/solved-hmip-asir-homematic-ip-siren-config-rule-problems/83179 · https://community.home-assistant.io/t/is-homematic-hmip-asir-siren-fully-supported/356693

### SWSD smoke-detector sounders

https://homematic-forum.de/forum/viewtopic.php?t=34685 · https://homematic-forum.de/forum/viewtopic.php?t=53206 · https://homematic-forum.de/forum/viewtopic.php?t=56818 · https://homematic-forum.de/forum/viewtopic.php?t=63211 · https://homematic-forum.de/forum/viewtopic.php?t=63503 · https://homematic-forum.de/forum/viewtopic.php?t=75200 · https://homematic-forum.de/forum/viewtopic.php?t=74692 · https://homematic-forum.de/forum/viewtopic.php?t=48050 · https://technikkram.net/blog/2018/03/13/homematic-ip-rauchwarnmelder-hmip-swsd-zusaetzlich-als-alarmsirene-nutzen/ · https://forum.fhem.de/index.php?topic=80462.0 · https://de.elv.com/elvwissen/elvhilft/homematic-sicherheitssteuerung/ · https://de.elv.com/elvwissen/elvhilft/hilfestellungen-zu-homematic-ip-rauchmeldern-und-sicherheitstechnik/ · https://homematic-ip.com/de/produkt/rauchwarnmelder-mit-q-label · https://www.eq-3.de/downloads/download/handbuecher/WebUI_Handbuch_eQ-3.pdf · https://github.com/SukramJ/hahomematic/discussions/955 · https://github.com/SukramJ/hahomematic/discussions/759 · https://community.home-assistant.io/t/homematic-smoke-detector-homematicip-hmip-swsd-service-for-triggering-alarm/152022 · https://community.home-assistant.io/t/homematic-ip-smoke-detector-hip-swsd-setup-binary-alarm-state-sensor-for-integration-in-alarm-panel/674056

### WKP keypad

https://homematic-ip.com/sites/default/files/downloads/faq-servicemeldungen-hmip-wkp.pdf · https://homematic-ip.com/sites/default/files/downloads/hmip-wkp-um-web.pdf · https://technikkram.net/blog/2022/05/29/quicktipp-keypad-hmip-wkp-servicemeldungen-zuruecksetzen/ · https://github.com/jens-maus/RaspberryMatic/issues/1846 · https://github.com/jens-maus/RaspberryMatic/issues/2187 · https://github.com/jens-maus/RaspberryMatic/issues/2375 · https://github.com/jens-maus/RaspberryMatic/issues/1567 · https://github.com/eq-3/occu/issues/86 · https://homematic-forum.de/forum/viewtopic.php?t=80009 · https://homematic-forum.de/forum/viewtopic.php?t=74115&start=50 · https://homematic-forum.de/forum/viewtopic.php?t=74115&start=100 · https://homematic-forum.de/forum/viewtopic.php?t=74115&start=120 · https://homematic-forum.de/forum/viewtopic.php?t=83151 · https://homematic-forum.de/forum/viewtopic.php?f=58&t=76808 · https://community.simon42.com/t/alarmo-und-hmip-wkp/32584 · https://community.simon42.com/t/hmip-wkp-von-homematic-einbinden/23363 · https://community.symcon.de/t/erfahrungen-mit-hmip-wkp/129217 · https://forum.iobroker.net/topic/55966/hmip-fwi-code_id-has-value-31-greater-than-max-20 · https://github.com/SukramJ/aiohomematic/issues/1101 · https://github.com/SukramJ/aiohomematic/pull/1102 · https://github.com/SukramJ/homematicip_local/pull/481 · https://github.com/SukramJ/pydevccu/pull/43 · https://raw.githubusercontent.com/SukramJ/pydevccu/master/pydevccu/paramset_descriptions/HmIP-WKP.json · https://raw.githubusercontent.com/SukramJ/pydevccu/master/pydevccu/device_descriptions/HmIP-WKP.json

### Alarmo / HmIP app UX

https://homematic-ip.com/sites/default/files/downloads/homematic-ip-anwenderhandbuch.pdf · https://technikkram.net/blog/2019/04/20/kurzerklaerung-hmip-access-point-scharfschalten-basic-scharfschalten-pro/ · https://homematic-forum.de/forum/viewtopic.php?t=46598 · https://homematic-forum.de/forum/viewtopic.php?t=81505 · https://homematic-forum.de/forum/viewtopic.php?t=85128 · https://homematic-forum.de/forum/viewtopic.php?t=41355 · https://github.com/nielsfaber/alarmo/issues/1289 · https://github.com/nielsfaber/alarmo/pull/1345 · https://github.com/nielsfaber/alarmo/issues/1014 · https://github.com/nielsfaber/alarmo/issues/910 · https://github.com/nielsfaber/alarmo/issues/1118 · https://github.com/nielsfaber/alarmo/issues/1308 · https://github.com/nielsfaber/alarmo/issues/1309 · https://github.com/nielsfaber/alarmo/releases · https://github.com/nielsfaber/alarmo/blob/main/README.md · https://www.home-assistant.io/integrations/alarm_control_panel.mqtt/ · https://community.home-assistant.io/t/alarm-system-beep-chirp-sound-suggestions/89283
