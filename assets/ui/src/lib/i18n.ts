// Minimal i18n helper for the SPA. The Backend already ships
// translation catalogues at `internal/i18n/catalogs/{de,en}.json`
// for parameter / channel-type / message labels — this layer
// covers the strings the SPA itself owns (button labels, banners,
// nav items). Two-locale dictionary is held in memory; locale is
// driven by the existing `prefs.locale` store.
//
// Usage:
//   import { t } from "$lib/i18n";
//   t("nav.devices")
//   t("messages.ack", { name: msg.name })  // {name} placeholder
//
// Keys missing from the active locale fall back to the English
// string; a missing key in BOTH dictionaries returns the key
// itself so usability stays acceptable during migration.
//
// --- Two valid patterns for locale access in components ---
//
// Pattern A — display-only text (the common case):
//   Call `t(key)` directly; it reads `prefs.locale` reactively.
//   For JS date formatting read `prefs.locale` directly from this module.
//   Do NOT thread a `locale` prop just for rendering strings.
//
// Pattern B — server-side label resolution (the narrow exception):
//   Certain REST calls send `?locale=...` so the daemon can resolve
//   CCU-side labels in the right language (e.g. `api.uiSchema`,
//   `api.listLinks`, `api.linkableChannels`). Components that make
//   these calls DO need a `locale` prop so the caller can pass the
//   current value reactively. Keep the prop at the ChannelPanel,
//   DeviceLinks, LinkConfigPanel level where it feeds an API call,
//   and let App.svelte derive it from `prefs.locale` once for the
//   whole tree.

import { prefs } from "./stores/preferences.svelte";

type Catalog = Record<string, string>;

// English catalogue — canonical keys. Keep sorted alphabetically so
// the migration progress is easy to audit.
const EN: Catalog = {
  // --- Alarm panel (notes/concepts/alarm-concept.md §12). Distinct from the CCU
  //     alarm-messages surface (see "messages.*"): this is loom's own
  //     intrusion-alarm engine ("Alarm system" / "Alarmanlage"). ---
  "alarm.title": "Alarm system",
  "alarm.subtitle":
    "Zones, sensors, sirens — loom's local-first intrusion alarm.",
  "alarm.tab.overview": "Overview",
  "alarm.tab.sensors": "Sensors",
  "alarm.tab.outputs": "Outputs",
  "alarm.tab.policies": "Policies",
  "alarm.tab.codes": "Codes",
  "alarm.tab.journal": "Journal",
  "alarm.tab.walktest": "Walk test",
  // Arm modes (§4).
  "alarm.mode.disarmed": "Disarmed",
  "alarm.mode.perimeter": "Perimeter",
  "alarm.mode.full": "Full protection",
  "alarm.mode.night": "Night",
  "alarm.mode.vacation": "Vacation",
  "alarm.mode.custom": "Custom",
  // Arm-state-machine states (§5).
  "alarm.state.disarmed": "Disarmed",
  "alarm.state.arming": "Arming…",
  "alarm.state.armed": "Armed",
  "alarm.state.pending": "Entry delay…",
  "alarm.state.triggered": "Alarm",
  // Overview (§12.1).
  "alarm.overview.empty": "No alarm zones yet",
  "alarm.overview.empty.description":
    "Create your first zone with the setup wizard to start protecting rooms.",
  "alarm.overview.armed_by": "since {time}, by {user}",
  "alarm.overview.armed_at": "since {time}",
  "alarm.overview.silence_all": "Silence all sirens",
  "alarm.overview.reset_motion_all": "Reset motion ({count})",
  "alarm.overview.reset_motion_all.hint":
    "Clears the motion detectors that are currently triggered. A triggered detector reads as open and can block arming.",
  "alarm.overview.open_security": "Security & Safety",
  "alarm.readiness.ready": "ready",
  "alarm.readiness.blockers_title": "Blocking sensors",
  "alarm.readiness.warnings_title": "Warnings",
  // Bypass sheet (§12.1).
  "alarm.bypass.title": "Arm anyway?",
  "alarm.bypass.description":
    "These sensors are blocking the arm. Tick the ones to bypass, then force arm — nothing is bypassed silently.",
  "alarm.bypass.force_arm": "Force arm",
  "alarm.bypass.empty": "No blocking sensors.",
  // Countdown (§12.1).
  "alarm.countdown.exit": "Exit delay",
  "alarm.countdown.entry": "Entry delay",
  // Triggered surface (§12.1).
  "alarm.triggered.intrusion": "ALARM — Intrusion",
  "alarm.triggered.cause": "Triggered: {sensor} ({room}), {time}",
  "alarm.triggered.cause_short": "Triggered by {sensor}",
  "alarm.triggered.since": "since {time}",
  "alarm.triggered.silenced": "Sirens silenced",
  // Control actions (§5, §12.1).
  "alarm.action.disarm": "Disarm",
  "alarm.action.silence": "Silence sirens",
  "alarm.action.acknowledge": "Acknowledge",
  "alarm.action.reset_motion": "Reset motion ({count})",
  // Toasts.
  "alarm.toast.armed": "{zone} armed ({mode})",
  "alarm.toast.arming": "{zone} arming…",
  "alarm.toast.arm_failed": "Arming failed",
  "alarm.toast.disarmed": "{zone} disarmed",
  "alarm.toast.disarm_failed": "Disarm failed",
  "alarm.toast.silenced": "Sirens silenced",
  "alarm.toast.motion_reset": "Motion detectors reset: {count}",
  "alarm.toast.motion_reset_none": "No triggered motion detector to reset",
  "alarm.toast.motion_reset_partial":
    "Motion reset — succeeded: {reset}, failed: {failed}",
  "alarm.toast.motion_reset_failed": "Motion reset failed",
  "alarm.toast.silence_failed": "Silence failed",
  "alarm.toast.acknowledged": "Acknowledged",
  "alarm.toast.ack_failed": "Acknowledge failed",
  "alarm.toast.saved": "Saved",
  "alarm.toast.save_failed": "Save failed",
  "alarm.toast.deleted": "Zone deleted",
  "alarm.toast.delete_failed": "Delete failed",
  "alarm.toast.test_fired": "Test fired",
  "alarm.toast.test_failed": "Test failed",
  "alarm.toast.walktest_started": "Walk test started",
  "alarm.toast.walktest_stopped": "Walk test stopped",
  // Zone create / edit / delete.
  "alarm.zone.name": "Name",
  // Sensor picker (§12.2).
  "alarm.sensors.empty": "No sensors assigned",
  "alarm.sensors.empty.description":
    "Add security sensors and pick which modes they arm in.",
  "alarm.sensors.add": "Add sensor",
  "alarm.sensors.search": "Search…",
  "alarm.sensors.selected": "{count} selected",
  "alarm.sensors.modes": "Modes",
  "alarm.sensors.filter.room": "Room",
  "alarm.sensors.filter.function": "Function",
  "alarm.sensors.filter.area": "Area",
  "alarm.sensors.filter.type": "Type",
  "alarm.sensors.filter.status": "Status",
  "alarm.sensors.filter.all": "All",
  "alarm.sensors.filter.unassigned": "Unassigned only",
  "alarm.sensors.filter.assigned": "Assigned only",
  "alarm.sensors.view.cards": "Cards",
  "alarm.sensors.view.matrix": "Matrix",
  "alarm.sensors.bulk.assign": "Assign to mode",
  "alarm.sensors.bulk.remove": "Remove",
  "alarm.sensors.detail.title": "Sensor details",
  "alarm.sensors.state.unreach": "unreachable",
  // Sensor types (§6.1).
  "alarm.sensor_type.door": "Door",
  "alarm.sensor_type.window": "Window",
  "alarm.sensor_type.motion": "Motion",
  "alarm.sensor_type.tamper": "Tamper",
  "alarm.sensor_type.hazard": "Hazard",
  "alarm.sensor_type.panic": "Panic",
  // Per-sensor flags (§6.2).
  "alarm.flags.title": "Flags",
  "alarm.flag.use_exit_delay": "Exit delay",
  "alarm.flag.use_exit_delay.hint":
    "The sensor may be active while you leave: activations during the exit delay are ignored. Without this flag they trigger instantly.",
  "alarm.flag.use_entry_delay": "Entry delay",
  "alarm.flag.use_entry_delay.hint":
    "An activation starts the pending countdown instead of triggering instantly — the time you have to disarm after entering.",
  "alarm.flag.entry_delay_override": "Entry delay override (s)",
  "alarm.flag.entry_delay_override.hint":
    "Replaces the mode's entry delay for this sensor (seconds) — e.g. 60 for the garage door while the front door keeps 15. Empty uses the mode's default.",
  "alarm.flag.always_on": "Always on",
  "alarm.flag.always_on.hint":
    "Fires around the clock, independent of the armed state — for hazard sensors (smoke, water, gas) and panic buttons. Outputs follow the hazard/panic policies from the Policies tab.",
  "alarm.flag.allow_open_after_arming": "Allow open after arming",
  "alarm.flag.allow_open_after_arming.hint":
    "The sensor may stay open (e.g. a tilted window) while the zone arms; only a fresh activation after it cleared triggers.",
  "alarm.flag.arm_after_closing": "Arm after closing",
  "alarm.flag.arm_after_closing.hint":
    "Closing this sensor during the exit delay finishes arming early, after a short settle time.",
  "alarm.flag.bypass_auto": "Auto-bypass",
  "alarm.flag.bypass_auto.hint":
    "If this sensor would block arming, it is bypassed automatically until the next disarm instead of failing the arm; the bypass is recorded and visible.",
  "alarm.flag.trigger_when_unavailable": "Trigger when unavailable",
  "alarm.flag.trigger_when_unavailable.hint":
    "Treats the sensor becoming unreachable while armed as an activation. Off raises only a warning.",
  "alarm.flag.chime": "Door chime while disarmed",
  "alarm.flag.chime.hint":
    "Plays the door-chime tone on chirp outputs when this sensor activates while the zone is disarmed — never during a walk test.",
  "alarm.flag.panic_silent": "Silent panic (duress)",
  "alarm.flag.panic_silent.hint":
    "Activations fire the panic policy with all acoustic outputs suppressed — notifications only. For duress buttons that must not sound locally.",
  "alarm.flag.hold_time": "Hold time (s)",
  "alarm.flag.hold_time.hint":
    "The activation must persist this many seconds before it counts — filters twitchy motion sensors and rattling doors. If the sensor clears earlier, the activation is discarded. Never applied to always-on (hazard/panic) sensors.",
  "alarm.flag.group": "Cross-zoning group",
  "alarm.flag.group.hint":
    "Sensors sharing the same group name only trigger when a second member activates within 60 seconds. A single activation does not sound the alarm but is recorded in the journal.",
  "alarm.matrix.sensor": "Sensor",
  // Output picker (§7, §12.2).
  "alarm.outputs.empty": "No outputs assigned",
  "alarm.outputs.empty.description":
    "Add sirens or lights and assign them to loud/silent policies per mode.",
  "alarm.outputs.add": "Add output",
  "alarm.outputs.expert": "Expert mode",
  "alarm.outputs.expert.hint":
    "Show every modelled actuator, not just curated siren/light candidates.",
  "alarm.outputs.test": "Test fire",
  "alarm.outputs.test_optical_only": "Optical only",
  "alarm.outputs.test_optical_only.hint": "Tests with light only — no tone.",
  "alarm.outputs.test.confirm.title": "Test fire this output?",
  "alarm.outputs.test.confirm.body":
    "This briefly activates the real device (siren/light). Use optical-only to spare the neighbours.",
  "alarm.outputs.outdoor": "Outdoor",
  "alarm.outputs.outdoor.hint":
    'Marks this output as outdoor, so policies with "Exclude outdoor outputs" skip it.',
  "alarm.outputs.shared_with_ccu": "Shared with CCU programs",
  "alarm.outputs.shared_with_ccu.hint":
    "The output is also driven by CCU programs: Loom never switches it off automatically while the zone is disarmed.",
  "alarm.outputs.duration": "Duration (s)",
  "alarm.outputs.duration.hint":
    "Seconds one activation runs; acoustic activations are hard-capped at 600 s. Empty uses the bounded default.",
  "alarm.outputs.tone": "Tone",
  "alarm.outputs.tone.hint":
    "Tone label from the device's tone list. Empty plays the device's default alarm tone.",
  "alarm.outputs.optical_pattern": "Optical pattern",
  "alarm.outputs.optical_pattern.hint":
    "Light-pattern label from the device's list. Empty uses the device default.",
  "alarm.outputs.switched_caveat":
    "Convenience-grade: no sabotage contact, no battery backup, trivially unpluggable.",
  "alarm.outputs.smoke_caveat":
    "Smoke detectors double as sounders — no device-side duration, engine-watchdogged only, and repeated intrusion tones shorten battery life. Best on full protection only.",
  "alarm.outputs.device_default": "Device default",
  "alarm.outputs.channel_mismatch":
    "This channel cannot back the selected class — the enrollment predates channel validation and never fired. Saving is blocked until it is fixed or removed.",
  "alarm.outputs.channel_mismatch.repair": "Repair channel",
  "alarm.outputs.candidates.empty":
    "No eligible channels for this class. Expert mode lists every device without capability filtering.",
  "alarm.outputs.candidates.load_failed": "Loading output candidates failed",
  "alarm.outputs.soundfile": "Soundfile",
  "alarm.outputs.soundfile.hint":
    "MP3 soundfile played for chirps. Empty uses the device default.",
  "alarm.outputs.sysvar.central": "Central",
  "alarm.outputs.sysvar.central.hint":
    "CCU the variable lives on — the mirror writes (and, for managed variables, creates) it there.",
  "alarm.outputs.channel.hint":
    "Channel address as <device>:<channel>, e.g. 0001D3C9A4B2:3.",
  "alarm.outputs.chirp_arm_tone.hint":
    "Tone label for the arm squawk, from the device's tone list. Empty skips it on this output.",
  "alarm.outputs.chirp_disarm_tone.hint":
    "Tone label for the disarm squawk. Empty skips it on this output.",
  "alarm.outputs.sysvar.name": "Variable name",
  "alarm.outputs.sysvar.name.hint":
    "Created on the CCU automatically as a value-list variable mirroring the zone state (Unscharf … Alarm).",
  "alarm.outputs.sysvar.existing": "Use existing alarm variable",
  "alarm.outputs.sysvar.existing.hint":
    "Writes an operator-owned alarm-type variable: true while triggered, false otherwise. The variable is never created or retyped, and it accepts no inbound commands.",
  "alarm.outputs.sysvar.existing.badge": "existing",
  "alarm.outputs.sysvar.pick": "Alarm variable",
  "alarm.outputs.sysvar.none": "No alarm-type variables on this central.",
  "alarm.outputs.sysvar.load_failed": "Loading system variables failed",
  "alarm.outputs.sysvar.allow_disarm": "Allow disarm via variable",
  "alarm.outputs.sysvar.allow_disarm.hint":
    "Off (default): a CCU write can only arm — it can never disarm the zone. Enable only if you trust every CCU program that can write this variable.",
  "alarm.outputs.notification.note":
    "Emits a notification event to the enrolled planes when the zone alarms — no device involved. Configure the planes on the output card after adding.",
  "alarm.outputs.notify.mqtt": "MQTT event",
  "alarm.outputs.notify.mqtt.hint":
    "Publish a NOTIFICATION entry on the zone's MQTT alarm event topic.",
  "alarm.outputs.notify.webhook": "Webhook event",
  "alarm.outputs.notify.webhook.hint":
    "Forward an alarm_panel.notification event to the outbound webhook receivers.",
  // Output classes (§7).
  "alarm.output_class.acoustic_siren": "Acoustic siren",
  "alarm.output_class.acoustic_siren.hint":
    "A real siren device (e.g. HmIP-ASIR): tone and duration configurable, every activation bounded and stop-verified by the engine.",
  "alarm.output_class.switched_siren": "Plug-in siren",
  "alarm.output_class.switched_siren.hint":
    "A mains plug-in siren behind a switch actuator. Convenience grade: no sabotage contact, no battery backup, trivially unpluggable; the actuator must support device-side auto-off (ON_TIME).",
  "alarm.output_class.smoke_sounder": "Smoke-detector sounder",
  "alarm.output_class.smoke_sounder.hint":
    "Sounds enrolled smoke detectors for intrusion alarms. Costs detector battery, usually sounds the whole group, and offers no live test fire.",
  "alarm.output_class.optical_siren": "Optical siren",
  "alarm.output_class.optical_siren.hint":
    "The optical channel of a siren — signals without noise and may run longer than the acoustic cap.",
  "alarm.output_class.alarm_light": "Alarm light",
  "alarm.output_class.alarm_light.hint":
    "A switch or dimmer actuator as alarm light: on at trigger, off at silence or disarm.",
  "alarm.output_class.chirp": "Chirp",
  "alarm.output_class.chirp.hint":
    "Short confirmation tones only: arm/disarm squawks, countdown ticks and the door chime — never the loud alarm.",
  "alarm.output_class.notification": "Notification",
  "alarm.output_class.notification.hint":
    "Emits a deliberate notification event (MQTT, WebSocket, webhook) when the zone alarms — one-shot at fire time, never cancelled by silence. Each plane can be toggled per output.",
  "alarm.output_class.sysvar_mirror": "Sysvar mirror",
  "alarm.output_class.sysvar_mirror.hint":
    "Maintains a CCU system variable mirroring the alarm state — either a managed value-list variable (created automatically) or an existing alarm-type variable (true while triggered).",
  // Journal (§12.5).
  "alarm.journal.empty": "No journal entries",
  "alarm.journal.filter.zone": "Zone",
  "alarm.journal.filter.class": "Class",
  "alarm.journal.filter.from": "From",
  "alarm.journal.filter.to": "To",
  "alarm.journal.filter.all": "All",
  "alarm.journal.export_csv": "Export CSV",
  "alarm.journal.col.when": "Time",
  "alarm.journal.col.zone": "Zone",
  "alarm.journal.col.class": "Class",
  "alarm.journal.col.event": "Event",
  "alarm.journal.col.actor": "By",
  "alarm.journal.col.source": "Source",
  // Journal classes (§13.1).
  "alarm.journal_class.arm": "Arm",
  "alarm.journal_class.disarm": "Disarm",
  "alarm.journal_class.trigger": "Trigger",
  "alarm.journal_class.silence": "Silence",
  "alarm.journal_class.bypass": "Bypass",
  "alarm.journal_class.fault": "Fault",
  "alarm.journal_class.test": "Test",
  "alarm.journal_class.config": "Config",
  "alarm.journal_class.maintenance": "Maintenance",
  // Journal event vocabulary. The engine writes a stable snake_case token
  // per entry; the journal is the surface an operator reads after an alarm,
  // so every token the engine can emit needs a sentence here. A token
  // without a key falls back to the raw token (never the dotted key), so a
  // newly added engine event degrades to developer-readable rather than
  // broken.
  "alarm.journal_event.acknowledged": "Acknowledged",
  "alarm.journal_event.acoustic_budget_exhausted": "Acoustic budget exhausted",
  "alarm.journal_event.activation_during_downtime":
    "Activated while the daemon was down",
  "alarm.journal_event.always_on_activation": "Always-on sensor activated",
  "alarm.journal_event.arm_failed_on_restore": "Arming failed during restore",
  "alarm.journal_event.arm_reminder": "Arming reminder",
  "alarm.journal_event.armed": "Armed",
  "alarm.journal_event.armed_after_closing":
    "Armed after the last opening closed",
  "alarm.journal_event.arming_resumed": "Arming resumed",
  "alarm.journal_event.arming_started": "Arming started",
  "alarm.journal_event.auto_rearm_cancelled": "Auto re-arm cancelled",
  "alarm.journal_event.auto_rearm_deferred": "Auto re-arm deferred",
  "alarm.journal_event.auto_rearm_failed": "Auto re-arm failed",
  "alarm.journal_event.auto_rearm_mode_unavailable":
    "Auto re-arm mode unavailable",
  "alarm.journal_event.auto_rearm_resumed": "Auto re-arm resumed",
  "alarm.journal_event.auto_rearm_scheduled": "Auto re-arm scheduled",
  "alarm.journal_event.auto_rearmed": "Auto re-armed",
  "alarm.journal_event.central_lost_while_armed": "CCU lost while armed",
  "alarm.journal_event.central_restored": "CCU restored",
  "alarm.journal_event.code_action_failed": "Code action failed",
  "alarm.journal_event.code_locked_out": "Code entry locked out",
  "alarm.journal_event.code_lockout": "Code lockout started",
  "alarm.journal_event.code_missing": "Code required",
  "alarm.journal_event.code_permission_denied":
    "Code not permitted for this action",
  "alarm.journal_event.cross_zone_first_hit": "First cross-zone hit",
  "alarm.journal_event.disarmed": "Disarmed",
  "alarm.journal_event.disarmed_post_trigger": "Disarmed after an alarm",
  "alarm.journal_event.duress": "Duress code entered",
  "alarm.journal_event.failed_to_arm": "Failed to arm",
  "alarm.journal_event.implausible_clock_on_restore":
    "Implausible clock on restore",
  "alarm.journal_event.incident_load_failed": "Incident could not be loaded",
  "alarm.journal_event.incident_lost_on_restore": "Incident lost on restore",
  "alarm.journal_event.incident_persist_failed": "Incident could not be saved",
  "alarm.journal_event.invalid_code": "Invalid code entered",
  "alarm.journal_event.keypad_blocked": "Keypad blocked",
  "alarm.journal_event.keypad_press_unmatched": "Keypad entry did not match",
  "alarm.journal_event.mode_removed_while_armed":
    "Armed mode removed from the configuration",
  "alarm.journal_event.motion_reset": "Motion detectors reset",
  "alarm.journal_event.orphan_incident_adopted": "Orphaned incident adopted",
  "alarm.journal_event.orphan_incident_closed": "Orphaned incident closed",
  "alarm.journal_event.output_fire_failed": "Output could not be fired",
  "alarm.journal_event.output_stop_failed": "Output could not be stopped",
  "alarm.journal_event.output_stop_unverified": "Output stop unverified",
  "alarm.journal_event.pending_demoted_implausible_clock":
    "Entry delay dropped: implausible clock",
  "alarm.journal_event.pending_elapsed_while_down":
    "Entry delay elapsed while the daemon was down",
  "alarm.journal_event.pending_resumed": "Entry delay resumed",
  "alarm.journal_event.pending_started": "Entry delay started",
  "alarm.journal_event.pre_alarm_escalated": "Pre-alarm escalated to full alarm",
  "alarm.journal_event.pre_alarm_restored_as_full":
    "Pre-alarm restored as full alarm",
  "alarm.journal_event.reconcile_stopped_unowned_siren":
    "Stopped a siren nobody owned",
  "alarm.journal_event.refire_account_failed":
    "Output re-fire could not be accounted",
  "alarm.journal_event.restart_loop_breaker_degraded":
    "Restart-loop breaker degraded",
  "alarm.journal_event.retrigger_account_failed":
    "Retrigger could not be accounted",
  "alarm.journal_event.retrigger_cycle": "Retrigger cycle",
  "alarm.journal_event.schedule_arm_failed": "Scheduled arming failed",
  "alarm.journal_event.sensor_activity": "Sensor activity",
  "alarm.journal_event.sensor_activity_pending":
    "Sensor activity during the entry delay",
  "alarm.journal_event.sensor_bypassed": "Sensor bypassed",
  "alarm.journal_event.sensor_config_unparseable":
    "Sensor configuration unreadable",
  "alarm.journal_event.sensor_sabotage": "Sensor sabotage",
  "alarm.journal_event.sensor_unavailable_while_armed":
    "Sensor unavailable while armed",
  "alarm.journal_event.silence_persist_failed": "Silence could not be saved",
  "alarm.journal_event.silence_requested": "Silence requested",
  "alarm.journal_event.silenced": "Silenced",
  "alarm.journal_event.silenced_incident_restored":
    "Silenced incident restored",
  "alarm.journal_event.sounding_siren_adopted": "Sounding siren adopted",
  "alarm.journal_event.state_persist_failed": "State could not be saved",
  "alarm.journal_event.sysvar_arm_failed": "System-variable arming failed",
  "alarm.journal_event.sysvar_disarm_failed":
    "System-variable disarming failed",
  "alarm.journal_event.sysvar_disarm_refused":
    "System-variable disarming refused",
  "alarm.journal_event.sysvar_intent_ambiguous":
    "Ambiguous system-variable command",
  "alarm.journal_event.tamper_while_disarmed": "Tamper while disarmed",
  "alarm.journal_event.trigger_window_elapsed_while_down":
    "Alarm window elapsed while the daemon was down",
  "alarm.journal_event.triggered": "Triggered",
  "alarm.journal_event.triggered_restored": "Alarm restored",
  "alarm.journal_event.triggered_restored_implausible_clock":
    "Alarm restored with an implausible clock",
  "alarm.journal_event.unknown_persisted_state": "Unknown stored state",
  "alarm.journal_event.walktest_finished": "Walk test finished",
  "alarm.journal_event.walktest_sensor_seen": "Walk-test sensor seen",
  "alarm.journal_event.walktest_started": "Walk test started",
  "alarm.journal_event.zone_config_unparseable":
    "Zone configuration unreadable",
  "alarm.journal_event.zone_removed_while_armed":
    "Armed zone removed from the configuration",
  // Walk test (§12.4).
  "alarm.walktest.start": "Start test",
  "alarm.walktest.stop": "Stop test",
  "alarm.walktest.active": "Test running",
  "alarm.walktest.inactive": "No test running",
  "alarm.walktest.select_zone": "Select zone",
  "alarm.walktest.progress": "{seen}/{total} sensors verified",
  "alarm.walktest.tested": "verified",
  "alarm.walktest.untested": "pending",
  "alarm.walktest.empty": "No sensors in this zone.",
  // Setup wizard (§12.3).
  "alarm.wizard.launch": "Setup wizard",
  "alarm.wizard.step.zones": "Zones",
  "alarm.wizard.step.sensors": "Sensors",
  "alarm.wizard.step.outputs": "Outputs",
  "alarm.wizard.step.delays": "Delays & chirps",
  "alarm.wizard.step.codes": "Codes & users",
  "alarm.wizard.step.done": "Done",
  "alarm.wizard.next": "Next",
  "alarm.wizard.back": "Back",
  "alarm.wizard.skip": "Skip",
  "alarm.wizard.finish": "Finish",
  "alarm.wizard.codes_later":
    "PIN codes and remote keys are managed on the Codes tab once this zone exists — there is nothing to configure here yet.",
  // Health chip (§12.5, S7).
  "alarm.health.healthy": "Alarm system OK",
  "alarm.health.unhealthy": "Alarm system fault",
  // Per-tab intro lines rendered by the alarm section shell under the
  // tab bar — one orientation sentence per view.
  "alarm.intro.overview":
    "Arm and disarm each zone and handle a triggered alarm. Silence stops the sirens but keeps the incident open, disarm ends it, acknowledge only marks it as seen.",
  "alarm.intro.sensors":
    "Choose which sensors guard each zone and in which arm modes they count. The detail drawer tunes per-sensor behaviour such as entry delay and bypass; the matrix view is the fastest way to audit many sensors at once.",
  "alarm.intro.outputs":
    "Enroll sirens, lights, chirps and notification targets as alarm consequences and tune tone, duration and mode assignment per output. Every output can be test-fired briefly; the optical-only option spares the neighbours.",
  "alarm.intro.policies":
    "Per-zone rules beyond plain arm/disarm: when a code is required, which outputs hazard and panic triggers fire around the clock, how a pre-alarm softens escalation, and what happens after a trigger phase ends.",
  "alarm.intro.codes":
    "PIN codes, keypad slots and remote keys that can arm, disarm or silence the alarm — independent of login accounts, e.g. for household members without access to this UI.",
  "alarm.intro.journal":
    "The persistent log of everything the alarm engine does or observes — arming, triggers, bypasses, faults and tests. Filter by zone, event class and time range, or export the current view as CSV.",
  "alarm.intro.walktest":
    "Tests sensors without arming the zone: start a session, walk the house and trip each sensor — every activation turns its row green, and no alarm fires. The result is recorded in the journal.",
  "alarm.outputs.field.class": "Output class",
  "alarm.outputs.level": "Dimmer level (0–1)",
  "alarm.outputs.level.hint":
    "Dimmer level for actuator-backed outputs, 0–1. Empty keeps the device's last level.",
  "alarm.sensors.add.no_devices": "No matching device channels found.",
  "alarm.sensors.add.show_all": "Show all channels",
  "alarm.sensors.zone": "Zone",
  // Guard shown when switching the zone selector would drop an unsaved
  // editing session. The zone-scoped views save whatever is on screen back
  // under the zone the selector points at, so the buffer cannot travel with
  // the operator — it is either saved first or discarded.
  "alarm.zone_switch.discard.title": "Unsaved changes",
  "alarm.zone_switch.discard.body":
    "Your unsaved changes for this zone will be lost if you switch to another zone. Switch anyway?",
  "alarm.zone_switch.discard.confirm": "Discard and switch",
  "alarm.sensors.field.channel": "Channel address",
  "alarm.sensors.field.device": "Device",
  "alarm.sensors.field.name": "Name",
  "alarm.sensors.field.parameter": "Parameter",
  "alarm.sensors.select_all": "Select all filtered",
  "alarm.toast.walktest_start_failed": "Walk test could not be started",
  "alarm.toast.walktest_stop_failed": "Walk test could not be stopped",
  "alarm.wizard.zone.default_name": "Ground floor",
  "alarm.wizard.zone.hint":
    "A zone is an independently armable partition — for example one per floor.",
  "alarm.wizard.delay.entry": "Entry delay (s)",
  "alarm.wizard.delay.exit": "Exit delay (s)",
  "alarm.wizard.delay.trigger": "Alarm duration (s)",
  "alarm.wizard.delays.hint":
    "The exit delay lets you leave after arming; the entry delay gives you time to disarm after opening the door. Alarm duration bounds how long one alarm phase (and its sirens) runs — at most 600 s per cycle.",
  "alarm.wizard.finish.hint":
    "The zone is created disarmed. Run a walk test before relying on it.",
  "alarm.wizard.outputs.empty": "No eligible output channels found.",
  "alarm.wizard.outputs.empty.description":
    "Use the Outputs tab afterwards for expert-mode enrollment of any device.",
  "alarm.wizard.outputs.hint":
    "Pick the sirens, lights, and other outputs to enroll below — fine-tune tone, duration, and mode assignment afterwards in the outputs tab.",
  "alarm.wizard.sensors.empty": "No matching devices found.",
  "alarm.wizard.sensors.empty.description":
    "Try a different search, or enable the show-all toggle above to widen the candidate list beyond security-relevant devices.",
  "alarm.wizard.sensors.hint":
    "Pick the door, window, and motion sensors to enroll below — search by name or address, or show every device.",
  "alarm.wizard.sort.name": "Name",
  "alarm.wizard.sort.room": "Room",
  "alarm.wizard.sort.model": "Model",
  "alarm.wizard.summary.delay_line": "{mode} {exit}/{entry}/{trigger}s",
  "alarm.wizard.summary.delays": "Delays",
  // Alarm codes (notes/concepts/alarm-concept.md §11).
  "alarm.codes.add": "Add code",
  "alarm.codes.zones": "Zones",
  "alarm.codes.zones.all": "All zones",
  "alarm.codes.delete.confirm.body":
    'Delete the code "{name}"? This cannot be undone.',
  "alarm.codes.delete.confirm.title": "Delete code?",
  "alarm.codes.disabled": "Disabled",
  "alarm.codes.duress.badge": "Duress",
  "alarm.codes.duress.warning":
    "A duress code disarms the zone exactly like a normal code — nothing changes on the panel — but silently raises a duress event to the configured notification targets instead. Nothing appears in the visible journal until the incident is resolved; the full audit trail is kept internally. Never hand out a duress code casually.",
  "alarm.codes.edit": "Edit code",
  "alarm.codes.empty": "No codes yet",
  "alarm.codes.empty.description":
    "Add a PIN code, keypad slot, or remote key to let people arm, disarm, or silence this alarm system.",
  "alarm.codes.error.binding_json": "Binding must be valid JSON.",
  "alarm.codes.error.name_required": "Name is required.",
  "alarm.codes.error.pin_required": "A PIN is required for a new PIN code.",
  "alarm.codes.field.zones": "Zones",
  "alarm.codes.field.zones.help":
    "Select which zones this code applies to. Leave every box unchecked to apply it to all zones.",
  "alarm.codes.field.binding": "Hardware binding",
  "alarm.codes.field.binding.help":
    "Raw JSON describing the physical binding for this code kind — e.g. the keypad channel address or the remote-key press channel. Leave empty for no binding.",
  "alarm.codes.field.duress": "Duress code",
  "alarm.codes.field.enabled": "Enabled",
  "alarm.codes.field.kind": "Type",
  "alarm.codes.field.kind.hint":
    "A PIN is typed on the PIN pad or anonymous surfaces; keypad-slot and remote-key entries bind a hardware keypad user slot or a radio remote so its actions run under this name.",
  "alarm.codes.field.name": "Name",
  "alarm.codes.field.pin": "PIN",
  "alarm.codes.field.pin.help":
    "4–8 digit PIN, stored as a salted hash — the daemon never returns it again.",
  "alarm.codes.field.pin.keep": "Leave empty to keep the current PIN",
  "alarm.codes.field.pin.placeholder": "Enter PIN",
  "alarm.codes.field.valid_from": "Valid from",
  "alarm.codes.field.valid_until": "Valid until",
  "alarm.codes.field.validity.help":
    "Leave both empty for a code with no expiry.",
  "alarm.codes.kind.keypad_slot": "Keypad slot",
  "alarm.codes.kind.pin": "PIN",
  "alarm.codes.kind.remote_key": "Remote key",
  "alarm.codes.remote.key": "Remote button",
  "alarm.codes.remote.expert": "Raw JSON",
  "alarm.codes.remote.expert.hint":
    "Edit the binding document directly — needed for virtual remote channels or unusual setups.",
  "alarm.codes.remote.no_candidates":
    "No remote or wall-button keys found. Teach-in the remote first, or use raw JSON.",
  "alarm.codes.remote.alarm_keyfob": "Alarm keyfob",
  "alarm.codes.remote.candidates_failed": "Loading remote keys failed",
  "alarm.codes.remote.parameter": "Trigger",
  "alarm.codes.remote.parameter.hint":
    "Which press of the bound key fires the action — short or long.",
  "alarm.codes.remote.param.press_short": "Short press",
  "alarm.codes.remote.param.press_long": "Long press",
  "alarm.codes.remote.action": "Action",
  "alarm.codes.remote.action.hint":
    "What the key does: arm into a specific mode, disarm, silence, or panic.",
  "alarm.codes.remote.zone.hint": "Alarm zone the action applies to.",
  "alarm.codes.remote.action.arm": "Arm",
  "alarm.codes.remote.action.disarm": "Disarm",
  "alarm.codes.remote.action.silence": "Silence",
  "alarm.codes.remote.action.panic": "Panic",
  "alarm.codes.remote.zone": "Zone",
  "alarm.codes.error.remote_incomplete":
    "Pick a remote button, trigger, action, and zone.",
  "alarm.codes.perm.arm": "Arm",
  "alarm.codes.perm.disarm": "Disarm",
  "alarm.codes.perm.silence": "Silence",
  "alarm.codes.perms": "Permissions",
  "alarm.codes.perms.hint":
    "What this code is allowed to do: arm, disarm, and silence sirens.",
  "alarm.codes.unavailable": "Alarm codes unavailable",
  "alarm.codes.unavailable.description":
    "The alarm-code subsystem is not configured on this daemon.",
  "alarm.codes.validity.open": "No limit",
  // Chirp tone labels (notes/concepts/alarm-concept.md §15 row 23). The driver
  // reads three tone labels: arm squawk, disarm squawk, and the tick
  // tone (countdown ticks, entry warning, and the door chime).
  "alarm.outputs.chirp_arm_tone": "Arm chirp tone",
  "alarm.outputs.chirp_disarm_tone": "Disarm chirp tone",
  "alarm.outputs.chirp_tick_tone": "Tick & chime tone",
  "alarm.outputs.chirp_tick_tone.hint":
    "Used for countdown ticks, entry warnings and the door chime. An empty tone label skips that chirp kind on this output.",
  // PIN pad (notes/concepts/alarm-concept.md §12.1).
  "alarm.pinpad.arm_title": "Enter code to arm — {mode}",
  "alarm.pinpad.backspace": "Backspace",
  "alarm.pinpad.clear": "Clear",
  "alarm.pinpad.digit": "Digit {digit}",
  "alarm.pinpad.disarm_title": "Enter code to disarm {zone}",
  "alarm.pinpad.entered": "{count} digits entered",
  "alarm.pinpad.placeholder": "Enter code",
  "alarm.pinpad.title": "Enter code",
  // Policy editor (notes/concepts/alarm-concept.md §11, §15 rows 19/21/22).
  "alarm.policies.code.hint":
    "Operator sessions (REST, WebSocket, hmcli) always bypass these checks — the documented break-glass path — but a duress code they enter still fires a silent alarm.",
  "alarm.policies.code.require_arm": "Require code to arm",
  "alarm.policies.code.require_arm.hint":
    "Requires a valid code before the zone arms. Off by default — arming is the safe direction and stays one tap.",
  "alarm.policies.code.require_disarm": "Require code to disarm",
  "alarm.policies.code.require_disarm.always": "Always",
  "alarm.policies.code.require_disarm.default":
    "Automatic (on when codes exist)",
  "alarm.policies.code.require_disarm.hint":
    "Automatic requires a code as soon as this zone has an enabled code. A zone without codes never demands one, so a disarm can never lock you out.",
  "alarm.policies.code.require_disarm.never": "Never",
  "alarm.policies.code.require_silence": "Require code to silence",
  "alarm.policies.code.require_silence.hint":
    "Applies to anonymous input surfaces only — MQTT, keypad and remote key. Authenticated operator sessions always bypass this check.",
  "alarm.policies.code.source.keypad": "Keypad",
  "alarm.policies.code.source.mqtt": "MQTT",
  "alarm.policies.code.source.remote": "Remote key",
  "alarm.policies.output.exclude_outdoor": "Exclude outdoor outputs",
  "alarm.policies.output.exclude_outdoor.hint":
    "Skips outputs marked as outdoor (e.g. an outdoor siren); indoor outputs still fire.",
  "alarm.policies.output.silent": "Silent (no siren)",
  "alarm.policies.output.silent.hint":
    "Suppresses all acoustic outputs for this policy — notifications, optical signals and alarm lights still fire.",
  "alarm.policies.output.smoke_sounders": "Enroll smoke-detector sounders",
  "alarm.policies.output.smoke_sounders.hint":
    "Additionally sounds the enrolled smoke-detector sirens. Use deliberately: each activation costs irreplaceable detector battery and usually sounds the whole smoke-detector group.",
  "alarm.policies.posttrigger": "When the trigger phase ends",
  "alarm.policies.posttrigger.disarm": "Disarm",
  "alarm.policies.posttrigger.hint":
    "A trigger phase is always time-limited (default 180 s, at most 600 s per cycle); sirens stop when it ends no matter what. This setting decides what the zone does afterwards: stay armed in the previous mode, or disarm.",
  "alarm.policies.posttrigger.return_to_armed": "Return to armed",
  "alarm.policies.prealarm.empty":
    "No modes configured for this zone yet — add modes in the setup wizard first.",
  "alarm.policies.prealarm.hint":
    "Runs a quiet pre-alarm phase before the full trigger: only chirp, notification and light outputs fire for this many seconds, then the full output policy escalates. A silence during this phase cancels the escalation. 0 disables it.",
  "alarm.policies.rearm.hint":
    'Re-arms the zone to its pre-incident mode this many quiet seconds after a post-trigger disarm; only takes effect when "When the trigger phase ends" is set to Disarm. The countdown resets on any sensor activity.',
  "alarm.policies.rearm.seconds": "Auto re-arm after (s)",
  "alarm.policies.schedules.add": "Add schedule",
  "alarm.policies.schedules.auto_arm": "Auto-arm",
  "alarm.policies.schedules.auto_arm.hint":
    "When on, the zone arms automatically at this time. When off, this only raises a reminder.",
  "alarm.policies.schedules.days": "Days",
  "alarm.policies.schedules.empty": "No schedules yet",
  "alarm.policies.schedules.mode": "Mode",
  "alarm.policies.schedules.time": "Time",
  "alarm.policies.section.codes": "Codes",
  "alarm.policies.section.codes.hint":
    "Alarm codes are managed on the Codes tab and are independent of login accounts. These switches decide when a code must be entered; they only apply to anonymous surfaces such as MQTT, keypads and remote keys.",
  "alarm.policies.section.hazard": "Hazard outputs",
  "alarm.policies.section.hazard.hint":
    "Always-on output policy for hazard-class triggers (smoke, water, gas) — these sensors fire around the clock, independent of the armed mode.",
  "alarm.policies.section.panic": "Panic outputs",
  "alarm.policies.section.panic.hint":
    "Always-on output policy for panic-class triggers — independent of the armed mode. A sensor marked as silent panic suppresses acoustic outputs for its activations regardless of this policy.",
  "alarm.policies.section.prealarm": "Pre-alarm",
  "alarm.policies.section.rearm": "Post-trigger & auto re-arm",
  "alarm.policies.section.schedules": "Schedules",
  "alarm.policies.section.schedules.hint":
    "Time-of-day arm schedules for this zone, evaluated in the daemon's local time zone. With no day selected an entry fires every day. With auto-arm the zone actually arms; otherwise the entry only raises a reminder when the zone is not in the expected mode.",
  "audit.title": "Change history",
  "audit.empty": "No changes recorded yet.",
  "audit.empty.description":
    "Configuration changes are logged here with the operator, the affected setting and a timestamp.",
  "audit.entries": "{count} entries",
  "audit.filter.all": "All actions",
  "audit.from": "From",
  "audit.to": "To",
  "audit.export_csv": "Export CSV",
  "audit.prev": "Previous",
  "audit.next": "Next",
  "audit.page": "Page {page}",
  "audit.changes": "changes",
  "audit.col.parameter": "Parameter",
  "audit.col.before": "Before",
  "audit.col.after": "After",
  "audit.action.paramset_write": "Config",
  "audit.action.link_paramset_write": "Link config",
  "audit.action.link_add": "Link added",
  "audit.action.link_remove": "Link removed",
  "audit.action.schedule_write": "Schedule",
  "audit.action.active_profile": "Profile",
  "audit.action.data_point_write": "Value",
  "audit.action.addon_update_install": "Add-on update",
  "audit.action.alarm_acknowledge": "Alarm acknowledged",
  "audit.action.alarm_arm": "Alarm armed",
  "audit.action.alarm_code_change": "Alarm code changed",
  "audit.action.alarm_config_change": "Alarm configuration",
  "audit.action.alarm_disarm": "Alarm disarmed",
  "audit.action.alarm_motion_reset": "Motion detectors reset",
  "audit.action.alarm_output_test": "Alarm output test",
  "audit.action.alarm_silence": "Alarm silenced",
  "audit.action.alarm_walk_test": "Alarm walk test",
  "audit.action.area_change": "Area changed",
  "audit.action.backup_pre_update": "Pre-update backup",
  "audit.action.backup_upload": "Backup imported",
  "audit.action.backup_delete": "Backup deleted",
  "audit.action.central_create": "CCU added",
  "audit.action.central_delete": "CCU removed",
  "audit.action.central_update": "CCU changed",
  "audit.action.channel_flags": "Channel flags",
  "audit.action.config_section_delete": "Config section deleted",
  "audit.action.config_section_update": "Config section saved",
  "audit.action.device_assignment": "Device assignment",
  "audit.action.device_communication_test": "Communication test",
  "audit.action.device_config_restore": "Device config restored",
  "audit.action.device_install_mode": "Install mode",
  "audit.action.device_replace": "Device replaced",
  "audit.action.device_search": "Device search",
  "audit.action.device_team_set": "Device team",
  "audit.action.diagram_config": "Diagram configuration",
  "audit.action.group_admin": "Group administration",
  "audit.action.incidents_clear": "Incidents cleared",
  "audit.action.install_mode": "Install mode",
  "audit.action.install_mode_local": "Local teach-in",
  "audit.action.link_activate": "Link activated",
  "audit.action.link_update": "Link updated",
  "audit.action.matter_commissioning": "Matter commissioning",
  "audit.action.matter_exposure_bulk": "Matter exposure (bulk)",
  "audit.action.matter_exposure_update": "Matter exposure",
  "audit.action.matter_fabric_revoke": "Matter fabric revoked",
  "audit.action.matter_factory_reset": "Matter pairings removed",
  "audit.action.matter_force_sync": "Matter topology re-synced",
  "audit.action.matter_share": "Matter share",
  "audit.action.program_delete": "Program deleted",
  "audit.action.program_execute": "Program executed",
  "audit.action.recording_toggle": "Recording toggled",
  "audit.action.room_function": "Room / function",
  "audit.action.system_ccu_position": "CCU position",
  "audit.action.system_ccu_poweroff": "CCU power off",
  "audit.action.system_ccu_reboot": "CCU reboot",
  "audit.action.system_ccu_recovery_mode": "CCU recovery mode",
  "audit.action.system_ccu_safe_mode": "CCU safe mode",
  "audit.action.system_firmware_download": "Firmware download",
  "audit.action.tls_cert_upload": "TLS certificate",
  "audit.action.token_create": "Token created",
  "audit.action.token_revoke": "Token revoked",
  "audit.action.logging.override_set": "Log level override set",
  "audit.action.logging.override_reset": "Log level override reset",
  "audit.action.logging.default_level_set": "Default log level changed",
  "audit.action.diagnostics.capture_start": "Diagnostic capture started",
  "audit.action.diagnostics.capture_stop": "Diagnostic capture stopped",
  "audit.action.system.restart_requested": "Daemon restart requested",
  "audit.action.cache_clear": "Cache cleared",
  "audit.action.un_ignore_update": "Update un-ignored",
  "audit.action.user_create": "User created",
  "audit.action.user_delete": "User deleted",
  "audit.action.user_update": "User changed",
  "backup.title": "Backups",
  "backup.subtitle": "CCU backups stored on the daemon host.",
  "backup.empty": "No backups yet.",
  "backup.upload": "Import…",
  "backup.uploading": "Importing…",
  "backup.upload.help":
    "Take in a .sbk archive from elsewhere so it can be restored like a local backup. The archive is checked before it is stored.",
  "backup.uploaded": "Backup {id} imported.",
  "backup.uploaded_with_version":
    "Backup {id} imported (from firmware {version}).",
  "backup.trigger": "Trigger backup",
  "backup.trigger_central": "Target CCU",
  "backup.triggering": "Starting…",
  "backup.confirm.title": "Restore backup?",
  "backup.confirm.body":
    "The CCU will lose its current state and be overwritten with this backup's contents. This action cannot be undone.",
  "backup.col.created": "Created",
  "backup.col.central": "CCU",
  "backup.col.size": "Size",
  "backup.col.id": "ID",
  "backup.col.action": "Action",
  "backup.download": "Download",
  "backup.started": "Backup started (id {id}).",
  "backup.storage.label": "Storage location",
  "backup.storage.unknown": "not reported",
  "backup.storage.unavailable":
    "No storage directory — the daemon could not create it. Backups cannot be kept.",
  "backup.storage.summary": "{count} archives · {bytes}",
  "backup.delete": "Delete",
  "backup.deleting": "Deleting…",
  "backup.delete_confirm.title": "Delete backup?",
  "backup.delete_confirm.body":
    "{name} is removed from the daemon's storage for good. If this is the only copy of that CCU's configuration, there is no way back.",
  "backup.deleted": "Backup {id} deleted.",
  "backup.delete_failed": "Deleting {id} failed: {error}",
  "backup.restore_started": "Restore of {id} triggered.",
  "common.acknowledge": "Acknowledge",
  "common.add": "Add",
  "common.cancel": "Cancel",
  "common.close": "Close",
  "common.copy": "Copy",
  "common.delete": "Delete",
  "common.edit": "Edit",
  "common.enable": "Enable",
  "common.disable": "Disable",
  "common.error": "Error:",
  "common.refresh": "Refresh",
  "common.loading": "Loading…",
  "common.modified": "modified",
  "common.new": "+ New",
  "common.no": "No",
  "common.none": "none",
  "common.paste": "Paste",
  "common.reload": "Reload",
  "common.remove": "Remove",
  "loglevels.title": "Log-level overrides",
  "loglevels.subtitle":
    "Raise or lower logging for individual subsystems. Overrides resolve hierarchically (e.g. openccu-loom.client).",
  "loglevels.default": "default: {level}",
  "loglevels.empty":
    "No overrides — every subsystem follows the default level.",
  "loglevels.permanent": "permanent",
  "loglevels.expires_in_min": "expires in {mins} min",
  "loglevels.expires_soon": "expires shortly",
  "loglevels.path_label": "Logger path",
  "loglevels.level_label": "Level",
  "loglevels.ttl_label": "TTL (min)",
  "loglevels.ttl_permanent": "permanent",
  "loglevels.add": "Add override",
  "loglevels.added": "Override set for {path}.",
  "loglevels.removed": "Override removed for {path}.",
  "loglevels.admin_only": "Only administrators can change log-level overrides.",
  "account.password.title": "Change password",
  "account.password.subtitle": "Update the password for your account ({user}).",
  "account.password.current": "Current password",
  "account.password.new": "New password",
  "account.password.confirm": "Confirm new password",
  "account.password.submit": "Change password",
  "account.password.changed": "Password changed.",
  "account.password.mismatch": "Passwords do not match.",
  "account.password.too_short": "Use at least {min} characters.",
  "tls.title": "TLS certificate",
  "tls.subtitle":
    "Upload a PEM certificate and key. The listener (API + SPA) hot-reloads — no restart needed.",
  "tls.cert_label": "Certificate (PEM)",
  "tls.key_label": "Private key (PEM)",
  "tls.upload": "Upload & reload",
  "tls.uploaded": "Certificate replaced and reloaded.",
  "tls.not_enabled":
    "TLS is not enabled. Set north.rest.tls_cert_file / tls_key_file first.",
  "common.reset": "Reset",
  "common.download": "Download",
  "common.restore": "Restore",
  "common.save": "Save",
  "common.saving": "Saving…",
  "common.search": "Search…",
  "common.yes": "Yes",
  "devices.empty": "No devices found.",
  "devices.loading": "Loading devices…",
  "devices.title": "Devices",
  "devices.initializing": "Devices are still loading from CCU '{name}'…",
  "devices.initializing_banner":
    "CCU '{name}' is still initializing (devices {loaded}/{total}) — its devices appear automatically.",
  "central.readiness.ready": "Ready",
  "central.readiness.waiting": "Waiting for CCU",
  "central.readiness.loading_hub": "Initializing (names)",
  "central.readiness.loading_devices":
    "Initializing (devices {loaded}/{total})",
  "central.readiness.offline": "Offline",
  "central.readiness.unknown": "Unknown",
  "matter.readiness.waiting":
    "Waiting for CCU initialization — pairing becomes available once at least one CCU is ready.",
  "matter.readiness.partial":
    "CCU '{name}' is still initializing — its devices appear in the pairing automatically once loaded.",
  "firmware.title": "Firmware",
  "firmware.subtitle": "Device firmware versions and OTA update status.",
  "firmware.updates_available":
    "{count} device(s) have a firmware update available.",
  "firmware.no_updates": "No devices with firmware updates available.",
  "firmware.filter.all": "All devices",
  "firmware.filter.updatable": "Updates available",
  "firmware.col.device": "Device",
  "firmware.col.model": "Model",
  "firmware.col.current": "Installed",
  "firmware.col.available": "Available",
  "firmware.col.state": "Status",
  "firmware.col.action": "Action",
  "firmware.update": "Update",
  "firmware.triggering": "Triggering…",
  "firmware.in_progress": "In progress…",
  "firmware.up_to_date": "Up to date",
  "firmware.awaiting_transfer": "Awaiting transfer to the device",
  "firmware.triggered": "Firmware update triggered for {name}.",
  "firmware.confirm_update":
    'Trigger firmware update for "{name}"? The device will be briefly unreachable.',
  "firmware.duty_cycle_warning":
    "The radio interface duty cycle is high ({value}%). The over-the-air transfer may stall until the radio recovers.",
  "firmware.count": "{count} of {total} devices",
  "firmware.state.UNKNOWN": "Unknown",
  "firmware.state.UP_TO_DATE": "Up to date",
  "firmware.state.LIVE_UP_TO_DATE": "Up to date",
  "firmware.state.NEW_FIRMWARE_AVAILABLE": "Update available",
  "firmware.state.LIVE_NEW_FIRMWARE_AVAILABLE": "Update available",
  "firmware.state.DELIVER_FIRMWARE_IMAGE": "Delivering…",
  "firmware.state.LIVE_DELIVER_FIRMWARE_IMAGE": "Delivering…",
  "firmware.state.READY_FOR_UPDATE": "Ready",
  "firmware.state.DO_UPDATE_PENDING": "Pending…",
  "firmware.state.PERFORMING_UPDATE": "Updating…",
  "firmware.state.BACKGROUND_UPDATE_NOT_SUPPORTED": "Not supported",
  "diagnostics.title": "Diagnostics",
  "diagnostics.subtitle": "Health, interfaces and incidents",
  "diagnostics.health": "Health",
  "diagnostics.interfaces": "Interfaces",
  "diagnostics.rssi.device": "Device",
  "diagnostics.rssi.reachable": "Reachable",
  "diagnostics.rssi.device_dbm": "Device (dBm)",
  "diagnostics.rssi.peer_dbm": "Peer (dBm)",
  "diagnostics.rssi.battery": "Battery",
  "nav.signal": "Radio & battery",
  "page.title.signal": "Radio & battery — OpenCCU-Loom",
  "signal.title": "Radio & battery",
  "signal.count": "{count} devices",
  "signal.hint":
    "Per-device RF reception strength and battery, read from each device's maintenance channel. Works for HmIP and BidCos.",
  "signal.empty": "No devices report RSSI.",
  "signal.empty.description":
    "Radio and battery readings appear here once a device has communicated with its CCU.",
  "signal.low_battery": "Low",
  "diagnostics.incidents": "Incidents",
  "diagnostics.empty.components": "No components reported.",
  "diagnostics.empty.interfaces": "No interfaces configured.",
  "diagnostics.empty.incidents": "No incidents.",
  "diagnostics.connected": "connected",
  "diagnostics.disconnected": "disconnected",
  "diagnostics.reconnect": "Reconnect",
  "diagnostics.reconnect_done": "{id}: reconnect triggered.",
  "diagnostics.health_score": "Health score (0–100)",
  "diagnostics.download_dump": "Download diagnostics",
  "diagnostics.logging": "Logging",
  "diagnostics.log_default": "Default",
  "diagnostics.no_overrides": "No level overrides active.",
  "diagnostics.log_path": "Logger path",
  "diagnostics.log_level": "Level",
  "diagnostics.ttl_seconds": "TTL (s)",
  "diagnostics.apply": "Apply",
  "diagnostics.permanent": "permanent",
  "diagnostics.unavailable": "Not available",
  "diagnostics.log_level_applied": "Log level applied.",
  "diagnostics.capture": "Capture",
  // Debug-capture lifecycle, mirroring the Status constants the daemon
  // writes into a capture record. The unified Recordings table shows these
  // in the same column as the RPC-recording states, so they have to be
  // localized the same way.
  "diagnostics.capture_status.running": "Running",
  "diagnostics.capture_status.stopped": "Stopped",
  "diagnostics.capture_status.expired": "Expired",
  "diagnostics.capture_status.aborted": "Aborted",
  // Incident severity as recorded by the reliability/incident pipeline.
  "diagnostics.incident_severity.info": "Info",
  "diagnostics.incident_severity.warning": "Warning",
  "diagnostics.incident_severity.error": "Error",
  "diagnostics.incident_severity.critical": "Critical",
  "diagnostics.duration_seconds": "Duration (s)",
  "diagnostics.anonymise": "Anonymise",
  "diagnostics.stop": "Stop",
  "diagnostics.client_health": "Client health",
  "diagnostics.primary": "Primary",
  "diagnostics.healthy": "healthy",
  "diagnostics.unhealthy": "unhealthy",
  "diagnostics.in_recovery": "in recovery",
  // Health snapshot status enum + component note keys — mirror
  // `HealthComponent.Status` / `.NoteKey` from `GET /api/v1/health`.
  "health.status.healthy": "Healthy",
  "health.status.degraded": "Degraded",
  "health.status.unhealthy": "Unhealthy",
  "health.status.unknown": "Unknown",
  "health.note.initial_sync_connected": "Initial sync: connected",
  "health.note.initial_sync_not_connected": "Initial sync: not connected",
  "health.note.client_connected": "Client connected",
  "health.note.breaker_closed": "Breaker closed",
  "health.note.breaker_half_open": "Breaker half-open",
  "health.note.breaker_open": "Breaker open",
  "health.note.breaker_open_escalated": "Breaker open (escalated)",
  "health.note.recovery_started": "Recovery started",
  "health.note.recovery_completed": "Recovery completed",
  "health.note.recovery_failed_escalated": "Recovery failed (escalated)",
  "diagnostics.last_ok": "Last OK",
  "diagnostics.last_fail": "Last failure",
  "diagnostics.last_event": "Last event",
  "diagnostics.consecutive_failures": "Consec. failures",
  "diagnostics.reconnect_attempts": "Reconnect attempts",
  "diagnostics.central": "Central",
  "diagnostics.system_gauges": "System gauges",
  "diagnostics.rpc_recording.active": "Active",
  "diagnostics.rpc_recording.inactive": "Inactive",
  "diagnostics.rpc_recording.running_hint":
    "Recording active · survives restart",
  "diagnostics.rpc_recording.stop": "Stop",
  "diagnostics.rpc_recording.started": "RPC recording started.",
  "diagnostics.rpc_recording.stopped": "RPC recording stopped.",
  "inbox.title": "Inbox",
  "inbox.subtitle":
    "Devices the CCU saw during pairing but that haven't been accepted yet.",
  "inbox.empty":
    "Inbox empty. Enable pairing mode on the device list to see new candidates.",
  "inbox.accept": "Accept",
  "inbox.accepted": "{name} accepted.",
  "inbox.pending_creation_badge": "Awaiting approval",
  "inbox.pending_creation_hint":
    "Deferred device creation is enabled: this device exists on the CCU but has no data points here until you accept it.",
  "inbox.accept_dialog.title": "Accept device",
  "inbox.accept_dialog.subtitle":
    "Optionally configure {address} before it joins the registry. Leave everything blank to just accept.",
  "inbox.accept_dialog.name_label": "Name",
  "inbox.accept_dialog.name_placeholder": "Device name (optional)",
  "inbox.accept_dialog.include_channels": "Also rename channels",
  "inbox.accept_dialog.rooms_label": "Rooms",
  "inbox.accept_dialog.functions_label": "Functions",
  "inbox.accept_dialog.group_label": "Heating group",
  "inbox.accept_dialog.group_none": "— none —",
  "inbox.accept_dialog.group_hint":
    "Optionally add this device to a heating group after accepting.",
  "inbox.group_assign.done": "Added to group “{group}”.",
  "inbox.group_assign.no_channel":
    "The device has no channel assignable to this group — add it manually later.",
  "inbox.group_assign.failed": "Group assignment failed.",
  "inbox.accept_dialog.submit": "Accept",
  "inbox.accept_dialog.catalog_error": "Could not load rooms and functions.",
  "messages.title": "Messages",
  "messages.alarms": "Alarms",
  "messages.service": "Service messages",
  "messages.empty.alarms": "No alarms.",
  "messages.empty.alarms.description":
    "Alarms appear here as soon as a device reports a fault.",
  "messages.empty.service": "No service messages.",
  "messages.empty.service.description":
    "Service messages appear here, e.g. for low batteries or tampering.",
  "messages.quittable_only": "Quittable only",
  "messages.all_types": "All types",
  "messages.acknowledged": "Acknowledged.",
  "messages.summary": "{alarms} alarms · {services} service messages",
  "messages.ackable": "ackable",
  "messages.ack_all.button": "Acknowledge all",
  "messages.ack_all.confirm_alarms": "Acknowledge all alarm messages?",
  "messages.ack_all.confirm_services":
    "Acknowledge all quittable service messages?",
  "messages.ack_all.done": "{count} messages acknowledged.",
  "messages.type.generic": "Generic",
  "messages.type.sticky": "Sticky",
  "messages.type.config_pending": "Config pending",
  "messages.type.alarm": "Alarm",
  "messages.type.update_pending": "Update pending",
  "messages.type.communication": "Communication",
  "messages.suppress": "Hide permanently",
  "messages.suppress.confirm":
    "Permanently suppress this service message on the CCU? The device will stop raising it until you clear the suppression.",
  "messages.suppress.button": "Hide",
  "messages.suppressed": "Suppressed.",
  "messages.suppressed.tab": "Suppressed",
  "messages.suppressed.empty": "No suppressed service messages.",
  "messages.suppressed.empty.description":
    "Service messages you hide permanently appear here so you can bring them back.",
  "messages.suppressed.col.parameter": "Parameter",
  "messages.suppressed.col.channel": "Channel",
  "messages.suppressed.all_parameters": "All parameters",
  "messages.unsuppress.button": "Restore",
  "messages.unsuppress.confirm":
    "Clear the suppression and let this service message be raised again?",
  "messages.unsuppressed": "Suppression cleared.",
  "nav.alarm": "Alarm system",
  "nav.security": "Security & Safety",
  "nav.audit": "History",
  "nav.backups": "Backups",
  "nav.devices": "Devices",
  "nav.overview": "Overview",
  "nav.diagnostics": "Diagnostics",
  "nav.energy": "Energy",
  "nav.diagrams": "Diagrams",
  "nav.favorites": "Favorites",
  "nav.firmware": "Firmware",
  "nav.inbox": "Inbox",
  "nav.fleet": "Fleet",
  "nav.groups": "Groups",
  "nav.links": "Direct links",
  "nav.schedules": "Schedules",
  "favorites.title": "Favorites",
  "favorites.subtitle":
    "Your pinned devices and system variables, synced across browsers.",
  "favorites.empty": "No favorites yet. Pin a device from its detail page.",
  "favorites.pin": "Pin",
  "favorites.pinned": "Pinned",
  "favorites.unpin": "Unpin",
  "favorites.added": "{label} pinned to favorites.",
  "favorites.removed": "{label} removed from favorites.",
  "favorites.kind.channel": "Channel",
  "favorites.kind.program": "Program",
  "favorites.program_started": "{label} started.",
  "favorites.pin_channel": "Pin channel to favorites",
  "favorites.unpin_channel": "Remove channel from favorites",
  "favorites.pin_program": "Pin program to favorites",
  "favorites.unpin_program": "Remove program from favorites",
  "favorites.kind.device": "Device",
  "favorites.kind.sysvar": "System variable",
  "nav.logout": "Logout",
  "nav.messages": "Messages",
  "nav.programs": "Programs",
  "nav.settings": "Settings",
  "nav.sysvars": "Variables",
  "nav.about": "About",
  // --- About (#/about) ---
  "about.title": "About",
  "about.subtitle":
    "Version, build and runtime details of this OpenCCU-Loom daemon.",
  "about.load_error": "Loading failed: {error}",
  "about.section.daemon": "Daemon",
  "about.field.version": "Version",
  "about.field.commit": "Commit",
  "about.field.build_date": "Build date",
  "about.field.runtime": "Build variant",
  "about.runtime.addon": "CCU / RaspberryMatic add-on",
  "about.runtime.standalone": "Standalone (binary, Docker, or HA add-on)",
  "about.field.started_at": "Started",
  "about.field.uptime": "Uptime",
  "about.field.api_version": "API version",
  "about.field.capabilities": "Capabilities",
  "about.section.centrals": "Centrals",
  "about.centrals.empty": "No centrals configured.",
  "about.centrals.firmware": "CCU firmware",
  "about.centrals.update_available": "Update {version} available",
  "about.section.license": "License & links",
  "about.license.text":
    "OpenCCU-Loom is MIT-licensed open source. The binary embeds CCU metadata extracts covered by the eQ-3 HomeMatic Software License (non-commercial).",
  "about.links.github": "GitHub",
  "about.links.releases": "Releases & changelog",
  "about.links.notices": "Third-party notices",
  "about.links.docs": "User guide",
  // Guard shown when in-app navigation would discard an editor's
  // unsaved edits.
  "nav.leave_title": "Unsaved changes",
  "nav.leave_body":
    "You have unsaved changes that will be lost if you leave this view. Leave anyway?",
  "nav.leave_confirm": "Leave",
  // --- Overview (fleet-wide tile dashboard, roadmap B8) ---
  "overview.title": "Overview",
  "overview.subtitle": "All devices across your fleet, grouped and filterable.",
  "overview.group_by": "Group by:",
  "overview.group_mode.room": "Room",
  "overview.group_mode.function": "Function",
  "overview.group_mode.central": "CCU",
  "overview.filter.all_functions": "All functions",
  "overview.filter.central_title": "CCU",
  "overview.filter.room_title": "Room",
  "overview.filter.function_title": "Function",
  "overview.filter.area_title": "Area",
  "overview.search_placeholder": "Search devices…",
  "overview.empty": "No devices yet.",
  "overview.empty_filtered": "No devices match the current filters.",
  "overview.load_error": "Could not load devices: {error}",
  "overview.group.count": "{count} devices",
  "overview.group.loading": "Loading tiles…",
  "overview.group.error": "Could not load tiles: {error}",
  "overview.group.empty": "No controllable channels on these devices.",
  "overview.unassigned_room": "Unassigned",
  "overview.unassigned_function": "Unassigned",
  "overview.unassigned_central": "Unknown CCU",
  "overview.expand": "Expand group",
  "overview.collapse": "Collapse group",
  "unignore.subtitle":
    "Hidden parameters promoted to first-class data points. Use at your own risk.",
  "unignore.warning":
    "Excessive writes to MASTER paramset values can damage devices.",
  "unignore.central_label": "Central:",
  "unignore.search_placeholder": "Filter by name…",
  "unignore.add_pattern": "Add pattern",
  "unignore.add_pattern_placeholder":
    "PARAMETER or PARAMETER:PARAMSET@MODEL:CHANNEL",
  "unignore.save": "Save",
  "unignore.discard": "Discard",
  "unignore.no_centrals": "No centrals registered.",
  "unignore.no_candidates": "No hidden parameters available.",
  "unignore.no_match": "no match",
  "unignore.parse_errors_title": "Some patterns could not be applied:",
  "unignore.saved": "Un-ignore list updated ({count} patterns).",
  "unignore.saved_with_errors": "Saved with {count} parse error(s).",
  "unignore.save_failed": "Save failed: {err}",
  "unignore.stats":
    "{total} hidden parameters · {active} enabled · {pending} changed",
  "unignore.pending_changes": "Unsaved changes: {count}",
  "unignore.filter.categories": "Category",
  "unignore.filter.paramset": "Paramset",
  "unignore.filter.only_enabled": "Only enabled",
  "unignore.filter.hidden_notice":
    "Hidden by the category filter: {count}.",
  "unignore.filter.show_all": "Show all",
  "unignore.filter.reset": "Reset filter",
  "unignore.no_filter_match": "No parameter matches the filter.",
  "unignore.no_filter_match_hint":
    "Widen the search or re-enable a category chip.",
  "unignore.toggle_parameter": "Enable or disable {parameter}",
  "unignore.toggle_scopes": "Show device models for {parameter}",
  "unignore.remove_pattern": "Remove pattern {pattern}",
  "unignore.orphans_title": "Patterns without a matching parameter",
  "unignore.orphans_hint":
    "Saved earlier or typed by hand — no device in the fleet currently carries them.",
  "unignore.scope.all_devices": "All devices",
  "unignore.scope.all_channels": "all channels",
  "unignore.scope.partial": "Scopes: {count}",
  "unignore.scope.models": "Models: {count}",
  "unignore.scope.channel": "Channel {channel}",
  "unignore.scope.device_count": "Devices: {count}",
  "unignore.reason.operation_mode": "Channel mode",
  "unignore.reason.master_gate": "MASTER setting",
  "unignore.reason.week_profile": "Week profile",
  "unignore.reason.device_specific": "Device-specific",
  "unignore.reason.hidden": "Used internally",
  "unignore.reason.ignore_list": "Excluded",
  "unignore.reason.wildcard_prefix": "Name prefix",
  "unignore.reason.wildcard_suffix": "Name suffix",
  // Badge text once the server names the pattern that actually matched.
  "unignore.reason_detail.wildcard_prefix": "Prefix {pattern}",
  "unignore.reason_detail.wildcard_suffix": "Suffix {pattern}",
  "unignore.reason.channel_restricted": "Other channel",
  "unignore.reason.event_suppressed": "Events suppressed",
  "unignore.reason.internal_flag": "Internal",
  "unignore.reason.read_only": "Diagnostic bit",
  "unignore.reason.unknown": "Unknown",
  "unignore.reason_help.operation_mode":
    "The channel's operation mode excludes this parameter. Changing the mode surfaces it without an un-ignore entry.",
  "unignore.reason_help.master_gate":
    "A MASTER configuration value outside the whitelist for this model and channel.",
  "unignore.reason_help.week_profile":
    "One cell of a week profile (P1_ENDTIME_MONDAY_1, 01_WP_LEVEL). A single thermostat has hundreds of them; edit the profile in the schedule editor instead.",
  "unignore.reason_help.device_specific":
    "Suppressed for this device model specifically.",
  "unignore.reason_help.hidden":
    "The data point exists and is consumed elsewhere (maintenance channel, combined data point) but is not shown on its own.",
  "unignore.reason_help.ignore_list":
    "On the built-in list of parameters that never become data points.",
  "unignore.reason_help.wildcard_prefix":
    "Matches a suppressed name prefix (ADJUSTING_, ERR_TTM_, HANDLE_, IDENTIFY_, PARTY_START_, PARTY_STOP_, STATUS_FLAG_).",
  "unignore.reason_help.wildcard_suffix":
    "Matches a suppressed name suffix (_OVERFLOW, _OVERRUN, _REPORTING, _RESULT, _STATUS, _SUBMIT).",
  "unignore.reason_help.channel_restricted":
    "Accepted only on a different channel of this device.",
  "unignore.reason_help.event_suppressed":
    "Events for this parameter are filtered on this device model.",
  "unignore.reason_help.internal_flag":
    "The CCU marks the parameter as INTERNAL — a service value, not an operating one.",
  "unignore.reason_help.read_only":
    "Neither writable nor event-capable: the CCU never pushes it, so it only updates when polled.",
  "unignore.reason_help.unknown":
    "No known rule explains this suppression. Please report it.",
  "programs.title": "Programs",
  "programs.empty": "No programs.",
  "programs.run": "Execute",
  "programs.running": "Running…",
  "settings.title": "Settings",
  "settings.subtitle": "Daemon configuration and UI preferences",
  "settings.expert_mode": "Expert mode",
  "settings.expert_mode_hint":
    "Reveal deep-tuning fields (reliability, callback ports, Matter internals). Off by default.",
  "settings.live_edit_disabled":
    "Live edit is disabled — the data directory is read-only. Pflege Settings via config.yaml + restart.",
  "settings.restart_required":
    "This change applies after the next daemon restart.",
  "settings.save": "Save",
  "settings.save_and_restart": "Save and restart",
  "settings.saved": "Saved.",
  "settings.save_failed": "Save failed: {err}",
  "settings.reset": "Reset to default",
  "settings.section_unset": "Currently using built-in defaults.",
  "settings.values_admin_only":
    "Only administrators can see and change the current configuration values. The settings are listed here without them.",
  "config.source.bootstrap": "From the bootstrap config file",
  "config.source.db": "Saved via the UI",
  "config.source.env": "Overridden by environment variable",
  "config.source.default": "Default value",
  "config.source.short.bootstrap": "yaml",
  "config.source.short.db": "live",
  "config.source.short.env": "env",
  "config.source.short.default": "default",
  "config.field.locale": "UI language",
  "config.field.data_dir": "Data directory",
  "config.field.bootstrap.allow_first_run_setup": "Allow first-run setup",
  "config.field.logging.level": "Log level",
  "config.field.logging.format": "Log format",
  "config.field.logging.overrides": "Per-subsystem level overrides",
  "config.field.north.mqtt.enabled": "Enable MQTT bridge",
  "config.field.north.mqtt.broker_url": "Broker URL",
  "config.field.north.mqtt.client_id": "MQTT client ID",
  "config.field.north.mqtt.username": "Broker username",
  "config.field.north.mqtt.password": "Broker password",
  "config.field.north.mqtt.topic_base": "Topic prefix",
  "config.field.north.mqtt.raw_enabled": "Publish raw plane",
  "config.field.north.mqtt.discovery_enabled": "Publish HA discovery",
  "config.field.north.mqtt.protocol_version": "MQTT protocol version",
  "config.field.north.mqtt.payload_format": "Payload format",
  "config.field.north.mqtt.sub_devices_enabled":
    "One HA device per channel group",
  "config.field.north.matter.enabled": "Enable Matter bridge",
  "config.field.north.matter.enable_time_sync":
    "Mount TimeSynchronization cluster",
  "config.field.north.matter.listen": "UDP listen address",
  "config.field.north.matter.vendor_id": "Vendor ID",
  "config.field.north.matter.product_id": "Product ID",
  "config.field.north.matter.node_label": "Bridge label",
  "config.field.north.matter.discriminator": "Commissioning discriminator",
  "config.field.north.matter.prefer_ipv4": "Force IPv4",
  "config.field.north.matter.expose_secondary_channels":
    "Expose secondary channels",
  "config.field.north.matter.mdns_advertise": "mDNS advertiser",
  "config.field.north.matter.dev_rotate_unique_ids":
    "Rotate unique IDs each boot (dev)",
  "config.field.north.matter.commissioning.passcode": "Passcode",
  "config.field.north.matter.commissioning.salt": "PBKDF2 salt",
  "config.field.north.matter.commissioning.iterations": "PBKDF2 iterations",
  "config.field.north.matter.commissioning.concurrent_pairings":
    "Concurrent pairings",
  "config.field.north.matter.commissioning.ephemeral_window":
    "Ephemeral commissioning window",
  "config.field.north.matter.case.node_id": "Node ID",
  "config.field.north.matter.case.fabric_id": "Fabric ID",
  "config.field.north.matter.attestation.dac_path": "DAC certificate path",
  "config.field.north.matter.attestation.dac_key_path": "DAC private key path",
  "config.field.north.matter.attestation.pai_path": "PAI certificate path",
  "config.field.north.matter.attestation.cd_path":
    "Certification declaration (CD) path",
  "config.field.north.discovery.mdns.enabled": "Advertise via mDNS",
  "config.field.north.discovery.mdns.instance_name": "mDNS instance name",
  "config.field.north.discovery.ssdp.enabled": "Discover CCUs via SSDP",
  "config.field.north.discovery.ssdp.interval": "CCU discovery interval",
  "config.field.north.rest.enabled": "REST API enabled",
  "config.field.north.rest.listen": "REST listen address",
  "config.field.north.rest.public_url": "Public URL",
  "config.field.north.rest.tls_cert_file": "TLS certificate file",
  "config.field.north.rest.tls_key_file": "TLS private-key file",
  "config.field.north.rest.cors": "Allowed CORS origins",
  "config.field.north.rest.auth.basic_enabled": "HTTP Basic auth",
  "config.field.north.rest.auth.bearer_enabled": "Bearer-token auth",
  "config.field.north.rest.auth.oidc.enabled": "OIDC enabled",
  "config.field.north.rest.auth.oidc.issuer": "OIDC issuer URL",
  "config.field.north.rest.auth.oidc.client_id": "OIDC client ID",
  "config.field.north.rest.auth.oidc.redirect_url": "OIDC redirect URL",
  "config.field.north.rest.rate_limit.enabled": "REST rate-limit",
  "config.field.north.rest.rate_limit.requests_per_second":
    "Refill rate (req/s)",
  "config.field.north.rest.rate_limit.burst": "Burst capacity",
  "config.field.north.ui.enabled": "Bootstrap UI enabled",
  "config.field.north.ui.embedded": "Embedded in Home Assistant",
  "config.field.north.ui.embedded_scope": "Scope of the embedded mode",
  "config.field.north.ui.profiles": "Navigation profiles",
  "config.field.callback.host": "Callback bind address",
  "config.field.callback.port": "XML-RPC callback port",
  "config.field.callback.bin_port": "BIN-RPC callback port",
  "config.field.callback.port_range": "Ephemeral port range",
  "config.field.callback.public_host": "Public hostname (NAT)",
  "config.field.callback.max_connections": "Max callback connections",
  "config.field.callback.restrict_source_ips": "Restrict callbacks to CCU IPs",
  "config.field.ccu_data.translations_path": "Translations archive path",
  "config.field.ccu_data.easymode_path": "Easymode archive path",
  "config.field.north.rest.auth.users": "Bootstrap users",
  "config.field.north.rest.auth.tokens": "Bootstrap API tokens",
  "config.field.north.rest.auth.oidc.client_secret": "OIDC client secret",
  "config.field.north.rest.auth.oidc.role_claim": "OIDC role claim",
  "config.field.north.rest.auth.ccu.enabled": "CCU login enabled",
  "config.field.north.rest.auth.ccu.primary": "CCU is primary",
  "config.field.north.rest.auth.ccu.central": "Central (CCU)",
  "config.field.north.rest.auth.ccu.min_user_level": "Minimum user level",
  "config.field.north.rest.auth.ccu.role_mapping": "Role mapping",
  "config.field.north.rest.auth.ha_ingress.enabled": "HA Ingress passthrough",
  "config.field.north.rest.auth.ha_ingress.trusted_proxy_cidr":
    "Trusted proxy CIDR",
  "config.field.north.rest.auth.ha_ingress.role": "Granted role",
  "config.field.north.rest.openapi_spec_path": "OpenAPI spec path",
  "config.field.north.rest.openapi_validate":
    "Validate requests against OpenAPI spec",
  "config.field.north.rest.ws.replay_capacity":
    "WebSocket replay ring-buffer size",
  "config.field.persistence.values_cache.enabled": "Enable VALUES cache",
  "config.field.persistence.values_cache.flush_interval":
    "Cache flush interval",
  "config.field.persistence.values_cache.disabled_centrals": "Excluded CCUs",
  "config.field.backup.dir": "Backup directory",
  "config.field.backup.schedule": "Automatic backup interval",
  "config.field.backup.keep_last": "Keep last N backups",
  "config.field.alarm.enabled": "Alarm engine enabled",
  "config.field.alarm.default_siren_seconds": "Default siren duration (s)",
  "config.field.alarm.max_acoustic_per_incident_seconds":
    "Acoustic budget per incident (s)",
  "config.field.alarm.stop_verify_seconds":
    "Siren stop verification window (s)",
  "config.field.alarm.journal_retention_days": "Journal retention (days)",
  "config.field.alarm.restart_loop_breaker": "Restart loop breaker (re-fires)",
  "config.field.alarm.duress_visibility": "Duress and silent panic visibility",
  "config.field.persistence.history.enabled": "Enable history recorder",
  "config.field.persistence.history.retention": "Sample retention period",
  "config.field.persistence.history.energy_price_per_kwh":
    "Electricity tariff per kWh",
  "config.field.persistence.history.energy_currency": "Currency label",
  "config.field.persistence.history.retention_hourly":
    "Hourly rollup retention",
  "config.field.persistence.history.retention_daily": "Daily rollup retention",
  "config.field.persistence.history.flush_interval": "History flush interval",
  "config.field.persistence.history.include": "Include parameters",
  "config.field.persistence.history.exclude": "Exclude parameters",
  "config.field.persistence.history.disabled_centrals": "Excluded CCUs",
  "config.field.persistence.history.export.enabled": "Enable history export",
  "config.field.persistence.history.export.kind": "Export backend",
  "config.field.persistence.history.export.endpoint": "Export endpoint",
  "config.field.persistence.history.export.org": "InfluxDB org",
  "config.field.persistence.history.export.bucket": "InfluxDB bucket",
  "config.field.persistence.history.export.token_env": "Token env var",
  "config.field.reliability.command_retry_initial_delay": "Retry initial delay",
  "config.field.reliability.command_throttle_inter_command_delay":
    "Throttle inter-command delay",
  "config.field.centrals": "CCUs",
  "config.field.centrals.name": "Name",
  "config.field.centrals.host": "Host",
  "config.field.centrals.port": "Port",
  "config.field.centrals.json_rpc_port": "JSON-RPC port",
  "config.field.centrals.username": "Username",
  "config.field.centrals.password": "Password",
  "config.field.centrals.tls": "TLS",
  "config.field.centrals.tls_insecure_skip_verify": "Skip TLS verification",
  "config.field.centrals.primary_interface": "Primary interface",
  "config.field.centrals.ports": "Interface ports",
  "config.field.centrals.visibility.un_ignore": "Un-ignore patterns",
  "config.field.centrals.interfaces": "Interfaces",
  "config.field.centrals.interfaces.name": "Interface name",
  "config.field.centrals.interfaces.port": "Interface port",
  "config.field.centrals.interfaces.remote_path": "Remote path",
  "config.field.centrals.interfaces.rpc_type": "RPC type",
  "config.field.centrals.check_connection_interval":
    "Connection check interval",
  "config.field.centrals.behavior.delay_new_device_creation":
    "Defer new-device creation",
  "config.field.centrals.behavior.enable_device_firmware_check":
    "Firmware update entities",
  "config.field.centrals.behavior.enable_program_scan": "Scan programs",
  "config.field.centrals.behavior.enable_sysvar_scan": "Scan system variables",
  "config.field.centrals.behavior.include_internal_programs":
    "Include internal programs",
  "config.field.centrals.behavior.include_internal_sysvars":
    "Include internal sysvars",
  "config.field.centrals.behavior.light_last_brightness":
    "Restore last brightness",
  "config.field.centrals.behavior.program_markers": "Program markers",
  "config.field.centrals.behavior.sysvar_markers": "Sysvar markers",
  "config.field.centrals.behavior.sysvar_scan_interval": "Sysvar scan interval",
  "config.field.centrals.behavior.use_group_channel_for_cover_state":
    "Group channel for cover state",
  "config.field.north.mcp.enabled": "Enable MCP server",
  "config.field.north.mcp.allow_writes": "Allow writes",
  "config.field.north.mcp.path": "MCP mount path",
  "config.field.north.webhook.enabled": "Enable outbound webhook",
  "config.field.north.webhook.url": "Webhook URL",
  "config.field.north.webhook.secret": "Signing secret",
  "config.field.north.webhook.events": "Event filter",
  "config.field.north.webhook.centrals": "CCU filter",
  "config.field.north.webhook.parameter_glob": "Parameter glob",
  "config.field.north.webhook.timeout_ms": "Delivery timeout (ms)",
  "config.field.north.webhook.inbound.enabled": "Enable inbound webhook",
  "config.field.north.webhook.inbound.token": "Inbound token",
  "config.field.north.mqtt.retain_cleanup_window_ms":
    "Retain cleanup window (ms)",
  "config.field.north.rest.csrf_enabled": "CSRF protection",
  "config.field.north.rest.csrf_secure": "CSRF Secure cookie",
  "config.field.north.rest.tracing.otlp_endpoint": "OTLP trace endpoint",
  "config.field.addon_update.check_interval": "Update check interval",
  "config.field.addon_update.enabled": "Background update checks",
  // Inline help — shown beneath the field label. Same key
  // namespace as the labels above, but with `.help.` instead of
  // `.field.`. A missing help row is fine; the editor just
  // suppresses the hint line.
  "config.help.locale":
    "Default UI language for the very first SPA load. Operators can flip per-user via the Settings tab.",
  "config.help.data_dir":
    "Root for SQLite database, sessions, backups, logs. Must be writable; created on first start.",
  "config.help.bootstrap.allow_first_run_setup":
    "Keeps the unauthenticated onboarding surface (/setup) reachable while no authentication source exists. Set to false to keep it closed even on a database with zero users — the deliberate consequence is a lockout that only a YAML edit plus restart undoes. Bootstrap tier: edit the config file, not here.",
  "config.help.logging.level":
    "Filter threshold for the structured logger. debug exposes wire-level traces; info is the typical operator level.",
  "config.help.logging.format":
    "Handler shape. json for production / log shippers; text or text-color for terminal output.",
  "config.help.logging.overrides":
    "Per-subsystem level overrides keyed by dot-separated logger path. The most specific override wins.",
  "config.help.north.mqtt.enabled":
    "Master switch for the MQTT bridge. When off no broker connection is opened and no topics are emitted.",
  "config.help.north.mqtt.broker_url":
    "tcp://host:port (plain), tls://host:port (TLS), or mqtt:// / mqtts:// scheme aliases. Required when MQTT is enabled.",
  "config.help.north.mqtt.client_id":
    "MQTT client identifier the daemon registers with. Must be unique per broker connection.",
  "config.help.north.mqtt.username":
    "Broker username for authenticated brokers. Leave empty for anonymous brokers.",
  "config.help.north.mqtt.password":
    "Broker password — stored encrypted-at-rest by the OS, redacted from backups. Prefer setting via OPENCCU_LOOM_MQTT_PASSWORD env var.",
  "config.help.north.mqtt.topic_base":
    "Prefix every raw-plane and Discovery topic with this string. Change it when running multiple daemons against one broker.",
  "config.help.north.mqtt.raw_enabled":
    "Publish per-data-point state under <topic_base>/<interface>/... — the raw topic plane non-HA consumers subscribe to. Discovery needs it: switching Discovery on turns this on too, since Discovery payloads only point at raw-plane topics.",
  "config.help.north.mqtt.discovery_enabled":
    "Emit Home Assistant Discovery payloads so HA auto-registers the daemon's devices. Implies the raw plane — the payloads name its topics, so enabling this enables 'Publish raw plane' as well.",
  "config.help.north.mqtt.protocol_version":
    'MQTT wire dialect: "5" (default) or "3.1.1" for brokers without MQTT 5.0 support. No silent downgrade — a v5 connect against a v3-only broker fails with a named error.',
  "config.help.north.mqtt.payload_format":
    "Reserved, currently a no-op: state topics always carry the JSON envelope {value, available, modified_at} regardless of this setting. There is no primitive-scalar (bare) output mode today.",
  "config.help.north.mqtt.sub_devices_enabled":
    "Split multi-channel-group devices into one HA device per channel group. Renders the parent + N children hierarchy in HA.",
  "config.help.north.matter.enabled":
    "Master switch for the Matter bridge. Off by default. When enabled the daemon stands up the UDP listener and mDNS records.",
  "config.help.north.matter.enable_time_sync":
    "Mount the optional TimeSynchronization cluster (0x0038) on the Matter Root endpoint. Off by default — it is optional-only on a RootNode and some controllers (e.g. Apple Home) may reject the bridge at pairing when it appears. Enable only if a controller needs a time-sync surface, and re-pair afterwards.",
  "config.help.north.matter.listen":
    "UDP bind address for the Matter listener. :5540 is the IANA-assigned default; :0 lets the OS pick (useful in tests). Amazon Alexa can only commission bridges on port 5540.",
  "config.help.north.matter.vendor_id":
    "IANA-assigned vendor identifier. 0xFFF1 is the test / development vendor block — never ship that value to production.",
  "config.help.north.matter.product_id":
    "Vendor-assigned product identifier. Defaults to 0x8000.",
  "config.help.north.matter.node_label":
    "User-visible label for the bridge node, surfaced in commissioners (Apple Home, Google Home, …).",
  "config.help.north.matter.discriminator":
    "12-bit Matter commissioning discriminator. Combined with the passcode to form the manual setup code.",
  "config.help.north.matter.prefer_ipv4":
    "Force the Matter UDP socket to bind IPv4-only. Default false opens an IPv6 dual-stack socket that also accepts IPv4 traffic — the standard choice.",
  "config.help.north.matter.expose_secondary_channels":
    "Off by default: a multi-channel device (switch, dimmer, cover, lock, siren, valve) projects a single Matter endpoint from its primary channel. Enable to also expose its secondary virtual-receiver actor channels as separate endpoints. Matter only — MQTT, HA-Discovery and REST always carry every channel.",
  "config.help.north.matter.mdns_advertise":
    "mDNS advertiser implementation. Unset defaults to `zeroconf`, which publishes the operational + commissionable records on the network — required for pairing by QR code. `noop` keeps the records in-memory only (tests / out-of-band discovery); commissioners cannot discover the bridge in that mode.",
  "config.help.north.matter.dev_rotate_unique_ids":
    "Development-only: mix a per-boot 16-byte random salt into every bridged endpoint's Matter UniqueID. Apple Home / Google Home need a STABLE UniqueID across restarts to recognise accessories — leave this off in production.",
  "config.help.north.matter.commissioning.passcode":
    "27-bit Matter setup code (Spec §5.1.6.4) — between 00000001 and 99999998. Required to accept commissioner pairings; 0 leaves the PASE acceptor dormant.",
  "config.help.north.matter.commissioning.salt":
    "PBKDF2 salt persisted with the passcode (16–32 bytes per Spec §3.10). Empty falls back to a fixed development salt — never ship the default to production.",
  "config.help.north.matter.commissioning.iterations":
    "PBKDF2 iteration count (1000..100000 per Spec §3.10). Default 1000. Higher = more CPU at pairing time, harder to brute-force a captured transcript.",
  "config.help.north.matter.commissioning.concurrent_pairings":
    "Isolate the PASE adapter per exchange-id so multiple commissioners can pair in parallel. Default off — the singleton adapter is fine for the typical one-commissioner flow and cheaper memory-wise.",
  "config.help.north.matter.commissioning.ephemeral_window":
    "Generate a fresh discriminator + passcode + verifier each time the SPA opens a commissioning window. Recommended for production: pairing codes auto-rotate and the static configured passcode acts only as the long-lived label-code fallback.",
  "config.help.north.matter.case.node_id":
    "64-bit operational node identifier inside the fabric. 0 disables the CASE responder entirely — needed when no commissioner has installed a NOC yet.",
  "config.help.north.matter.case.fabric_id":
    "64-bit fabric identifier the bridge belongs to. Required when Node ID is non-zero.",
  "config.help.north.matter.attestation.dac_path":
    "Filesystem path to the vendor-supplied Device Attestation Certificate (PEM or DER). Empty falls back to an ephemeral self-signed development DAC that only validates under chip-tool --bypass-attestation-verifier.",
  "config.help.north.matter.attestation.dac_key_path":
    "Filesystem path to the DAC's P-256 private key (PEM PKCS#8 or DER). MUST match the public key embedded in the DAC certificate.",
  "config.help.north.matter.attestation.pai_path":
    "Filesystem path to the Product Attestation Intermediate certificate. Provided by the CSA along with the DAC.",
  "config.help.north.matter.attestation.cd_path":
    "Filesystem path to the CSA-signed Certification Declaration (a CMS / PKCS#7 message). The CD pins the vendor + product as a certified Matter device.",
  "settings.section.intro.north.matter":
    "Native-Go Matter bridge that exposes selected CCU devices as Matter accessories. Disabled by default. Production deployments need vendor-supplied attestation material (DAC / PAI / CD) in the Expert section; dev work pairs via chip-tool --bypass-attestation-verifier.",
  "settings.section.intro.north.mcp":
    "MCP (Model Context Protocol) server that exposes CCU devices to LLM agents as tools, served on the REST listener. Disabled by default and read-only until “Allow writes” is also enabled. Changes take effect after a daemon restart. See ADR 0025.",
  "config.help.north.mcp.enabled":
    "Master switch for the MCP server, served over Streamable-HTTP on the REST listener. Off by default. Takes effect after a daemon restart.",
  "config.help.north.mcp.allow_writes":
    "Enable write-capable MCP tools (e.g. set_datapoint). Off by default — the server alone is read-only; turn this on for agent-driven control. Takes effect after a daemon restart.",
  "config.help.north.mcp.path":
    "HTTP mount path for the MCP transport on the REST listener. Empty defaults to /mcp. Takes effect after a daemon restart.",
  "config.help.north.webhook.enabled":
    "Master switch for the outbound webhook. When on, the daemon POSTs a signed JSON payload to the configured URL on datapoint, system-status and incident events. Off by default. Takes effect after a daemon restart.",
  "config.help.north.webhook.url":
    "Absolute http(s) endpoint each event is POSTed to. Empty disables delivery even while enabled.",
  "config.help.north.webhook.secret":
    "Shared key for the HMAC-SHA256 body signature sent in the X-OpenCCU-Signature header. Empty means no signature is sent (the receiver cannot verify authenticity).",
  "config.help.north.webhook.events":
    "Allowlist of event-type tags to deliver (e.g. datapoint.value_changed). Empty delivers all supported events.",
  "config.help.north.webhook.centrals":
    "Allowlist of CCU names to deliver events for. Empty delivers events from all CCUs.",
  "config.help.north.webhook.parameter_glob":
    "Optional glob (e.g. *TEMPERATURE*) restricting datapoint events to matching parameter names. Empty applies no parameter filter; other event types are unaffected.",
  "config.help.north.webhook.timeout_ms":
    "Per-delivery HTTP timeout in milliseconds. Zero or negative uses the 10000 ms default.",
  "config.help.north.webhook.inbound.enabled":
    "Master switch for the inbound webhook REST surface (POST /api/v1/webhook/value and /api/v1/webhook/program). Off by default. Routes are mounted only when enabled, so changing this takes effect after a daemon restart. Inbound requests are real device writes / program runs.",
  "config.help.north.webhook.inbound.token":
    "Optional bearer token accepted in addition to the normal auth chain, so a header-only caller (e.g. a doorbell) can POST without a session or user login. Sent as Authorization: Bearer <token>. Empty means only the normal auth chain applies.",
  "config.help.north.discovery.mdns.enabled":
    "Advertise the daemon's REST listener via mDNS / Zeroconf so LAN clients (e.g. Home Assistant) can auto-discover it.",
  "config.help.north.discovery.mdns.instance_name":
    "Leftmost label of the mDNS SRV / TXT record. Empty falls back to the OS hostname.",
  "config.help.north.discovery.ssdp.enabled":
    "Periodically scan the LAN for Homematic / OpenCCU central units via SSDP/UPnP so they can be adopted with one click. Read-only — no data about the daemon leaves the LAN.",
  "config.help.north.discovery.ssdp.interval":
    "How often the discovery scan re-runs (e.g. 60s). Empty falls back to 60 seconds.",
  "config.help.north.rest.enabled":
    "Master switch for the REST + WebSocket server. Disabling it leaves the daemon with no operator-facing surface.",
  "config.help.north.rest.listen":
    "Bind address for REST + WebSocket. :8119 listens on every interface; tighten with a host: prefix when needed.",
  "config.help.north.rest.public_url":
    "Externally-reachable base URL of this daemon (scheme + host [+ port]), e.g. https://loom.example.com. Used to build absolute links such as the OIDC redirect URL and to derive secure-cookie behaviour. Leave empty to infer it per request — set it when running behind a reverse proxy or under a custom domain.",
  "config.help.north.rest.tls_cert_file":
    "Path to the PEM certificate (chain). Set this together with the key file to serve the API + SPA over HTTPS on the same port; leave both empty for plain HTTP behind a TLS-terminating proxy. An uploaded certificate is written to this path and watched for hot-reload — the upload replaces the file's contents, it does not remove the need to pick a location.",
  "config.help.north.rest.tls_key_file":
    "Path to the PEM private key that matches the certificate file. Required together with the certificate to enable HTTPS; an uploaded key is written here and hot-reloaded on change.",
  "config.help.north.rest.cors":
    'Whitelisted browser origins for cross-origin REST calls. Empty disables CORS entirely; use ["*"] only for development.',
  "config.help.north.rest.auth.basic_enabled":
    "Accept HTTP Basic credentials on protected routes. Useful for curl + CI. Default on; set to false to reject Basic auth even when users are configured.",
  "config.help.north.rest.auth.bearer_enabled":
    "Accept Bearer tokens via Authorization header. Use for automation. Default on; set to false to reject tokens even when they are configured.",
  "config.help.north.rest.auth.oidc.enabled":
    "Enable OpenID Connect single-sign-on. The login page surfaces an SSO button when configured.",
  "config.help.north.rest.auth.oidc.issuer":
    "Issuer URL (no trailing slash). The .well-known/openid-configuration document is fetched on daemon start.",
  "config.help.north.rest.auth.oidc.client_id":
    "Public client identifier registered with the IdP. PKCE flow.",
  "config.help.north.rest.auth.oidc.redirect_url":
    "Must match the URL registered with the IdP. Points at the daemon's OIDC callback handler.",
  "config.help.north.rest.rate_limit.enabled":
    "Enforce per-identity token-bucket rate limiting on REST requests. Excess returns HTTP 429.",
  "config.help.north.rest.rate_limit.requests_per_second":
    "Steady-state token-refill rate per identity. 10 is a sensible starting point.",
  "config.help.north.rest.rate_limit.burst":
    "Token-bucket size — maximum concurrent requests per identity before the limiter gates.",
  "config.help.north.ui.enabled":
    "Bootstrap UI surface (login, /setup wizard, /health). The SPA itself lives on the REST listener.",
  "config.help.north.ui.embedded":
    "Turn on when Home Assistant owns this daemon's config surface — the Homematic(IP) Local integration runs against this daemon. Hides what HA already owns. Not derived from Ingress: the add-on is also used without the integration. Where the setting applies is a separate question — see “Scope of the embedded mode”.",
  "config.help.north.ui.embedded_scope":
    "Where the embedded mode applies. “Only in Home Assistant” (default) reduces the UI only for requests that arrive through Home Assistant, so anyone who opens this daemon's own address keeps the full UI — they chose Loom over the HA panel, and the reason for hiding (Home Assistant shows the same editor) does not apply to them. “Everywhere” reduces it on every path.",
  "config.help.north.ui.profiles":
    "Per-profile navigation overrides, edited under Settings → Navigation & views. Stored sparsely, so views added by a later release keep the default their own code assigns.",
  "config.help.callback.host":
    "Local interface the XML-RPC + BIN-RPC callback listeners bind to. Lock down via firewall, not via bind address.",
  "config.help.callback.port":
    "XML-RPC callback listener port. 0 lets the OS pick an ephemeral port; the daemon re-advertises it on every CCU reconnect.",
  "config.help.callback.bin_port":
    "BIN-RPC callback listener port (CUxD). 0 resolves to the 8129 default, not an OS-assigned port — unlike the XML-RPC port above, this listener has no port-range escape valve, so two daemons on one host each need an explicit, distinct value here.",
  "config.help.callback.port_range":
    "Optional port range <lo>-<hi>; the callback listener binds the first free port in it. Takes precedence over the XML-RPC port above. Use when the daemon sits behind a narrow firewall range.",
  "config.help.callback.public_host":
    "Hostname the daemon announces to the CCU in every init() call. Set when running behind NAT.",
  "config.help.callback.max_connections":
    "Cap on simultaneous connections per callback listener (XML-RPC and BIN-RPC). Bounds memory/goroutine use if an untrusted LAN host floods the socket. 0 uses the default (64).",
  "config.help.callback.restrict_source_ips":
    "Only accept callbacks from the configured CCU IPs plus loopback. Adds a source-IP allowlist on top of the connection cap. Off by default; enable when no legitimate host other than your CCUs reaches the callback ports.",
  "config.help.ccu_data.translations_path":
    "Filesystem path to the OCCU translations ZIP. Defaults to the embedded archive bundled with the binary; override only when testing a custom extract.",
  "config.help.ccu_data.easymode_path":
    "Filesystem path to the OCCU easymode ZIP. Defaults to the embedded archive; override only when testing a custom extract.",
  "config.help.north.rest.auth.users":
    "Seed-only user map loaded once on first start. Manage users after boot via the Users tab; entries here are ignored once the database exists.",
  "config.help.north.rest.auth.tokens":
    "Seed-only API token map loaded once on first start. Manage tokens after boot via the API Tokens tab; entries here are ignored once the database exists.",
  "config.help.north.rest.auth.oidc.client_secret":
    "Confidential client secret registered with the IdP. Leave empty for public clients (PKCE-only). Prefer setting via environment variable.",
  "config.help.north.rest.auth.oidc.role_claim":
    'JWT claim name the daemon reads to determine the user role (admin / user). Defaults to "role".',
  "config.help.north.rest.auth.ccu.enabled":
    "Delegate login to the named CCU's user database. Users sign in with their CCU accounts; local users remain as a break-glass fallback. Restart required.",
  "config.help.north.rest.auth.ccu.primary":
    "When on, the CCU is tried first and local users are the break-glass fallback. Off makes local users primary and the CCU the last resort. Break-glass holds either way.",
  "config.help.north.rest.auth.ccu.central":
    "Name of the configured central whose user database authenticates logins. Empty selects the first configured central.",
  "config.help.north.rest.auth.ccu.min_user_level":
    "Reject CCU users below this UserLevel (8 admin, 2 operator, 1 guest; 0 is always denied). Default 1 admits any real user.",
  "config.help.north.rest.auth.ccu.role_mapping":
    'Override the default CCU UserLevel→Loom-role mapping. Keys are the UserLevel as a string ("8", "2", "1"); values are "admin" / "operator" / "viewer". Empty uses the defaults (≥8 admin, ≥2 operator, ≥1 viewer).',
  "config.help.north.rest.auth.ha_ingress.enabled":
    "Trust Home Assistant Ingress: a request proxied by the Supervisor counts as an authenticated admin — no login. Default (unset) = on in the HA add-on, off in a plain build; set On/Off to override. Safe only with the add-on's panel_admin: true (admins-only Ingress); real tokens/sessions still win. Restart required.",
  "config.help.north.rest.auth.ha_ingress.trusted_proxy_cidr":
    "Network the Ingress request's real peer must come from. Empty uses the HA Supervisor default 172.30.32.0/23. X-Forwarded-For is never trusted.",
  "config.help.north.rest.auth.ha_ingress.role":
    'Loom role granted to a trusted Ingress request: "admin" (default), "operator" or "viewer".',
  "config.help.north.rest.openapi_spec_path":
    "Override path for the OpenAPI YAML. Defaults to the copy embedded in the binary at build time. Expert: set only when hot-patching the spec during development.",
  "config.help.north.rest.openapi_validate":
    "Validate every incoming REST request against assets/openapi.yaml at runtime. Default true. Disable only on heavily degraded hardware where the ~1 ms overhead is measurable.",
  "config.help.north.rest.ws.replay_capacity":
    "Ring-buffer depth for the subscribe-with-since WebSocket feature. Default 1024 events. Reduce on memory-constrained hosts; increase if late subscribers miss bursts.",
  "config.help.persistence.values_cache.enabled":
    "Cache the most recent VALUES paramset of every data point locally so a daemon restart picks up where it left off without a full CCU re-read. Default: ON.",
  "config.help.persistence.values_cache.flush_interval":
    "How often to flush queued cache writes to disk. Default 60 s — short enough to survive a crash with minimal loss, long enough to coalesce bursts.",
  "config.help.persistence.values_cache.disabled_centrals":
    "List of central names (one per line) whose data points are kept out of the cache. Useful for test rigs in a multi-CCU deployment.",
  "config.help.backup.dir":
    "Where downloaded CCU archives are stored. Empty means <data directory>/backups. On a CCU add-on install the data directory sits inside the tree the CCU backs up itself, so pointing this at external storage keeps the CCU's own backups from growing with every archive. Takes effect after a daemon restart.",
  "config.help.backup.schedule":
    "How often each configured CCU is backed up automatically (e.g. 24h). Zero disables scheduled backups; manual backups via the Backups view still work. The first automatic backup runs one interval after start, not immediately.",
  "config.help.backup.keep_last":
    "Bounds how many scheduled backups are retained per CCU: after each successful backup the oldest beyond this count are deleted. Zero keeps all.",
  "config.help.alarm.enabled":
    "Master switch for the alarm engine. With no zones configured yet the engine stays inert either way. Takes effect after a daemon restart.",
  "config.help.alarm.default_siren_seconds":
    "Default acoustic activation duration in seconds, used when an alarm output does not configure its own siren duration.",
  "config.help.alarm.max_acoustic_per_incident_seconds":
    "Cumulative acoustic budget in seconds for one incident across all re-triggers and restarts, so a stuck sensor cannot sound a siren indefinitely.",
  "config.help.alarm.stop_verify_seconds":
    "How long, in seconds, an unverified siren-stop command is retried before it is escalated to a health incident.",
  "config.help.alarm.journal_retention_days":
    "How many days alarm-journal entries are kept before being pruned. Zero disables retention (entries are kept forever).",
  "config.help.alarm.restart_loop_breaker":
    "Caps how many times a restore-driven output may re-fire within one incident before the engine degrades to optical signalling and notifications only.",
  "config.help.alarm.duress_visibility":
    "Where a duress-code use or a silent panic trigger may appear. 'hidden' reaches the webhook and nothing else — with no webhook configured, nobody is told at all. 'notify_only' (default) additionally sends the notification event, so a phone is reached, but never writes it to retained state or the local screens. 'full' treats it like any other alarm. The threat is not an insecure Home Assistant — it is that whoever stands next to you sees the same screen. Whether Home Assistant shows the notification as a lock-screen banner is outside this setting; use a notify channel without preview if that matters.",
  "config.help.persistence.history.enabled":
    "Master switch for the measurement-history recorder; off by default (opt-in) — when enabled the daemon opens history.db and starts the retention job.",
  "config.help.persistence.history.retention":
    "How long raw samples are kept; zero falls back to 30 days (720 h), after which the retention job purges older rows.",
  "config.help.persistence.history.energy_price_per_kwh":
    "Price of one kilowatt-hour, used by the energy view to show costs next to consumption. Leave at 0 to show no costs at all — a tariff of 0 would render every amount as 0.00.",
  "config.help.persistence.history.energy_currency":
    "Label for the amounts derived from the tariff (symbol or code, e.g. € or CHF). Defaults to the euro sign. Purely a label — no conversion happens.",
  "config.help.persistence.history.retention_hourly":
    "How long the hourly rollup tier is kept; zero falls back to 13 months. Hourly rows are folded into the daily tier before this cutoff removes them.",
  "config.help.persistence.history.retention_daily":
    "How long the daily rollup tier is kept; zero (default) keeps daily rows forever since they are tiny (one row per data point per day).",
  "config.help.persistence.history.flush_interval":
    "How often the recorder flushes a batch of samples to history.db; zero falls back to the daemon default of 5 s.",
  "config.help.persistence.history.include":
    "Parameter-name globs to record (e.g. TEMPERATURE, *POWER*); empty (default) records every numeric VALUES parameter.",
  "config.help.persistence.history.exclude":
    "Parameter-name globs to drop from recording; exclude always wins over include — empty (default) excludes nothing.",
  "config.help.persistence.history.disabled_centrals":
    "Central names whose data points must not be recorded; empty (default) records all enabled centrals.",
  "config.help.persistence.history.export.enabled":
    "Turn on the push exporter that forwards each recorded sample to an external time-series store (InfluxDB by default); disabled by default.",
  "config.help.persistence.history.export.kind":
    'Exporter backend; empty or "influxdb" selects the InfluxDB v2 line-protocol writer (the only available backend today).',
  "config.help.persistence.history.export.endpoint":
    "Base URL of the target time-series store, e.g. http://influx:8086.",
  "config.help.persistence.history.export.org":
    "InfluxDB v2 organisation name that owns the target bucket.",
  "config.help.persistence.history.export.bucket":
    "InfluxDB v2 bucket into which samples are written.",
  "config.help.persistence.history.export.token_env":
    "Name of the environment variable that holds the InfluxDB write token; the token is never stored inline in config.",
  "config.help.reliability.command_retry_initial_delay":
    "First backoff delay after a transient CCU write failure (the retry-stack doubles on each step). Default 2 s (production-hardened); lower it for fast test rigs.",
  "config.help.reliability.command_throttle_inter_command_delay":
    "Minimum gap between two consecutive throttled commands per CCU interface. Default 0 (no pacing). Raise to ~50–500 ms on heavily-loaded BidCos-RF interfaces to reduce duty-cycle errors.",
  "config.help.centrals":
    "All configured CCUs. Manage them in the dedicated CCUs tab — entries set here in config.yaml act as bootstrap seeds.",
  "config.help.centrals.name": "Managed in the CCUs tab.",
  "config.help.centrals.host": "Managed in the CCUs tab.",
  "config.help.centrals.port": "Managed in the CCUs tab.",
  "config.help.centrals.json_rpc_port": "Managed in the CCUs tab.",
  "config.help.centrals.username": "Managed in the CCUs tab.",
  "config.help.centrals.password": "Managed in the CCUs tab.",
  "config.help.centrals.tls": "Managed in the CCUs tab.",
  "config.help.centrals.tls_insecure_skip_verify": "Managed in the CCUs tab.",
  "config.help.centrals.primary_interface": "Managed in the CCUs tab.",
  "config.help.centrals.ports": "Managed in the CCUs tab.",
  "config.help.centrals.visibility.un_ignore": "Managed in the CCUs tab.",
  "config.help.centrals.interfaces": "Managed in the CCUs tab.",
  "config.help.centrals.interfaces.name": "Managed in the CCUs tab.",
  "config.help.centrals.interfaces.port": "Managed in the CCUs tab.",
  "config.help.centrals.interfaces.remote_path": "URL path this interface's XML-RPC requests are sent to. Leave empty for the CCU default (/RPC2, or /groups for VirtualDevices); set an absolute path only when a reverse proxy re-routes the interface.",
  "config.help.centrals.interfaces.rpc_type": "Transport of this interface. It follows the interface name — CUxD speaks BIN-RPC, every other interface XML-RPC — so this only confirms that derivation; a contradicting value is rejected when the configuration is loaded.",
  "config.help.centrals.check_connection_interval":
    "How often the daemon pings the CCU in the background; zero uses the compiled-in default of 30 s, negative disables the check entirely.",
  "config.help.centrals.behavior.delay_new_device_creation":
    "Hold a newly-paired device back until you accept it: it is listed in the inbox and only gets data points once accepted; default false.",
  "config.help.centrals.behavior.enable_device_firmware_check":
    "Surface a firmware-update entity for every device that reports available firmware; default true.",
  "config.help.centrals.behavior.enable_program_scan":
    "Fetch CCU programs and expose them as hub entities; disable to skip program discovery entirely (default true).",
  "config.help.centrals.behavior.enable_sysvar_scan":
    "Fetch CCU system variables and expose them as hub entities; disable to skip sysvar discovery entirely (default true).",
  "config.help.centrals.behavior.include_internal_programs":
    "Include CCU-internal programs (those not intended for user control) in the hub entity surface; default false.",
  "config.help.centrals.behavior.include_internal_sysvars":
    "Include CCU-internal system variables in the hub entity surface; default true.",
  "config.help.centrals.behavior.light_last_brightness":
    "When turning a light on, restore the last non-zero brightness the CCU reported rather than switching to full (100%); default true.",
  "config.help.centrals.behavior.program_markers":
    "Tokens the CCU description of a program may carry. As for system variables, markers decide only how a program arrives, not whether it is imported: marker-matched programs arrive enabled in Home Assistant, everything else disabled. HX is a free marker for your own filtering, and INTERNAL additionally includes the CCU's internal programs — which matters because the CCU flags most ordinary programs as internal. HAHM has no effect here; it makes system variables writable and programs have no value to write. Empty: everything is imported, everything disabled.",
  "config.help.centrals.behavior.sysvar_markers":
    "Tokens the CCU description of a system variable may carry. Markers do not decide whether a variable is imported — everything is imported — only how it arrives: marker-matched variables arrive enabled in Home Assistant, everything else arrives disabled for you to switch on per entity. HAHM makes a variable writable (switch, select, number, text instead of a read-only sensor). HX is a free marker for your own filtering. INTERNAL additionally includes the CCU's internal variables. Empty: everything is imported, everything disabled.",
  "config.help.centrals.behavior.sysvar_scan_interval":
    "How often the daemon refreshes system variables from the CCU. Zero uses the compiled-in default of 30 seconds; below 3 seconds is rejected, because each cycle costs the CCU a script run on a single-threaded interpreter.",
  "config.help.centrals.behavior.use_group_channel_for_cover_state":
    "Report a cover's position from its group-channel LEVEL rather than from its own channel; default true.",
  "config.help.north.mqtt.retain_cleanup_window_ms":
    "How long (in milliseconds) the daemon waits for the broker to deliver all retained messages before processing the retain-cleanup eviction list; zero falls back to 2000 ms.",
  "config.help.north.rest.csrf_enabled":
    "Mount the double-submit cookie/header CSRF guard on mutating REST endpoints; enabled by default for browser-facing deployments — disable only for pure API-token setups where no session cookies are issued.",
  "config.help.north.rest.csrf_secure":
    "Set the Secure flag on the CSRF cookie; enable when the daemon is behind an HTTPS / TLS terminator.",
  "config.help.north.rest.tracing.otlp_endpoint":
    "Base URL of an OTLP/HTTP trace collector (e.g. http://jaeger:4318); empty (default) disables span export entirely.",
  "config.help.addon_update.check_interval":
    "How often the daemon checks GitHub for a new add-on release in the background, plus a random jitter of up to 1 hour so a fleet doesn't poll all at once. Zero falls back to the default of 24 h; use the enabled toggle to turn background checking off.",
  "config.help.addon_update.enabled":
    'Check GitHub for new add-on releases in the background (boot check plus the recurring interval); default on. The manual "Check for updates" button and installing stay available when disabled.',
  "settings.section.intro.persistence":
    "Local on-disk cache of CCU data-point values. The cache lets the daemon survive restarts without re-reading every paramset from the CCU. By default it is ON with a 60-second flush interval — leave it alone unless you are debugging cache behaviour.",
  "settings.section.intro.reliability":
    "Knobs for the southbound transport stack (XML-RPC / BIN-RPC retry, throttle, circuit breaker). Defaults match aiohomematic's behaviour; touch only when the CCU shows duty-cycle errors or you are chasing a latency regression.",
  "settings.section.intro.callback":
    "XML-RPC and BIN-RPC callback listeners that the CCU pushes state-change events into. Defaults bind on 0.0.0.0 and let the OS pick ports; override only when the daemon sits behind NAT or a narrow firewall.",
  "settings.interface": "Interface",
  "settings.language": "Language",
  "settings.start_route": "Start page",
  "settings.start_route.default": "Device list (default)",
  "settings.start_route.help":
    "The view that opens after logging in. Opening a direct link always wins over this setting.",
  "settings.start_route.saved": "Start page saved",
  "settings.theme": "Theme",
  "settings.theme.light": "Light",
  "settings.theme.dark": "Dark",
  "settings.theme.system": "System",
  "settings.appearance.design": "Design",
  "settings.appearance.design.help":
    "Choose the visual style. Inside Home Assistant the SPA follows your HA theme automatically.",
  "settings.appearance.design.loom": "OpenCCU-Loom",
  "settings.appearance.design.ha": "Home Assistant",
  "settings.appearance.design.embedded_hint": "Managed by Home Assistant",
  "settings.daemon": "Daemon",
  "settings.copy_json": "Copy JSON",
  "settings.rooms": "Rooms",
  "settings.functions": "Functions",
  "settings.users": "Users",
  "settings.tokens": "API tokens",
  "settings.show_raw": "Show raw JSON",
  "settings.system": "System",
  "settings.enabled": "Enabled",
  "settings.startup_capture": "Bootstrap capture",
  "settings.startup_capture_help":
    "Open a diagnostics capture as the very first boot step so the wire / paramset / callback init lands in the archive. Effective on the next daemon restart.",
  "settings.startup_capture_saved": "Saved. Takes effect on next restart.",
  "settings.mqtt.reload_title": "Apply MQTT changes",
  "settings.mqtt.reload_description":
    "After saving the section above the new values land in the configuration store, but the running MQTT stack keeps using the previous broker connection. Click reload to tear down + rebuild the stack without a daemon restart. The new connection is established before the old one is dropped — on failure the previous stack continues unchanged.",
  "settings.mqtt.reload": "Reload MQTT now",
  "settings.mqtt.reload_running": "Reloading…",
  "settings.mqtt.reload_success": "MQTT reloaded ({ms} ms).",
  "settings.mqtt.reload_failed": "Reload failed: {err}",
  "settings.restart_daemon": "Restart daemon",
  "settings.restart_daemon_help":
    "Sends SIGTERM to the daemon. In production (systemd / Docker) the supervisor restarts the process; in dev you have to start it manually.",
  "settings.restart_daemon_unsupervised":
    "Disabled — no supervisor detected (no systemd, Docker, or Kubernetes). Triggering a restart would leave the daemon offline. Set OPENCCU_LOOM_SUPERVISOR=1 to override.",
  "settings.secret_env_override":
    "Override at runtime by setting the {name} environment variable. When set it wins over the value entered here.",
  "settings.secret_from_env":
    "Currently resolved at runtime from {name}; clear the env variable to use the value entered here.",
  "settings.secret_not_set": "Not set — no value stored.",
  "connectivity.ccu": "CCU",
  "connectivity.mqtt": "MQTT",
  "connectivity.matter": "Matter",
  "connectivity.green": "OK",
  "connectivity.amber": "degraded",
  "connectivity.red": "failed",
  "connectivity.grey": "disabled",
  "connectivity.no_components": "Not wired into the daemon's health surface.",
  "settings.restart_confirm":
    "Really restart the daemon? CCU connections drop for a few seconds.",
  "settings.restart_signalled": "Shutdown signalled — wait for the supervisor.",
  "settings.restarting": "Restarting…",
  "admin.cache_clear.button": "Clear CCU cache",
  "admin.cache_clear.title": "Clear CCU cache",
  "admin.cache_clear.body":
    "Discards all CCU-derived in-memory and on-disk caches (device data, paramsets, values, master profiles). The daemon re-pulls everything from the CCU on next access. Config, visibility settings, auth sessions and Matter state are NOT affected.",
  "admin.cache_clear.confirm": "Clear cache",
  "admin.cache_clear.success":
    "Cache cleared — {devices} devices, {paramsets} paramsets, {values} values, {centrals} centrals re-initialised.",
  "admin.cache_clear.error": "Cache clear failed: {err}",
  "admin.cache_clear.heading": "Clear CCU cache",
  "admin.cache_clear.help":
    "Removes all CCU-derived caches without restarting. Useful after importing data or when the daemon's view diverges from the CCU.",
  "settings.callback_ports": "Callback ports",
  "settings.feature_off": "off",
  "settings.live_edit_pending":
    "Live editing arrives in Phase 11 — until then daemon values are read from config.yaml and shown here read-only.",
  "settings.users_managed_yaml":
    "Users + API tokens are managed via config.yaml today. Live editing arrives in Phase 11.",
  "settings.tokens_secret":
    "Token values are never exposed; only the last six characters are shown as a fingerprint.",
  "settings.rooms_help":
    "Derived from CCU device metadata. Rooms and functions are assigned per device from the detail header.",
  // --- Settings sidebar groups ---
  "settings.group.general": "General & System",
  "settings.group.bridges": "Bridges (Northbound)",
  "settings.group.ccus": "CCUs & Connectivity",
  "settings.group.security": "Security & Access",
  "settings.group.advanced": "Advanced",
  // --- Settings section subgroups (within a tab) ---
  "config.subgroup.general": "General",
  "config.subgroup.auth": "Authentication",
  "config.subgroup.oidc": "OIDC (OpenID Connect)",
  "config.subgroup.rate_limit": "Rate Limiting",
  "config.subgroup.ws": "WebSocket",
  "config.subgroup.tracing": "Tracing",
  "config.subgroup.commissioning": "Commissioning",
  "config.subgroup.case": "CASE Session",
  "config.subgroup.attestation": "Attestation",
  "config.subgroup.mdns": "mDNS",
  "config.subgroup.ssdp": "SSDP",
  "config.subgroup.history": "Measurement History",
  "config.subgroup.values_cache": "VALUES Cache",
  "config.subgroup.behavior": "Behavior",
  // --- Device parameter groups (channel MASTER paramset editor) ---
  "config.paramgroup.temperature": "Temperature",
  "config.paramgroup.timing": "Timing & Duration",
  "config.paramgroup.display": "Display",
  "config.paramgroup.transmission": "Transmission & Communication",
  "config.paramgroup.powerup": "Power-Up Behavior",
  "config.paramgroup.boost": "Boost",
  "config.paramgroup.button": "Button Behavior",
  "config.paramgroup.threshold": "Thresholds & Conditions",
  "config.paramgroup.status": "Status & Reporting",
  "config.paramgroup.other": "Other Settings",
  // --- Settings tabs ---
  "settings.tab.general": "General",
  "settings.tab.ccus": "CCUs",
  "settings.tab.mqtt": "MQTT",
  "settings.tab.matter": "Matter",
  "settings.tab.mcp": "MCP",
  "settings.tab.discovery": "Discovery (mDNS)",
  "settings.tab.rest": "API & WebSocket",
  "settings.tab.oidc": "OIDC",
  "settings.tab.ccu_auth": "CCU login",
  "settings.ccu_auth.hint":
    "Delegate login to the CCU's own user database. When enabled, users sign in with their CCU accounts; local users stay as a break-glass fallback. Changes take effect after a daemon restart.",
  "settings.tab.callback": "Callback Ports",
  "settings.tab.reliability": "Reliability",
  "settings.tab.persistence": "Persistence",
  "settings.tab.visibility": "Hidden parameters",
  "settings.tab.groups": "Rooms & Functions",
  "settings.tab.users": "Users",
  "settings.tab.tokens": "API Tokens",
  "settings.tab.system": "System",
  "settings.tab.changes": "Changed settings",
  "settings.tab.navviews": "Navigation & views",

  // --- Surface-profile editor ------------------------------------
  "navviews.banner":
    "Hiding a view removes it from the navigation for every user of this daemon. API tokens, Loom accounts and MQTT are unaffected — restrict those through roles and tokens.",
  "navviews.scope.title": "Where does the embedded mode apply?",
  "navviews.scope.inside_ha": "Only in Home Assistant",
  "navviews.scope.inside_ha.desc":
    "The reduced navigation applies to the Home Assistant panel. Anyone who opens this daemon's own address keeps the full user interface — they chose Loom over the panel, so the reason for hiding does not apply to them.",
  "navviews.scope.always": "Everywhere",
  "navviews.scope.always.desc":
    "The reduced navigation applies on every path, including direct access. Choose this for a daemon whose interface should always look the same.",
  "navviews.scope.here.inside": "you are in the Home Assistant panel",
  "navviews.scope.here.direct": "you opened this daemon directly",
  "navviews.toast.scope_saved": "Scope saved.",
  "navviews.mode.title": "Embedded in Home Assistant",
  "navviews.mode.desc":
    "Turn this on when Home Assistant owns this daemon's config surface — the Homematic(IP) Local integration is configured against this daemon. Loom then hides what Home Assistant already provides: its own login, user and token administration, the CCU connection, the device editors, Matter and the aggregated charts.",
  "navviews.mode.live": "Live profile",
  "navviews.mode.views_visible": "{visible} of {total} views visible",
  "navviews.mode.deviations": "{count} deviations from defaults",
  "navviews.profile.editing": "Editing profile",
  "navviews.profile.standalone": "Standalone",
  "navviews.profile.embedded": "Embedded",
  "navviews.profile.live": "live",
  "navviews.profile.reset": "Reset profile to defaults",
  "navviews.search": "Find a view…",
  "navviews.filter.label": "Filter views",
  "navviews.filter.all": "All",
  "navviews.filter.visible": "Visible",
  "navviews.filter.hidden": "Hidden",
  "navviews.filter.changed": "Changed from default",
  "navviews.group.overview": "Overview",
  "navviews.group.automation": "Automation",
  "navviews.group.diagnose": "Diagnostics",
  "navviews.group.bridges": "Bridges",
  "navviews.group.system": "System",
  "navviews.group.settings": "Settings tabs",
  "navviews.group.device": "Device detail tabs",
  "navviews.group.count": "{visible} of {total} visible",
  "navviews.group.show_all": "Show all",
  "navviews.group.hide_all": "Hide all",
  "navviews.row.ha_owns": "Home Assistant provides this.",
  "navviews.row.multi_central":
    "Visible by default because this daemon serves {count} CCUs: Home Assistant addresses one CCU per config entry, so for the others this is the only editor there is.",
  "navviews.row.locked": "Cannot be hidden — {why}",
  "navviews.row.unavailable": "Unavailable — {why}",
  "navviews.row.role_admin": "Only visible to admins.",
  "navviews.row.opens_hidden":
    "“{target}” is hidden, so the entries of this overview do not link. The list itself stays.",
  "navviews.row.opened_by_hidden":
    "Hidden here also drops the jump out of “{source}”. That overview keeps its list.",
  "navviews.row.changed_from": "Changed · default: {default}",
  "navviews.row.default_visible": "visible",
  "navviews.row.default_hidden": "hidden",
  "navviews.row.reset_one": "Reset to default",
  "navviews.preview.title": "Preview",
  "navviews.preview.sub_live": "How the navigation looks after saving.",
  "navviews.preview.sub_other":
    "Preview of the {profile} profile — not currently live.",
  "navviews.preview.none": "Nothing visible in this section.",
  "navviews.save.count": "{count} unsaved changes",
  "navviews.save.discard": "Discard",
  "navviews.save.save": "Save changes",
  "navviews.toast.saved":
    "Navigation saved. Every user sees the new layout on their next navigation.",
  "navviews.toast.reset": "Profile reset to defaults — not saved yet.",
  "navviews.toast.discarded": "Changes discarded.",
  "navviews.toast.mode_on": "Embedded mode is on. The Embedded profile is now live.",
  "navviews.toast.mode_off":
    "Embedded mode is off. The Standalone profile is now live.",
  "navviews.toast.error": "Could not save",
  "navviews.dlg.hide_title": "Hide “{surface}”?",
  "navviews.dlg.hide_ok": "Hide it",
  "navviews.dlg.mode_on_title": "Switch to embedded mode?",
  "navviews.dlg.mode_on_text":
    "Home Assistant becomes the place for identity, the CCU connection and the device editors, so this Config UI stops offering them. It changes what every user of this daemon sees here; nothing about what Home Assistant or the APIs may do.",
  "navviews.dlg.mode_on_ok": "Switch to embedded",
  "navviews.dlg.mode_off_title": "Leave embedded mode?",
  "navviews.dlg.mode_off_text":
    "This Config UI serves its full surface again — including the views Home Assistant also provides.",
  "navviews.dlg.mode_off_ok": "Switch to standalone",
  "navviews.dlg.will_hide": "{views} views and {tabs} settings tabs will be hidden.",
  "navviews.dlg.will_show": "{views} views and {tabs} settings tabs come back.",
  "navviews.dlg.reset_title": "Reset the {profile} profile?",
  "navviews.dlg.reset_text":
    "All {count} deviations in this profile go back to the shipped defaults. Nothing is written until you save.",
  "navviews.dlg.reset_ok": "Reset profile",
  "navviews.warn.alarm_armed":
    "While the alarm system is armed, a hidden panel leaves no way to disarm it from this UI — MQTT, REST and the Home Assistant integration keep working.",
  "navviews.warn.security_faults":
    "Security & Safety faults can only be acknowledged on this view.",
  "navviews.warn.last_ccu_editor":
    "New CCUs can then only be added by editing the config file or calling the REST API.",
  "navviews.why.core": "the device list is what this UI is for.",
  "navviews.why.settings":
    "hiding Settings removes every path back, including to this editor.",
  "navviews.why.editor":
    "this editor would only be repairable through YAML or the REST API.",
  "navviews.why.about":
    "the only in-app statement of version and build — every support request starts there.",
  "navviews.why.identity":
    "in standalone, this is the only place to rotate a password or a token.",
  "navviews.gate.matter": "the Matter bridge is switched off.",
  "navviews.gate.history": "measurement-history recording is switched off.",

  // --- Surface profiles (Settings → Navigation & views) ----------
  // One label + one description per addressable surface. The
  // description says what the view does, not what hiding it means —
  // an operator deciding what to switch off needs to recognise the
  // view first. TestSurfaceCopyIsComplete requires both, in both
  // locales, for every surface in the Go registry.
  "surface.desc.nav.overview": "Tiles for every device, grouped by room.",
  "surface.desc.nav.devices": "The device list and every device detail page.",
  "surface.desc.nav.favorites": "Your starred devices and channels.",
  "surface.desc.nav.alarm": "Arming, zones, sensors and sirens.",
  "surface.desc.nav.security":
    "Smoke, water, tamper and power classes with their fault state.",
  "surface.desc.nav.inbox": "Devices waiting to be taught in, plus install mode.",
  "surface.desc.nav.fleet": "Every configured CCU with its connection state.",
  "surface.desc.nav.programs":
    "CCU programs — run them, enable them, see when they last fired.",
  "surface.desc.nav.sysvars":
    "Read and write CCU system variables, including channel assignment.",
  "surface.desc.nav.groups": "HmIP groups on the CCU and their member devices.",
  "surface.desc.nav.links":
    "Fleet-wide, read-only list of every direct link between channels.",
  "surface.desc.nav.messages":
    "Low battery, unreachable, sabotage — with acknowledgement.",
  "surface.desc.nav.diagnostics":
    "Connection health, throttling, circuit breakers and the RPC recorder.",
  "surface.desc.nav.energy": "Consumption and power across all metering devices.",
  "surface.desc.nav.diagrams": "Recorded measurement curves for any data point.",
  "surface.desc.nav.signal": "RSSI per device, with the weakest links first.",
  "surface.desc.nav.audit":
    "Who changed what, when — configuration and device writes.",
  "surface.desc.nav.logs": "The daemon's live log stream with filters.",
  "surface.desc.nav.matter":
    "Bridge Homematic devices to Apple Home, Google Home or Alexa.",
  "surface.desc.nav.firmware":
    "Available device firmware updates and their rollout state.",
  "surface.desc.nav.backups":
    "CCU and daemon backups — create, download, restore.",
  "surface.desc.nav.settings": "Everything in this section.",
  "surface.desc.nav.about": "Version, build, add-on stamp and licence information.",

  "surface.desc.settings.general":
    "Locale, log level and the daemon's own identity.",
  "surface.desc.settings.system": "Restart, update and runtime information.",
  "surface.desc.settings.navviews": "This editor.",
  "surface.desc.settings.changes":
    "Config fields that differ from the running boot configuration.",
  "surface.desc.settings.mqtt":
    "Broker connection, topic layout and Home Assistant discovery.",
  "surface.desc.settings.matter":
    "Matter bridge, commissioning and paired controllers.",
  "surface.desc.settings.mcp":
    "The Model Context Protocol server and its write tools.",
  "surface.desc.settings.rest": "Listener, TLS and CORS for the northbound API.",
  "surface.desc.settings.discovery":
    "How this daemon announces itself on the network.",
  "surface.desc.settings.ccus":
    "Add, edit and discover the CCUs this daemon talks to.",
  "surface.desc.settings.callback": "XML-RPC and BIN-RPC callback ports.",
  "surface.desc.settings.oidc":
    "Single sign-on through an external identity provider.",
  "surface.desc.settings.ccu_auth":
    "Credentials this daemon uses against the CCU.",
  "surface.desc.settings.users": "Local user accounts and their roles.",
  "surface.desc.settings.groups":
    "The CCU's rooms and functions, and which channels belong to them.",
  "surface.desc.settings.tokens": "Long-lived tokens for machine clients.",
  "surface.desc.settings.visibility":
    "Which data points are suppressed on the northbound planes.",
  "surface.desc.settings.reliability":
    "Retry, throttle and circuit-breaker tuning.",
  "surface.desc.settings.persistence":
    "Database location, retention and vacuum schedule.",

  "surface.desc.device.overview":
    "Live values and controls for the selected device.",
  "surface.desc.device.configure":
    "The whole configuration tab, including its sub-tabs.",
  "surface.desc.device.configure.device-config":
    "MASTER and VALUES paramsets with edit sessions and undo.",
  "surface.desc.device.configure.channels":
    "The channel strip that selects which channel the editor shows.",
  "surface.desc.device.configure.links":
    "Create and delete links between this device and others.",
  "surface.desc.device.configure.schedule":
    "Weekly climate and switching programs.",
  "surface.desc.device.history":
    "The recorded curve of any parameter of this device.",

  "groups.central_label": "Central",
  "groups.created": "Created.",
  "groups.deleted": "Removed.",
  "groups.delete_function_confirm": "Remove function?",
  "groups.delete_room_confirm": "Remove room?",
  "groups.empty_functions": "No functions configured.",
  "groups.empty_rooms": "No rooms configured.",
  "groups.function_placeholder": "Function name…",
  "groups.functions_title": "Functions",
  "groups.rename": "Rename",
  "groups.renamed": "Renamed.",
  "groups.room_placeholder": "Room name…",
  "groups.rooms_title": "Rooms",
  // --- RoomsFunctions table column labels ---
  "roomsfn.col.name": "Name",
  "roomsfn.col.count": "Channels",
  "roomsfn.col.actions": "Actions",
  "changes.revert": "Revert",
  "changes.revert_confirm":
    "Discard your value for “{field}” and fall back to the built-in default? This cannot be undone here.",
  "changes.reverted": "Field reset to default",
  "changes.empty": "No changed settings — everything is at its default.",
  "changes.n_entries": "{count} entries",
  "changes.manage_ccus": "Manage on CCUs",
  "changes.not_revertible": "Not revertible here",
  "changes.intro": "Settings you have overridden. Revert any to its default.",
  "settings.restart_later": "Later",
  "settings.reset_confirm":
    "Remove the persisted override for this section? The daemon will revert to its built-in defaults on the next restart.",
  "settings.reset_done": "Section reset to built-in defaults.",
  "restart.banner_text":
    "Configuration changes need a daemon restart to take effect.",
  "restart.now": "Restart now",
  "settings.json_parse_error": "Invalid JSON — check syntax.",
  "settings.duration_parse_error":
    "Invalid duration. Use Go syntax: 60s, 5m, 250ms, 1h30m.",
  "settings.tristate.default": "Default",
  "settings.tristate.on": "On",
  "settings.tristate.off": "Off",
  // --- Roles (shared by the users and tokens settings tabs) ---
  "role.viewer": "Viewer",
  "role.operator": "Operator",
  "role.admin": "Admin",
  // --- Users admin ---
  "users.empty": "No users configured.",
  "users.add": "Add user",
  "users.add_title": "Add user",
  "users.edit_title": "Edit user",
  "users.password_leave_blank": "Leave blank to keep current",
  "users.degraded_note":
    "The live user store is not available. Users shown are from the bootstrap list and cannot be edited here. Manage users via config.yaml.",
  "users.created": "User created.",
  "users.deleted": "User removed.",
  "users.password_changed": "Password changed.",
  "users.role_changed": "Role updated.",
  "users.last_admin_error": "Cannot remove the last admin.",
  "users.last_admin_demote_error": "Cannot demote the last admin.",
  "users.exists_error": "A user with this name already exists.",
  "users.new_password": "New password",
  "users.password": "Password",
  "users.confirm_delete_title": "Remove user?",
  "users.confirm_delete_body":
    'Remove user "{subject}"? This action cannot be undone.',
  "users.col.subject": "Username",
  "users.col.role": "Role",
  "users.col.created": "Created",
  "users.col.last_seen": "Last seen",
  "users.col.actions": "Actions",
  // --- Tokens admin ---
  "tokens.empty": "No API tokens.",
  "tokens.create": "Create token",
  "tokens.create_title": "Create API token",
  "tokens.revoke": "Revoke",
  "tokens.revoked": "Token revoked.",
  "tokens.reveal_title": "Token created",
  "tokens.reveal_warning": "This token will not be shown again. Copy it now.",
  "tokens.copied": "Copied!",
  "tokens.copy_failed":
    "Copy failed — the clipboard needs a secure (HTTPS) context. The token is selected so you can copy it manually.",
  "tokens.confirm_revoke_title": "Revoke token?",
  "tokens.confirm_revoke_body":
    "Revoke token {fingerprint}? Any client using it will lose access immediately.",
  "tokens.col.subject": "Subject",
  "tokens.col.role": "Role",
  "tokens.col.fingerprint": "Fingerprint",
  "tokens.col.created": "Created",
  "tokens.col.last_seen": "Last seen",
  "tokens.col.actions": "Actions",
  // --- Discovery ---
  "discovery.add": "Add",
  "discovery.already_configured": "Already configured",
  "discovery.empty": "No CCUs found on the network.",
  "discovery.found_hint":
    "CCUs found on your network via SSDP — click Add to prefill the form.",
  "discovery.ignore": "Ignore",
  "discovery.ignore_confirm":
    'Ignore "{name}" ({serial})? It will no longer appear in the discovered list.',
  "discovery.ignored": '"{name}" ignored.',
  "discovery.refresh": "Refresh",
  "discovery.title": "Discovered CCUs",
  // --- Centrals admin ---
  "centrals.empty": "No CCUs configured.",
  "centrals.add": "Add CCU",
  "centrals.col.name": "Name",
  "centrals.col.host": "Host",
  "centrals.col.status": "Status",
  "centrals.col.actions": "Actions",
  "centrals.add_title": "Add CCU",
  "centrals.edit_title": "Edit CCU",
  "centrals.created": "CCU added.",
  "centrals.updated": "CCU updated.",
  "centrals.updated_restart_required":
    "CCU settings saved. A daemon restart is required to apply them to the running connection.",
  "centrals.deleted": "CCU removed.",
  "centrals.enabled": "CCU enabled.",
  "centrals.disabled": "CCU disabled.",
  "centrals.confirm_delete_title": "Remove CCU?",
  "centrals.confirm_delete_body":
    'Remove CCU "{name}"? Devices managed by this CCU will become unavailable.',
  "centrals.field.name": "Name",
  "centrals.field.name_hint": "Letters, digits, - and _ only — the name becomes part of the callback URL the CCU pushes events to.",
  "centrals.field.host": "Host",
  "centrals.field.interfaces": "Interfaces",
  "centrals.field.port": "Port",
  "centrals.field.port_hint":
    "Leave the port empty to use the default. Override only when the CCU exposes a non-standard port.",
  "centrals.field.json_rpc_port": "JSON-RPC port",
  "centrals.field.json_rpc_port_hint":
    "CCU web/ReGa port for JSON-RPC. Empty uses the default (80, or 443 with TLS). Override for a non-standard CCU HTTP port.",
  "centrals.field.primary_interface": "Primary interface",
  "centrals.behavior.title": "Advanced behaviour",
  "centrals.behavior.light_last_brightness":
    "Restore last brightness on light turn-on",
  "centrals.behavior.use_group_channel_for_cover_state":
    "Report cover position from the group channel",
  "centrals.behavior.enable_sysvar_scan": "Scan system variables",
  "centrals.behavior.enable_program_scan": "Scan programs",
  "centrals.behavior.include_internal_sysvars":
    "Include internal system variables",
  "centrals.behavior.include_internal_programs": "Include internal programs",
  "centrals.behavior.enable_device_firmware_check":
    "Surface device firmware-update entities",
  "centrals.behavior.delay_new_device_creation":
    "Defer new-device creation to the inbox",
  "centrals.behavior.sysvar_scan_interval":
    "System-variable scan interval (seconds, 0 = default 30, minimum 3)",
  "centrals.behavior.sysvar_markers": "System-variable markers",
  "centrals.behavior.program_markers": "Program markers",
  "centrals.behavior.markers_hint":
    "Markers are text tokens you write into the entry's description in the CCU WebUI. They do not decide what is imported — everything the CCU exposes is imported. They decide how an entry arrives: an entry matching a marker you tick arrives enabled, everything else arrives disabled and is switched on individually.",
  "centrals.behavior.marker.hahm":
    "Makes the system variable writable — it arrives as a switch, select, number or text field instead of a read-only sensor.",
  "centrals.behavior.marker.hx": "Free marker for your own filtering.",
  "centrals.behavior.marker.internal.sysvar":
    "Additionally delivers the CCU's internal variables — the ones it maintains for itself rather than ones you created.",
  "centrals.behavior.marker.internal.program":
    "Additionally delivers the CCU's internal programs. This matters more than it sounds: the CCU flags most ordinary user programs as internal, so without this the program list stays almost empty.",
  "centrals.field.username": "Username",
  "centrals.field.password": "Password",
  "centrals.field.password_hint":
    "Stored in the daemon's SQLite database (file mode 0600). Backup tarballs redact it unless --include-secrets is passed.",
  "centrals.field.password_hint_unchanged":
    "A password is stored. Leave blank to keep it — type a new one to replace it.",
  "centrals.field.password_placeholder_env": "(resolved from env variable)",
  "centrals.field.password_env": "Password env-var (override)",
  "centrals.field.password_env_hint":
    "Optional. Name of an environment variable; when set, it overrides the password field above. Use for Kubernetes / Vault / systemd-creds workflows. See README → Secrets.",
  "centrals.field.tls_insecure": "Skip TLS verification",
  "centrals.field.tls_insecure_warn":
    "Disables certificate chain + hostname checks. Use only against CCUs with self-signed certificates on a trusted network.",
  "centrals.error.no_interface": "Pick at least one interface.",
  "centrals.error.invalid_name": "The name may only contain letters, digits, - and _.",
  "sysvars.title": "System variables",
  "sysvars.empty": "No variables.",
  "sysvars.col.name": "Name",
  "sysvars.col.type": "Type",
  "sysvars.col.value": "Value",
  "sysvars.col.actions": "Actions",
  "sysvars.create.title": "New variable",
  "sysvars.create.name": "Name",
  "sysvars.create.type": "Type",
  "sysvars.create.unit": "Unit",
  "sysvars.create.values": "Values (semicolon-separated)",
  "sysvars.create.alarm_hint":
    "Creates a binary, acknowledgeable alarm line on the CCU.",
  "sysvars.edit.title": "Edit",
  "sysvars.edit.name": "Name (rename)",
  "sysvars.edit.description": "Description",
  "sysvars.edit.note":
    "Type changes require delete + recreate. This dialog only updates metadata.",
  "sysvars.edit.bound_required":
    "A numeric system variable always has both bounds on the CCU — they can be changed, but not removed. Enter a value for the minimum and the maximum.",
  "sysvars.confirm_remove": 'Really remove sysvar "{name}"?',
  "sysvars.usage.warning":
    "Warning: {count} program(s) reference this variable and will be affected by deletion:",
  "sysvars.usage.internal": "internal",
  "sysvars.removed": "{name} removed.",
  "sysvars.created": "Sysvar created.",
  "sysvars.updated": "{name} updated.",
  "sysvars.saved": "{name} saved.",
  "sysvars.count": "{count} variables",
  "sysvars.edit.tooltip": "Edit metadata",
  "sysvars.remove.tooltip": "Remove variable",
  "sysvars.labels.title": "Value labels",
  "sysvars.labels.value0": "Label for “false”",
  "sysvars.labels.value1": "Label for “true”",
  "sysvars.labels.hint":
    "Operator-visible text for each state of a binary (BOOL/ALARM) variable.",
  "sysvars.flags.visible": "Visible in CCU WebUI",
  "sysvars.flags.logged": "Log value changes",
  "sysvars.channel.label": "Channel assignment",
  "sysvars.channel.hint":
    "Bind the variable to a device channel (the CCU “Kanalzuordnung”). Optional — leave unassigned to attach it to the hub.",
  "sysvars.channel.none": "Not assigned",
  "sysvars.channel.clear": "Clear",
  "sysvars.channel.search": "Search device…",
  "sysvars.channel.no_devices": "No devices",
  "sysvars.channel.load_failed": "Could not load channels",
  // --- DeviceDetail ---
  "device.tab.control": "Control",
  "device.tab.values": "Values",
  "device.tab.master": "Configuration",
  "device.tab.links": "Links",
  "device.tab.schedule": "Schedule",
  "device.no_channels": "This device has no channels.",
  "device.all_devices": "All devices",
  "device.offline": "offline",
  "device.update_available": "update available",
  "device.firmware_update": "Update firmware",
  "device.firmware_update.tooltip":
    "Trigger firmware update ({current} → {available})",
  "device.firmware_triggered": "Firmware update triggered.",
  "device.confirm_remove":
    'Really remove device "{name}"?\n\nThe CCU pairing will be dropped.',
  "device.confirm_firmware":
    'Trigger firmware update for "{name}"? The device will be briefly unreachable during the update.',
  "device.removed": "Device removed.",
  "device.renamed": "Device renamed.",
  "device.rename_include_channels": "Rename channels along",
  "channel.rename": "Rename channel",
  "channel.renamed": "Channel renamed.",
  "channel.rooms": "Rooms",
  "channel.functions": "Functions",
  "channel.rooms_updated": "Channel rooms updated.",
  "channel.functions_updated": "Channel functions updated.",
  "remote.key_grid_title": "Key simulation",
  "remote.key_n": "Key {n}",
  "remote.press_short": "Short",
  "remote.press_long": "Long",
  "remote.press_short_aria": "Press key {n} short",
  "remote.press_long_aria": "Press key {n} long",
  "remote.press_short_title": "{title}: short press",
  "remote.press_long_title": "{title}: long press",
  "remote.press_failed": "Key press failed",
  "remote.press_just_now": "{kind} just now",
  "remote.press_ago_sec": "{kind} {n} s ago",
  "remote.press_ago_min": "{kind} {n} min ago",
  "remote.press_ago_hour": "{kind} {n} h ago",
  "remote.press_ago_day": "{kind} {n} d ago",
  "device.rooms_updated": "Rooms updated.",
  "device.functions_updated": "Functions updated.",
  "device.rooms": "Rooms",
  "device.functions": "Functions",
  "device.rooms.placeholder": "Living room, kitchen, …",
  "device.functions.placeholder": "Lighting, heating, …",
  "roomfn.placeholder.room": "Search or add room…",
  "roomfn.placeholder.function": "Search or add function…",
  "roomfn.remove": "Remove",
  "roomfn.remove_named": "Remove {name}",
  "roomfn.no_matches": "No matches",
  "roomfn.create": "+ Create “{name}”",
  "roomfn.create.room": "+ Create room “{name}”",
  "roomfn.create.function": "+ Create function “{name}”",
  "roomfn.created.room": "Room created.",
  "roomfn.created.function": "Function created.",
  "device.rename": "Rename",
  "device.remove": "Remove",
  "device.channel_n": "Channel {n}",
  "device.error_label": "Error: {message}",
  "device.export_definition": "Export definition",
  "device.export_definition_success": "Definition downloaded.",
  "device.export_definition_error": "Export failed.",
  // --- Channel paramset / ChannelPanel ---
  "channel.loading_schema": "Loading schema…",
  "channel.schema_failed": "Schema could not be loaded",
  "channel.snapshot_downloaded": "Snapshot downloaded.",
  "channel.session_lock_other":
    "Another session currently holds the edit lock for this view. Saves may fail until the lock expires.",
  "channel.take_over": "Take over",
  "channel.take_over_failed": "Take-over failed",
  "channel.lock_lost": "Edit lock lost",
  "channel.lock_lost_detail":
    "Another session took over the edit lock, or your lock expired. Re-open this editor before saving so you don't overwrite concurrent changes.",
  "channel.tab.common": "General",
  "channel.tab.short": "Short keypress",
  "channel.tab.long": "Long keypress",
  // --- QuickControlTab ---
  "quick.on": "On",
  "quick.off": "Off",
  // --- Programs ---
  "programs.toggle.tooltip": "Toggle enabled",
  "programs.executed": 'Program "{name}" executed.',
  "programs.not_executed": 'Program "{name}" not executed — condition not met.',
  "programs.check_conditions": "Only run when the condition is met",
  "programs.toggle_done": 'Program "{name}" {state}.',
  "programs.enabled": "enabled",
  "programs.disabled": "disabled",
  "programs.active": "active",
  "programs.col.name": "Program",
  "programs.col.status": "Status",
  "programs.col.condition": "Condition",
  "programs.col.activity": "Activity",
  "programs.col.last_executed": "Last executed",
  "programs.col.actions": "Actions",
  "programs.never_executed": "never",
  "programs.inactive": "inactive",
  "programs.count": "{count} programs",
  "programs.confirm_run": 'Execute program "{name}"?',
  "programs.confirm_delete": 'Delete program "{name}"? This cannot be undone.',
  "programs.deleted": 'Program "{name}" deleted.',
  "programs.delete.tooltip": "Delete program from the CCU",
  "programs.show_internal": "Show system programs",
  // --- Direct Links ---
  "schedules.title": "Schedules",
  "schedules.subtitle": "Every device with a week schedule, across all CCUs.",
  "schedules.empty": "No device has a week schedule.",
  "schedules.empty.description":
    "Thermostats, and switches or covers with a week-profile channel, appear here once they are taught in.",
  "schedules.no_matches": "No device matches the search.",
  "schedules.search": "Search by name, address or model…",
  "schedules.kind.climate": "Thermostat",
  "schedules.kind.week_profile": "Week profile",
  "schedules.editor_hidden":
    "The schedule editor is hidden in this profile (Settings → Navigation & views). The overview stays; its entries do not link.",
  "surface.desc.nav.schedules":
    "Every device that has a week schedule, with a link to its editor.",
  "links.title": "Direct links",
  "links.empty": "No direct links.",
  "links.add": "Add link",
  "links.remove": "Remove",
  "links.removed": "Link removed.",
  "links.sender": "Sender",
  "links.receiver": "Receiver",
  "links.name": "Name",
  "links.search": "Search by name, sender, receiver…",
  "links.count": "{count} links",
  "links.empty.description":
    "Direct links let two channels talk to each other without the CCU program. None exist yet.",
  "links.no_matches": "No links match the search.",
  "links.central": "CCU",
  "links.edit_on_device": "Edit on device",
  "links.editor_hidden":
    "The link editor is hidden in this profile (Settings → Navigation & views). The overview stays; its entries do not link.",
  "profile.test.short": "Test (short press)",
  "profile.test.long": "Test (long press)",
  "links.test.ok": "Link triggered on the device.",
  "links.test.error": "Could not trigger the link.",
  "links.test.unsupported": "This interface does not support link testing.",
  "links.test.confirm_title": "Test link at the device?",
  "links.test.confirm_body":
    "This physically triggers the receiver (a switch clicks, a blind moves) as if the sender fired. Continue?",
  // --- Central links ---
  "central.title": "Press events to central",
  "central.subtitle":
    "Controls whether the CCU forwards press events (PRESS_SHORT/LONG) to OpenCCU-Loom.",
  "central.help.summary":
    "Why a button seems to do nothing, and what enabling costs",
  "central.help.no_link":
    "Without forwarding enabled, many HmIP buttons never send their press events to the CCU or OpenCCU-Loom — this is the most common reason a button appears to do nothing.",
  "central.help.duty_cycle":
    "Enabling forwarding creates an internal link that raises the device's radio duty cycle and battery consumption.",
  "central.unsupported":
    "This interface has no concept of central event routing.",
  "central.eligible": "press channels",
  "central.active": "Active",
  "central.inactive": "Inactive",
  "central.active_count": "{count} active",
  "central.enable": "Enable",
  "central.disable": "Disable",
  "central.unsupported_badge": "unsupported",
  "central.report.enabled":
    "Enabled: {touched} channel(s), {skipped} skipped, {failed} failed.",
  "central.report.disabled":
    "Disabled: {touched} channel(s), {skipped} skipped, {failed} failed.",
  "central.device_wide": "Whole device",
  "central.per_channel": "Per channel",
  "central.channel_label": "Channel {number}",
  "central.confirm.enable_title": "Enable press-event forwarding?",
  "central.confirm.enable_body":
    "The CCU will forward this channel's press events (PRESS_SHORT/PRESS_LONG) to OpenCCU-Loom and to CCU-side programs. This raises the device's radio duty cycle and battery use.",
  "central.confirm.disable_title": "Disable press-event forwarding?",
  "central.confirm.disable_body":
    "CCU-side programs may rely on these press events. After disabling, neither CCU programs nor OpenCCU-Loom will receive press events from this channel.",
  "central.action_failed": "Could not change press-event forwarding",
  // --- Schedules ---
  "schedule.loading": "Loading schedule…",
  "schedule.unsupported": "This device does not support a schedule.",
  "schedule.unsupported_channel": "This channel has no climate schedule.",
  "schedule.profile_active": "Active profile: {profile}",
  "schedule.base_temperature": "Base",
  "schedule.weekday_overview": "Weekly overview",
  "schedule.click_to_edit": "Click a day to edit",
  "schedule.astro": "Astro",
  "schedule.condition": "Condition",
  "schedule.duration": "Duration",
  "schedule.ramp_time": "Ramp time",
  "schedule.color": "Colour",
  "schedule.color.hue_saturation": "Colour (hue/saturation)",
  "schedule.color.temperature": "Colour temperature",
  "schedule.color.effect": "Effect",
  "schedule.target_channels": "Target channels",
  "schedule.level": "Level",
  "schedule.simple_title": "Switching schedule",
  "schedule.slots_count": "{count} / {max} slots",
  "schedule.add_slot": "+ Slot",
  "schedule.empty_slots": "No switching slots yet — click '+' to add one.",
  "schedule.max_reached": "Maximum of {max} entries reached.",
  "schedule.weekday_select_one": "Slot {n}: select at least one weekday.",
  "schedule.invalid_time": "Slot {n}: invalid time {time}.",
  "schedule.saved_toast": "Schedule saved.",
  "schedule.save_failed": "Save failed",
  "schedule.cover.position": "Position",
  "schedule.cover.slat": "Slat",
  "schedule.lock.mode": "Mode",
  "schedule.lock.action": "Action",
  "schedule.lock.permission": "Permission",
  "schedule.lock.door_lock": "Door lock",
  "schedule.lock.user_permission": "User permission",
  "schedule.lock.action.lock_autorelock_end": "Lock + auto-relock end",
  "schedule.lock.action.lock_autorelock_start": "Lock + auto-relock start",
  "schedule.lock.action.unlock_autorelock_end": "Unlock + auto-relock end",
  "schedule.lock.action.autorelock_end": "Auto-relock end",
  "schedule.lock.granted": "Granted",
  "schedule.lock.not_granted": "Not granted",
  "schedule.astro.sunrise": "Sunrise",
  "schedule.astro.sunset": "Sunset",
  "schedule.astro.offset": "Offset",
  "schedule.advanced": "Advanced",
  "schedule.cond.fixed_time": "Fixed time",
  "schedule.cond.astro": "Astro",
  "schedule.cond.fixed_if_before_astro": "Fixed if before astro",
  "schedule.cond.astro_if_before_fixed": "Astro if before fixed",
  "schedule.cond.fixed_if_after_astro": "Fixed if after astro",
  "schedule.cond.astro_if_after_fixed": "Astro if after fixed",
  "schedule.cond.earliest_of_fixed_and_astro": "Earliest of fixed and astro",
  "schedule.cond.latest_of_fixed_and_astro": "Latest of fixed and astro",
  "schedule.viz.aria": "Weekly schedule preview",
  "schedule.entry.level": "Level",
  "schedule.targets.all_default": "All (CCU default)",
  "schedule.targets.selected": "{count} selected",
  "schedule.targets.all": "All",
  "schedule.targets.none": "None",
  "weekday.short.MONDAY": "Mon",
  "weekday.short.TUESDAY": "Tue",
  "weekday.short.WEDNESDAY": "Wed",
  "weekday.short.THURSDAY": "Thu",
  "weekday.short.FRIDAY": "Fri",
  "weekday.short.SATURDAY": "Sat",
  "weekday.short.SUNDAY": "Sun",
  "weekday.long.MONDAY": "Monday",
  "weekday.long.TUESDAY": "Tuesday",
  "weekday.long.WEDNESDAY": "Wednesday",
  "weekday.long.THURSDAY": "Thursday",
  "weekday.long.FRIDAY": "Friday",
  "weekday.long.SATURDAY": "Saturday",
  "weekday.long.SUNDAY": "Sunday",
  "climate.base_label": "Base",
  "climate.add_period": "+ period",
  "climate.all_day": "All day at base temperature {temp} °C",
  "climate.day_copied": "Copied ({count} periods).",
  "climate.day_pasted": "Pasted into {day}.",
  "climate.fill_all_done": "Monday applied to every day.",
  "climate.fill_all": "Mo → All",
  "climate.fill_all.tooltip": "Apply Monday to all days",
  "climate.set_active": "Set active",
  "climate.set_active_failed": "Could not activate the profile",
  "climate.profile_active_badge": "active",
  "channel.save_failed": "Save failed",
  "channel.import_failed": "Import failed",
  "channel.kanal": "Channel {n}",
  "channel.action_triggered": "Action {name} triggered.",
  "channel.action_failed": "Action {name} failed",
  "channel.profile_staged": "Profile staged — press Save to apply.",
  "channel.import_staged": "Import staged — press Save to apply.",
  "channel.import_paramset_mismatch":
    "Paramset mismatch: snapshot={snapshot}, current={current}.",
  "channel.import_invalid_file": "Not a valid OpenCCU-Loom export.",
  "channel.import_cross_channel_confirm":
    "Snapshot is from {snapshot}. Apply to {current} anyway?",
  "channel.lock_count": "{count} parameters locked by profile.",
  "channel.unlock_label": "Unlock",
  "channel.advanced_label":
    "Show advanced parameters (jump targets, conditions)",
  "channel.expert_label":
    "Expert mode (show all parameters, including untranslated)",
  "channel.no_params_in_group": "No parameters in this group.",
  "channel.other": "Other",
  "channel.cross_validation_error":
    "Conflicting values — please correct, then save again.",
  "channel.export": "Export",
  "channel.import": "Import",
  "channel.export.tooltip": "Download current values as JSON",
  "channel.import.tooltip": "Load values from JSON file",
  "channel.undo.tooltip": "Undo (Ctrl+Z)",
  "channel.redo.tooltip": "Redo (Ctrl+Y)",
  "channel.save_n": "Save ({count})",
  "channel.unsaved": "Unsaved changes",
  "channel.saved_short": "Saved.",
  // --- Secured transmission (channel/SecureTransmission.svelte) ---
  "channel.flags.hidden.title": "Hide channel",
  "channel.flags.hidden.help":
    "Removes this channel from the operation surfaces (entity list, MQTT, Matter). It stays visible here so you can unhide it.",
  "channel.flags.locked.title": "Lock operation",
  "channel.flags.locked.help":
    "Blocks control writes to this channel. Reads and configuration are unaffected.",
  "channel.flags.saved_toast": "Channel overrides saved",
  "channel.flags.failed": "Could not save channel overrides",
  "channel.secure_transmission.title": "Secured transmission",
  "channel.secure_transmission.help":
    "Sign this channel's radio telegrams (AES). Raises security but also the channel's radio load and, on battery devices, battery drain.",
  "channel.secure_transmission.confirm_title": "Enable secured transmission?",
  "channel.secure_transmission.confirm_body":
    "Secured (AES-signed) transmission adds an acknowledgement round-trip to every command, which increases this channel's radio load and — on battery devices — battery drain. Enable it anyway?",
  "channel.secure_transmission.enable": "Enable",
  "channel.secure_transmission.enabled_toast": "Secured transmission enabled.",
  "channel.secure_transmission.disabled_toast":
    "Secured transmission disabled.",
  "channel.secure_transmission.failed": "Could not change transmission mode.",
  // --- Motion-detector brightness helper (channel/brightness-helper.ts) ---
  "channel.brightness.apply": "Use brightness {value}",
  "channel.brightness.apply_tooltip":
    "Take the motion sender's current brightness ({value}) as this threshold.",
  // --- DST sub-group headers (channel/dst-groups.ts) ---
  "channel.dst.start_header": "Start of daylight saving time",
  "channel.dst.end_header": "End of daylight saving time",
  // --- Measurement history (device "Verlauf" tab) ---
  "history.chart_title": "Measurement history — {name}",
  "history.label_channel": "Channel:",
  "history.label_parameter": "Parameter:",
  "history.channel_n": "Channel {n}",
  "history.loading_parameters": "Loading parameters…",
  "history.no_numeric": "No numeric parameters on this channel.",
  "history.record_label": "Record",
  "history.record_saved": "Recording preference saved.",
  "history.record_reset": "Reset to default",
  "history.record_reset_done": "Reverted to the recording policy.",
  "history.record_error": "Could not change recording: {error}",
  "history.reload": "Reload",
  "history.empty": "No recorded samples in this time range.",
  "history.disabled_title": "History recording is off",
  "history.disabled_hint":
    "Turn it on under Settings → Persistence to chart this value.",
  "history.enable_link": "Open settings",
  // --- Energy view (GET /api/v1/energy) ---
  "energy.title": "Energy",
  "energy.subtitle":
    "Consumption and feed-in per device, aggregated over time.",
  "energy.central": "Central",
  "energy.group": "Group by",
  "energy.group.hour": "Hour",
  "energy.group.day": "Day",
  "energy.group.month": "Month",
  "energy.range": "Range",
  "energy.preset.24h": "24 h",
  "energy.preset.7d": "7 d",
  "energy.preset.30d": "30 d",
  "energy.preset.12mo": "12 mo",
  "energy.no_centrals": "No CCUs configured yet.",
  "energy.total_consumed": "Total consumed",
  "energy.total_feed_in": "Total feed-in",
  "energy.chart_title": "Consumption over time",
  "energy.chart.all_devices": "All devices",
  "energy.breakdown_title": "Per-device breakdown",
  "energy.col.device": "Device",
  "energy.col.consumed": "Consumed",
  "energy.col.feed_in": "Feed-in",
  "energy.col.avg_power": "Avg. power",
  "energy.col.cost": "Cost",
  "energy.col.peak_power": "Peak power",
  "energy.col.reset": "reset",
  "energy.reset_note":
    "A meter reset occurred for at least one device in this range — the affected bucket reports the counter value since the reset, not a negative delta.",
  "energy.empty": "No energy devices recorded data in this range.",
  "energy.disabled_title": "History recording is off",
  "energy.disabled_hint":
    "Turn it on under Settings → Persistence to see energy data.",
  "energy.enable_link": "Open settings",
  "links.add.create": "Create",
  "links.add.creating": "Creating…",
  "links.add.title2": "New link",
  "links.add.step1": "Step 1 — pick a channel of this device to link from.",
  "links.add.step2": "Step 2 — choose the role and pick a peer channel.",
  "links.add.step3": "Step 3 — review the mapping and confirm.",
  "links.add.loading_channels": "Loading channels…",
  "links.add.no_linkable": "No linkable channels available.",
  "links.add.role": "Role",
  "links.add.search_peers": "Search device, model, or channel…",
  "links.add.loading_peers": "Loading peers…",
  "links.add.no_peer_matches": "No matches for the search.",
  "links.add.no_compatible": "No compatible channels found.",
  "links.add.back": "Back",
  "links.add.next": "Next",
  "links.add.name_optional": "Name (optional)",
  "links.add.desc_optional": "Description (optional)",
  "links.add.aria_progress": "Progress",
  "links.config.back_to_list": "Back to links",
  "links.config.receiver_section": "Receiver configuration",
  "links.config.sender_section": "Sender configuration",
  "links.created": "Link created.",
  "links.wakeup_pending.title": "Saved — pending device wakeup",
  "links.wakeup_pending.body":
    "This is a battery-powered device. The change is queued and only transfers the next time the device wakes up (e.g. on a button press).",
  "links.removal_failed": "Removal failed",
  "links.no_for_device": "No direct links for this device.",
  "links.configure": "Configure",
  "links.direction": "Direction",
  "links.outgoing_label": "Outgoing",
  "links.incoming_label": "Incoming",
  "links.rename": "Rename",
  "links.rename.title": "Rename link",
  "links.rename.name": "Name",
  "links.rename.description": "Description",
  "links.rename.name_placeholder": "Link name",
  "links.rename.description_placeholder": "Optional description",
  "links.rename.saving": "Saving…",
  "links.renamed": "Link renamed.",
  "links.rename_failed": "Rename failed",
  "links.confirm_delete": "Really delete the link {sender} → {receiver}?",
  "links.links_label": "links",
  "common.sort": "Sort:",
  "common.no_matches": "No matches.",
  "shortcut.help_open": "Show this help",
  "shortcut.title": "Keyboard shortcuts",
  "shortcut.group.general": "General",
  "shortcut.group.editor": "Parameter editor",
  "shortcut.close_dialog": "Close dialog",
  "shortcut.undo": "Undo",
  "shortcut.redo": "Redo",
  "connection.reconnecting": "reconnecting…",
  "connection.daemon_stopping": "daemon stopping",
  "connection.live_on": "Live",
  "connection.live_off": "Live offline",
  "connection.tooltip.on": "Live connection active — changes appear instantly.",
  "connection.tooltip.off":
    "Live connection lost — values no longer update automatically. The connection is restored automatically.",
  "connection.tooltip.connecting": "Establishing live connection…",
  "connection.tooltip.daemon_stopping":
    "The daemon announced that it is shutting down. This is not a network problem — live updates return once it is running again.",
  "connection.events": "events",
  "connection.last": "last",
  "session.unsaved": "Unsaved changes",
  "session.idle":
    "Idle for a while. Save within {time} or your edits will be lost on reload.",
  "session.dismiss": "Dismiss",
  "app.menu": "Menu",
  "app.close_menu": "Close menu",
  "app.skip_to_content": "Skip to content",
  "app.switch_language": "Switch language",
  "page.title.default": "OpenCCU-Loom",
  "page.title.alarm": "Alarm system — OpenCCU-Loom",
  "page.title.security": "Security & Safety — OpenCCU-Loom",
  "page.title.devices": "Devices — OpenCCU-Loom",
  "page.title.overview": "Overview — OpenCCU-Loom",
  "page.title.diagnostics": "Diagnostics — OpenCCU-Loom",
  "page.title.energy": "Energy — OpenCCU-Loom",
  "page.title.fleet": "Fleet — OpenCCU-Loom",
  "page.title.groups": "Heating groups — OpenCCU-Loom",
  "page.title.links": "Direct links — OpenCCU-Loom",
  "page.title.schedules": "Schedules — OpenCCU-Loom",
  "page.title.diagrams": "Diagrams — OpenCCU-Loom",
  "diagrams.title": "Diagrams",
  "diagrams.subtitle": "Named multi-series measurement charts",
  "diagrams.new": "New diagram",
  "diagrams.edit": "Edit diagram",
  "diagrams.empty": "No diagrams yet.",
  "diagrams.empty.description":
    "Create a diagram to chart several data points together over time.",
  "diagrams.saved": "Diagram saved.",
  "diagrams.deleted": "Diagram deleted.",
  "diagrams.field.name": "Name",
  "diagrams.field.visibility": "Visibility",
  "diagrams.field.series": "Series",
  "diagrams.visibility.private": "Private",
  "diagrams.visibility.shared": "Shared",
  "diagrams.series.label": "Label (optional)",
  "diagrams.series.add": "Add series",
  "diagrams.series.remove": "Remove",
  "diagrams.picker.series": "Series",
  "diagrams.picker.device": "Device",
  "diagrams.picker.search": "Search device…",
  "diagrams.picker.no_devices": "No devices",
  "diagrams.picker.channel": "Channel",
  "diagrams.picker.channel_none": "Select a channel…",
  "diagrams.picker.value": "Value",
  "diagrams.picker.param_none": "Select a value…",
  "diagrams.picker.label": "Label (optional)",
  "diagrams.picker.channels_failed": "Could not load channels",
  "diagrams.picker.params_failed": "Could not load values",
  "diagrams.delete.confirm_title": 'Delete diagram "{name}"?',
  "diagrams.error.name_required": "A name is required.",
  "diagrams.error.series_required": "Add at least one series with a central.",
  "diagrams.error.save": "Could not save the diagram.",
  "diagrams.error.delete": "Could not delete the diagram.",
  "diagrams.chart.empty": "No recorded samples in this range.",
  "diagrams.chart.aria": "Multi-series measurement chart",
  "diagrams.chart.history_off": "history off",
  "diagrams.chart.series_error": "unavailable",
  "diagrams.chart.no_samples": "no samples",
  "diagrams.history_required": "History recording is off.",
  "diagrams.history_required.description":
    "Diagrams chart recorded measurement history. Enable history recording in settings to use them.",
  "page.title.logs": "Logs — OpenCCU-Loom",
  "page.title.settings": "Settings — OpenCCU-Loom",
  "page.title.about": "About — OpenCCU-Loom",
  // --- Profile selector ---
  "profile.header": "Profile",
  "profile.detected": "active profile detected",
  "profile.placeholder": "Select profile",
  "profile.apply": "Apply",
  "profile.preview_label": "Preview:",
  "profile.preview.matching": "matching",
  "profile.preview.will_change": "will change",
  "profile.preview.conflict": "conflict",
  "profile.preview.hide": "Hide details",
  "profile.preview.show": "Show details",
  "profile.col.parameter": "Parameter",
  "profile.col.current": "Current",
  "profile.col.next": "Next",
  "profile.col.status": "Status",
  "subset.active": "active",
  "subset.placeholder": "Select…",
  // --- Parameter field ---
  "parameter.help": "Help",
  "parameter.profile_badge": "profile",
  "parameter.last_value": "Last value",
  "parameter.modified": "modified",
  "parameter.read_only": "read-only",
  "parameter.not_triggerable": "not triggerable",
  "parameter.unknown_type": "Unknown type: {type}",
  "parameter.execute": "Run",
  "parameter.custom": "Custom value",
  // "Determine" button: reads the parameter's current value from the
  // device (OPERATIONS 0x08) and stages it into the editor.
  "parameter.determine": "Determine",
  "parameter.determine.tooltip": "Read the current value from the device",
  "parameter.determine.done": "Determined {name} from the device",
  "parameter.determine.failed": "Determine failed",
  "parameter.determine.unsupported":
    "This device does not support determining this parameter",
  // Directional qualifier appended to a label shared by two parameters
  // that differ only by an upper/lower threshold suffix in their name.
  "parameter.threshold.upper": "upper threshold",
  "parameter.threshold.lower": "lower threshold",
  // --- Time-pair presets (channel/time-pairs.ts) — only the
  // word-bearing presets need a key; numeric-with-unit presets
  // ("100 ms", "1 s", …) are locale-identical and carry the literal
  // string as their key, which `t()` returns unchanged as a fallback.
  "parameter.time_preset.not_active": "Not active",
  "parameter.time_preset.1_second": "1 second",
  "parameter.time_preset.2_seconds": "2 seconds",
  "parameter.time_preset.3_seconds": "3 seconds",
  "parameter.time_preset.30_seconds": "30 seconds",
  "parameter.time_preset.1_minute": "1 minute",
  "parameter.time_preset.2_minutes": "2 minutes",
  "parameter.time_preset.4_minutes": "4 minutes",
  "parameter.time_preset.15_minutes": "15 minutes",
  // --- App-level chrome / sidebar ---
  "common.ok": "OK",
  "app.theme.toggle": "Toggle theme",
  "sidebar.cluster.overview": "Overview",
  "sidebar.cluster.automation": "Automation",
  "sidebar.cluster.diagnose": "Status & Diagnose",
  "sidebar.cluster.system": "System",
  "sidebar.install_mode_active": "Pairing mode active",
  "sidebar.pending_messages": "{count} pending message(s)",
  "diagnostics.all_ccus": "all",
  // --- DeviceList ---
  "device.list.select_aria": "Select device",
  "device.list.reachable": "Reachable",
  "device.list.unreachable": "Not reachable",
  "device.list.firmware_available": "Firmware update available",
  "device.list.channels": "channels",
  // --- DeviceDetail top-/sub-tabs ---
  "device.toptab.overview": "Overview",
  "device.toptab.configure": "Configure",
  "device.toptab.history": "History",
  "device.subtab.device_config": "Device configuration",
  "device.subtab.maintenance_config": "Maintenance configuration",
  "device.subtab.channels": "Channels",
  "device.subtab.links": "Direct links",
  "device.subtab.schedule": "Schedule",
  "device.virtual": "Virtual",
  "device.no_device_config": "This device has no device-level configuration.",
  "device.week_profile_channel.title": "Week-profile channel",
  "device.week_profile_channel.body":
    "This channel only stores the device schedule. Open the schedule editor to edit it.",
  "device.confirm_remove_title": "Remove device?",
  "device.confirm_remove_body":
    'Remove "{name}"? The CCU pairing will be dropped and cannot be undone.',
  "device.delete.mode_label": "Removal mode",
  "device.delete.mode_unpair": "Unregister only",
  "device.delete.mode_unpair_hint": "Drop the CCU pairing (default).",
  "device.delete.mode_reset": "Reset to factory defaults",
  "device.delete.mode_reset_hint":
    "Also reset the device to factory settings while removing it.",
  "device.delete.force": "Force removal (device unreachable)",
  "device.delete.force_hint":
    "Remove the device even when it no longer responds to the CCU.",
  "device.delete.checking": "Checking dependencies…",
  "device.delete.warning_title": "This device is still referenced",
  "device.delete.warning_links":
    "{count} direct link(s) reference this device and will stop working.",
  "device.delete.warning_programs": "{count} program(s) reference this device.",
  "device.confirm_firmware_body":
    'Trigger firmware update for "{name}"? The device will be briefly unreachable.',
  "device.restore_config": "Restore config",
  "device.restore_config.tooltip":
    "Re-transmit the stored configuration to the device (after a factory reset)",
  "device.confirm_restore_config_body":
    'Re-transmit the stored configuration (all channel settings and direct links) to "{name}"? Use this after a factory reset — the transfer runs over the radio and may take a while.',
  "device.restore_config_triggered": "Configuration transfer started.",
  "device.communication_test": "Test",
  "device.communication_test.tooltip":
    "Send a radio test frame and check the device answers",
  "device.communication_test_running": "Testing…",
  "device.communication_test_passed": "Communication OK",
  "device.communication_test_failed": "No response",
  "device.team.title": "Team",
  "device.team.reset": "Default team",
  "device.team.changed": "Team assignment updated.",
  "device.team.none": "No other teams for this device type.",
  "device.status.paramset_pick": "Read source",
  // --- Maintenance grid ---
  "device.maintenance.title": "Maintenance",
  "device.maintenance.reachable": "Reachable",
  "device.maintenance.rssi_device": "RSSI (device)",
  "device.maintenance.rssi_peer": "RSSI (peer)",
  "device.maintenance.low_bat": "Low battery",
  "device.maintenance.battery": "Battery",
  "device.maintenance.bat_low": "Low",
  "device.maintenance.status_ok": "OK",
  "device.maintenance.blocked": "Blocked",
  "device.maintenance.operating_voltage": "Operating voltage",
  "device.maintenance.duty_cycle": "Duty-cycle blocked",
  "device.maintenance.duty_cycle_level": "Duty cycle",
  "device.maintenance.carrier_sense_level": "Carrier sense",
  "device.maintenance.config_pending": "Config pending",
  "device.maintenance.update_pending": "Update pending",
  "device.config_pending": "Scheduled",
  // --- Friendly API errors ---
  "api.error.upstream_unavailable":
    "CCU temporarily unavailable. Try again in a few seconds.",
  "api.error.unauthorized": "Session expired. Please sign in again.",
  "auth.error.invalid_credentials": "Invalid username or password.",
  "api.error.forbidden": "You are not authorised for this action.",
  "api.error.not_found": "Resource not found.",
  "api.error.rate_limited": "Too many requests — slowing down.",
  "api.error.server": "Server error ({status}).",
  "api.error.request": "Request rejected ({status}).",
  "api.error.locked":
    "Locked — this channel is protected against control writes. Lift the lock in the channel's flags.",
  "api.error.locked_reason": "Locked ({status}).",
  "api.error.edit_lock_lapsed":
    "Your edit session has expired — reopen the paramset editor to get a new lock.",
  // --- Matter bridge ---
  "nav.matter": "Matter",
  "sidebar.cluster.bridges": "Bridges",
  "matter.tab.expose": "Expose",
  "matter.tab.fabrics": "Fabrics",
  "matter.tab.pair": "Pair",
  "matter.tab.diagnostics": "Diagnostics",
  "matter.diag.title": "Bridge diagnostics",
  "matter.diag.discovery": "Discovery",
  "matter.diag.discovery_ok": "The bridge advertises correctly — controllers can discover it.",
  "matter.diag.not_advertising": "mDNS advertising is switched off, so no controller can discover this bridge.",
  "matter.diag.port": "Port",
  "matter.diag.severity.error": "Blocking",
  "matter.diag.severity.warning": "Warning",
  "matter.diag.sessions": "Connected controllers",
  "matter.diag.sessions_hint": "How long each controller has been quiet. A controller that goes away without disconnecting leaves its session open and simply stops sending.",
  "matter.diag.no_sessions": "No controller is connected right now.",
  "matter.diag.sessions_occupancy": "Session ids: {live} live · {reserved} reserved for handshakes in progress · {free} of {capacity} free",
  "matter.diag.col_session": "Session",
  "matter.diag.col_fabric": "Fabric",
  "matter.diag.col_peer_idle": "Controller quiet for",
  "matter.diag.col_subscriptions": "Subscriptions",
  "matter.diag.pase": "Pairing",
  "matter.diag.no_subscriptions": "connected but receiving nothing",
  "matter.diag.age_seconds": "{n}s",
  "matter.diag.age_minutes": "{n}min",
  "matter.diag.age_hours": "{n}h",
  "matter.diag.events": "Recent events",
  "matter.diag.events_hint": "What happened, as opposed to what is currently true. Kept in memory only and lost on restart.",
  "matter.diag.events_empty": "Nothing recorded since the bridge started.",
  "matter.diag.kind_pairing": "Pairing",
  "matter.diag.kind_session": "Session",
  "matter.diag.kind_discovery": "Discovery",
  "matter.diag.compatibility": "Ecosystem compatibility",
  "matter.diag.compat_ok": "No known incompatibilities for the paired ecosystems.",
  "matter.diag.ecosystem.apple": "Apple",
  "matter.diag.ecosystem.google": "Google",
  "matter.diag.ecosystem.amazon": "Alexa",
  "matter.diag.ecosystem.smartthings": "SmartThings",
  "matter.diag.ecosystem.aqara": "Aqara",
  "matter.diag.ecosystem.home_assistant": "Home Assistant",
  "matter.diag.ecosystem.unknown": "Unknown controller",
  "matter.diag.endpoints": "Endpoints",
  "matter.diag.endpoints_hint": "What a controller sees. Endpoint numbers come from stored identity, so they stay the same across restarts but cannot be derived from the device list.",
  "matter.diag.no_endpoints": "No device is exposed to Matter yet.",
  "matter.diag.reachable": "Reachable",
  "matter.diag.unreachable": "Unreachable",
  "matter.status.enabled": "Matter Bridge active",
  "matter.status.disabled":
    "Matter bridge is not enabled. Set matter.enabled = true in config.yaml to activate.",
  "matter.status.listening": "listening",
  "matter.status.not_listening": "not listening",
  "matter.status.endpoints": "{count} endpoints",
  "matter.status.fabrics": "{count} fabrics",
  "matter.status.advertising": "advertising",
  "matter.expose.empty": "No exposable data points found.",
  "matter.expose.filter_kind": "Kind",
  "matter.expose.filter_class": "Class",
  "matter.expose.filter_class_all": "All classes",
  "matter.expose.filter_class_unmapped": "(no class)",
  "matter.expose.select_all": "Select all",
  "matter.expose.search_placeholder": "Search name, address, class…",
  "matter.expose.col_channel": "Ch.",
  "matter.expose.col_parameter": "Parameter",
  "matter.expose.kind.custom": "Custom",
  "matter.expose.kind.generic": "Generic",
  "matter.expose.kind.calculated": "Calculated",
  "matter.expose.kind.combined": "Combined",
  "matter.expose.kind.measurement": "Measurement",
  "matter.expose.unmappable_hint": "Not mappable to a Matter endpoint.",
  "matter.expose.partially_mappable_hint":
    "Partially mappable — some clusters will remain MQTT-only.",
  "matter.expose.conflict_hint":
    "Already exposed via another data point on this channel.",
  "matter.expose.conflict_hint_custom_active":
    "Already exposed via custom DP `{profile}` — bridging the generic DP risks duplicate Matter entities.",
  "matter.expose.conflict_hint_generic_active":
    "Channel also exposes generic DP(s) — Apple Home may show duplicate entities.",
  "matter.expose.bulk_expose": "Expose selection",
  "matter.expose.bulk_hide": "Hide selection",
  "matter.expose.save": "Save changes",
  "matter.expose.discard": "Discard",
  "matter.expose.saved_toast": "{count} update(s) applied.",
  "matter.expose.legend": "Legend",
  "matter.expose.group_count": "{count} data points",
  "matter.expose.state_exposed": "Exposed",
  "matter.expose.state_partial": "Partially mappable",
  "matter.expose.state_available": "Available (not exposed)",
  "matter.expose.state_unmappable": "Cannot be mapped",
  "matter.expose.unmappable_checkbox_title":
    "Cannot be mapped to a Matter endpoint",
  // Count-neutral phrasing on purpose: t() has no plural machinery, and
  // one controller is the normal case.
  "matter.pair.already_paired": "Controllers already holding this bridge: {count}. Opening a window adds another one; the existing pairings are untouched.",
  "matter.pair.add_controller": "Add another controller",
  "matter.pair.copy_manual_code": "Copy the manual pairing code",
  "matter.pair.copy_qr_payload": "Copy the QR payload",
  "matter.pair.copied": "Copied to the clipboard.",
  "matter.pair.copy_failed": "The browser refused clipboard access — copy the code by hand.",
  "matter.pair.window_open": "Pairing window open",
  "matter.pair.window_open_duration": "Open pairing window",
  "matter.pair.qr_caption": "Scan QR with your Matter controller app",
  "matter.pair.manual_code": "Manual code",
  "matter.pair.success": "Controller paired successfully.",
  "matter.pair.close_window": "Close pairing window",
  "matter.pair.loading": "Loading commissioning state…",
  "matter.pair.load_error": "Failed to load commissioning state.",
  "matter.pair.minutes": "min",
  // Localized rendering of the `matter.commissioning_progress` WS
  // broadcast's `stage` token — mirrors `MatterCommissioningClose`
  // in internal/north/rest/handlers/matter_exposures.go.
  "matter.commissioning.closed": "Commissioning window closed by operator",
  "matter.maint.title": "Maintenance",
  "matter.maint.force_sync": "Re-sync topology",
  "matter.maint.force_sync_hint": "Rebuilds the exposed endpoints from the current devices. Controllers keep their pairing.",
  "matter.maint.force_sync_done": "Topology re-assembled.",
  "matter.maint.reset": "Remove all pairings",
  "matter.maint.reset_hint": "Returns the bridge to its unpaired state. Every controller has to add it again.",
  "matter.maint.reset_confirm": "Remove all pairings?",
  "matter.maint.reset_confirm_body": "Every paired controller loses this bridge and has to add it again. This cannot be undone.",
  "matter.maint.reset_confirm_label": "Remove all",
  "matter.maint.reset_done": "All pairings removed.",
  "matter.fabric.unpair_confirm": "Remove this fabric?",
  "matter.fabric.unpaired": "Fabric removed.",
  "matter.fabric.share_bridge_hint": "Adding another controller opens a commissioning window with a QR code, a countdown, and a way to close it again — all of that lives on the pairing tab.",
  "matter.fabric.share_bridge_go": "Go to the pairing tab",
  "matter.fabric.share_bridge": "Share bridge with another controller",
  "matter.fabric.label_unknown": "(no label)",
  "sensor_actor.toggle_failed": "Could not toggle {name}",
  "sensor_actor.action_failed": "Action {name} failed",
  "sensor_actor.numeric_invalid": "Invalid value for {name}",
  "sensor_actor.numeric_invalid_detail": "Enter a number first.",
  "sensor_actor.send": "Send",
  "sensor_actor.cancel": "Cancel",
  "sensor_actor.no_primary":
    "No primary value available yet — waiting for first CCU push.",
  "sensor_actor.loading": "Loading {address}…",
  "sensor_actor.load_failed": "Could not load channel {address}.",
  "sensor_actor.event_last": "last {age}",
  "sensor_actor.event_idle": "not triggered yet",
  "sensor_actor.age_sec": "{n} s ago",
  "sensor_actor.age_min": "{n} min ago",
  "sensor_actor.age_hour": "{n} h ago",
  "sensor_actor.age_day": "{n} d ago",
  // --- Log viewer ---
  "nav.logs": "Logs",
  "logs.title": "Log Viewer",
  "logs.subtitle": "Structured daemon logs with live streaming.",
  "logs.default_level": "Default level",
  "logs.view.aggregated": "Aggregated",
  "logs.view.detail": "Detail",
  "logs.filter_placeholder": "Filter by message, component…",
  "logs.live": "Live",
  "logs.paused": "Paused",
  "logs.to_live": "▼ {count} new · Resume",
  "logs.download": "Download",
  "logs.download_last": "Last {count}",
  "logs.empty": "No log records yet.",
  "logs.forbidden": "Admin access required to view logs.",
  "logs.repeated": "×{count}",
  "logs.connection.live": "connected",
  "logs.connection.reconnecting": "reconnecting…",
  "logs.level_saved": "Default level saved.",
  // --- Unified recordings hub ---
  "diagnostics.recordings.section_title": "Recordings",
  "diagnostics.recordings.new_title": "New Recording",
  "diagnostics.recordings.type": "Type",
  "diagnostics.recordings.type.log": "Debug Log",
  "diagnostics.recordings.type.rpc": "RPC Traffic",
  "diagnostics.recordings.type.both": "Both",
  "diagnostics.recordings.scope": "CCU scope",
  "diagnostics.recordings.scope_all": "All CCUs",
  "diagnostics.recordings.start": "Start recording",
  "diagnostics.recordings.running_title": "Recording active",
  "diagnostics.recordings.col_type": "Type",
  "diagnostics.recordings.col_scope": "CCU / Scope",
  "diagnostics.recordings.col_start": "Start / Status",
  "diagnostics.recordings.col_size": "Size / Entries",
  "diagnostics.recordings.col_action": "Action",
  "diagnostics.recordings.empty": "No recordings yet.",
  "diagnostics.recordings.anonymise_hint":
    "Anonymise hashes operator-identifying fields (login subject, username) — interface names, device addresses and host IPs remain visible.",
  "diagnostics.recordings.retention_hint":
    "Debug-log captures use a rolling RAM buffer for the configured duration. RPC recordings keep the full session in memory until stopped and survive daemon restarts.",
  "diagnostics.recordings.format_map": "Map",
  "diagnostics.recordings.format_golden": "Golden",
  "diagnostics.recordings.until": "until {time}",
  "diagnostics.recordings.anonymised": "anonymised",
  "diagnostics.recordings.duration_open_hint": "0 = open (server cap 60 min)",
  "app.not_found": "Not found",
  "app.unknown_path": "Unknown path",
  "app.route_load_failed":
    "This view could not be loaded. The application was probably updated in the meantime — reload the page.",
  "audit.filter.global": "— (global)",
  "blind.label.position": "Position",
  "blind.label.slats": "Slats",
  "blind.pct_open": "open",
  "cdp.climate.absence": "Away",
  "cdp.climate.absence_active": "Away · active",
  "cdp.climate.activate": "Activate",
  "cdp.climate.actual_temp": "Actual temperature",
  "cdp.climate.away_24h": "24 h away",
  "cdp.climate.away_duration": "Duration (h)",
  "cdp.climate.away_temperature": "Temperature (°C)",
  "cdp.climate.boost": "Boost",
  "cdp.climate.frost": "Frost protection",
  "cdp.climate.heat_off": "Off",
  "cdp.climate.heat_on": "On",
  "cdp.climate.humidity": "Humidity",
  "cdp.climate.mode_auto": "Auto",
  "cdp.climate.mode_away": "Away",
  "cdp.climate.mode_boost": "Boost",
  "cdp.climate.mode_manual": "Manual",
  "cdp.climate.present": "Present",
  "cdp.climate.profile": "Profile",
  "cdp.climate.secondary_off": "Off",
  "cdp.climate.secondary_on": "On",
  "cdp.climate.week_program": "Week program {n}",
  "cdp.cover.close": "Close",
  "cdp.cover.open": "Open",
  "cdp.cover.position": "Position",
  "cdp.cover.secondary_open": "{pct}% open",
  "cdp.cover.secondary_slats": "Slats {pct}%",
  "cdp.cover.slats": "Slats",
  "cdp.cover.state_closed": "Closed",
  "cdp.cover.state_open": "Open",
  "cdp.cover.state_unknown": "Unknown",
  "cdp.cover.state_ventilating": "Ventilating",
  // Generic ENUM value tokens (sensor/actor readouts). Looked up as
  // `enum.<TOKEN>`; unknown tokens fall back to a title-cased form.
  "enum.CLOSED": "Closed",
  "enum.OPEN": "Open",
  "enum.TILTED": "Tilted",
  "enum.UNKNOWN": "Unknown",
  "enum.STABLE": "Stable",
  "enum.FALLING": "Falling",
  "enum.RISING": "Rising",
  "enum.UP": "Up",
  "enum.DOWN": "Down",
  "enum.NONE": "None",
  "enum.ON": "On",
  "enum.OFF": "Off",
  "enum.DRY": "Dry",
  "enum.WET": "Wet",
  // Generic data-point parameter labels (sensor/actor tiles). Looked up
  // as `datapoint.<NAME>`; channel-agnostic names only.
  "datapoint.STATE": "State",
  "datapoint.LEVEL": "Level",
  "datapoint.DIRECTION": "Direction",
  "datapoint.ERROR": "Error",
  "datapoint.WORKING": "Working",
  "cdp.cover.stop": "Stop",
  "cdp.cover.ventilate": "Ventilate",
  "cdp.light.brightness": "Brightness",
  "cdp.light.color": "Color",
  "cdp.light.color_temp": "Color temperature",
  "cdp.light.effect": "Effect",
  "cdp.light.hue": "Hue",
  "cdp.light.saturation": "Saturation",
  "cdp.light.white": "White",
  "cdp.panel.general": "General",
  "cdp.panel.group": "Group {n}",
  "cdp.panel.loading": "Loading {addr}/cdps · {n}s…",
  "cdp.panel.no_controls": "No controls for this device.",
  "cdp.panel.server_unresponsive":
    "Server not responding. Check if the daemon is running (browser Network tab: <code>/api/v1/devices/{addr}/cdps</code>).",
  "cdp.retry": "Retry",
  "cdp.siren.acoustic": "Acoustic",
  "cdp.siren.duration": "Duration",
  "cdp.siren.off": "Off",
  "cdp.siren.optical": "Optical",
  "cdp.siren.state_acoustic": "Acoustic",
  "cdp.siren.state_active": "Alarm active",
  "cdp.siren.state_optical": "Optical",
  "cdp.siren.state_quiet": "Quiet",
  "cdp.siren.test": "Test",
  "cdp.siren.volume": "Volume",
  "cdp.switch.on_for": "On for…",
  "cdp.group_n": "Group {n}",
  "cdp.tile.no_state": "No state received yet",
  "cdp.status.age": " · last seen {ago} ago",
  "cdp.status.from_cache": "Restored from cache{age}",
  "cdp.status.no_datapoints": "No data points observed.",
  "cdp.status.stale": "Connection lost{age}",
  "cdp.textdisplay.advanced": "Advanced",
  "cdp.textdisplay.color_label": "Color",
  "cdp.textdisplay.color_placeholder": "e.g. WHITE",
  "cdp.textdisplay.icon_label": "Icon",
  "cdp.textdisplay.icon_placeholder": "e.g. 0",
  "cdp.textdisplay.less": "Less",
  "cdp.textdisplay.row": "Row {row}",
  "cdp.textdisplay.row_label": "Row",
  "cdp.textdisplay.sending": "Sending…",
  "cdp.textdisplay.text_placeholder": "Text…",
  "cdp.textdisplay.write": "Write",
  "cdp.valve.close": "Close",
  "cdp.valve.open": "Open",
  "cdp.valve.open_for": "Open for…",
  "cdp.valve.opening": "Opening",
  "cdp.valve.secondary_open": "{pct}% open",
  "cdp.valve.state_closed": "Closed",
  "cdp.valve.state_open": "Open",
  "climate.mode.auto": "Auto",
  "climate.mode.away": "Away",
  "climate.mode.boost": "Boost",
  "climate.mode.manual": "Manual",
  "climate.preset.boost": "Boost",
  "climate.preset.comfort": "Comfort",
  "climate.preset.frost": "Frost protection",
  "climate.preset.lowering": "Economy",
  "climate.stat.current_temp": "Actual temp.",
  "climate.stat.heat_cool": "Heat/Cool",
  "climate.stat.humidity": "Humidity",
  "climate.stat.valve": "Valve",
  "climate.stat.window": "Window",
  "common.all_ccus": "All CCUs",
  "common.max": "Max",
  "common.min": "Min",
  "common.select_placeholder": "— select —",
  "control.active": "Active",
  "control.alarm_active": "Alarm active",
  "control.brightness": "Brightness",
  "control.color": "Color",
  "control.color_temp": "Color temperature",
  "control.current": "Current",
  "control.effect": "Effect",
  "control.energy": "Energy",
  "control.frequency": "Frequency",
  "control.hue": "Hue",
  "control.idle": "Idle",
  "control.locked": "Locked",
  "control.number.decrement": "Decrease",
  "control.number.increment": "Increase",
  "control.power": "Power",
  "control.status_unknown": "Status unknown",
  "control.test": "Test",
  "control.unlocked": "Unlocked",
  "control.voltage": "Voltage",
  "cover.close": "Close",
  "cover.open": "Open",
  "cover.stop": "Stop",
  "device.aria.configure_sub_tabs": "Configure sub-tabs",
  "device.aria.top_tabs": "Top tabs",
  "devicelist.all": "All",
  "devicelist.all_areas": "All areas",
  "devicelist.all_rooms": "All rooms",
  "devicelist.apply": "Apply",
  "devicelist.availability": "Availability",
  "devicelist.available": "Available",
  "devicelist.bulk_firmware_body":
    "Trigger firmware update for {count} device(s)?",
  "devicelist.bulk_firmware_confirm": "Start update",
  "devicelist.bulk_firmware_label": "Firmware update",
  "devicelist.bulk_no_updates":
    "No selected devices have a firmware update available.",
  "devicelist.bulk_result": "{ok} OK, {fail} failed.",
  "devicelist.ccu_refresh": "Reload from CCU",
  "devicelist.ccu_refresh_title": "Re-read device list and names from the CCU",
  "devicelist.clear_selection": "Clear selection",
  "devicelist.col.address": "Address",
  "devicelist.col.model": "Model",
  "devicelist.col.name": "Name",
  "devicelist.col.rooms": "Rooms",
  "devicelist.col.status": "Status",
  "devicelist.count": "{filtered} / {total} devices",
  "devicelist.group_by_interface": "Group by interface",
  "devicelist.last_updated": "Last updated {time}",
  "devicelist.load_error": "Error loading: {error}",
  "devicelist.area": "Area",
  "devicelist.room": "Room",
  "devicelist.room_aria": "Room for selection",
  "devicelist.room_placeholder": "Room (empty = remove)",
  "devicelist.search_placeholder": "Search (address, name, model)",
  "devicelist.select_filtered": "Select filtered",
  "devicelist.selected": "{count} device(s) selected",
  "devicelist.set_room": "Set room",
  "devicelist.unavailable": "Unavailable",
  "devicelist.update_available": "Update available",
  "devicelist.view_mode": "View",
  "devicelist.view_grid": "Grid view",
  "devicelist.view_list": "Table view",
  "garage.cmd.close": "Close",
  "garage.cmd.open": "Open",
  "garage.cmd.stop": "Stop",
  "garage.cmd.vent": "Vent",
  "garage.state.closed": "Closed",
  "garage.state.open": "Open",
  "garage.state.unknown": "Unknown",
  "garage.state.ventilating": "Ventilating",
  "inbox.install_mode": "Pairing mode",
  "inbox.install_mode_active_title": "Pairing mode active — click to stop",
  "inbox.install_mode_badge": "active",
  "inbox.install_mode_pairing": "Pairing · {seconds} s",
  "inbox.install_mode_running": "Pairing mode running",
  "inbox.install_mode_seconds_left": "seconds remaining",
  "inbox.install_mode_start_title":
    "Start pairing mode (60 s) to pair new devices",
  "inbox.install_mode_select_interface":
    "Select an interface to start pairing.",
  "inbox.install_mode_banner_iface_on":
    "Pairing mode active on {iface} ({seconds} s).",
  "inbox.install_mode_banner_iface_off": "Pairing mode stopped on {iface}.",
  "inbox.pair_serial_label": "Pair by serial:",
  "inbox.pair_serial_placeholder": "Device address / serial",
  "inbox.pair_serial_submit": "Pair device",
  "inbox.install_mode_local_label": "Pair HmIP device offline (SGTIN + key):",
  "inbox.install_mode_local_sgtin_label": "SGTIN",
  "inbox.install_mode_local_sgtin_placeholder": "SGTIN, e.g. 3014-F711-A000-…",
  "inbox.install_mode_local_key_label": "Device key",
  "inbox.install_mode_local_key_placeholder": "Device key from the label",
  "inbox.install_mode_local_submit": "Start local teach-in",
  "inbox.install_mode_local_started":
    "Local teach-in started — press the pairing button on the device.",
  "inbox.install_mode_local_hint":
    "Works without internet access: only the device matching SGTIN and key can pair.",
  "inbox.search_wired": "Search wired bus",
  "inbox.search_wired_title":
    "Scan the BidCos-Wired bus for newly connected devices",
  "inbox.search_wired_hint":
    "Scans the wired bus; found devices appear in the inbox.",
  "inbox.search_wired_running": "Scanning…",
  "inbox.search_wired_done": "Found {count} device(s) — check the inbox.",
  "inbox.replace.button": "Replace device",
  "inbox.replace.title": "Replace an existing device",
  "inbox.replace.intro":
    "Choose the paired device that {address} replaces. Its links, teams and programs move to the new device; the old device is unpaired.",
  "inbox.replace.empty": "No replaceable devices",
  "inbox.replace.empty_description":
    "The CCU found no compatible paired device this one can replace.",
  "inbox.replace.same_type": "Same type",
  "inbox.replace.compatible_type": "Compatible",
  "inbox.replace.confirm_title": "Replace device?",
  "inbox.replace.confirm_text":
    '"{new}" will replace "{old}". The old device is unpaired and removed from the system. This cannot be undone.',
  "inbox.replace.confirm_label": "Replace",
  "inbox.replace.success": "Device replaced.",
  "inbox.pair_serial_started": "Pairing window opened for {addr}.",
  "light.brightness": "Brightness",
  "light.color_temp": "Color temperature",
  "light.effect": "Effect",
  "light.hue": "Hue",
  "light.mode.color": "Color",
  "light.mode.white": "White",
  "light.saturation": "Saturation",
  "lock.lock": "Lock",
  "lock.locked": "Locked",
  "lock.open_door": "Open door",
  "lock.unlock": "Unlock",
  "lock.unlocked": "Unlocked",
  "login.password": "Password",
  "login.sso": "Single Sign-On (OIDC)",
  "login.ccu_hint": "You can sign in with your CCU account.",
  "login.submit": "Sign in",
  "login.submitting": "Signing in…",
  "login.username": "Username",
  "setup.step.progress": "Step {current} of {total}",
  "setup.step1.title": "Administrator account",
  "setup.step2.title": "Language & appearance",
  "setup.step3.title": "Connect a CCU",
  "setup.step4.title": "MQTT broker",
  "setup.username": "Username",
  "setup.password": "Password",
  "setup.confirm": "Confirm password",
  "setup.password.too_short": "Password must be at least 8 characters.",
  "setup.password.mismatch": "Passwords do not match.",
  "setup.locale.label": "Language",
  "setup.theme.label": "Appearance",
  "setup.theme.system": "Follow system",
  "setup.theme.light": "Light",
  "setup.theme.dark": "Dark",
  "setup.ccu.enable": "Connect a CCU now",
  "setup.ccu.name": "Name",
  "setup.ccu.host": "Host",
  "setup.ccu.interfaces": "Interfaces",
  "setup.ccu.interfaces_hint": "Select the radios this CCU exposes.",
  "setup.mqtt.enable": "Enable MQTT",
  "setup.mqtt.broker": "Broker URL",
  "setup.back": "Back",
  "setup.next": "Next",
  "setup.finish": "Finish setup",
  "setup.finishing": "Finishing…",
  "setup.done.title": "Setup complete",
  "setup.done.detail": "Sign in with your new administrator account.",
  "setup.error.title": "Setup failed",
  "logs.groups": "groups",
  "matter.expose.col_select": "Select",
  "matter.expose.col_state": "State",
  "matter.expose.drawer_aria": "Exposure detail",
  "matter.expose.drawer_clusters": "Clusters",
  "matter.expose.drawer_device_type": "Matter device type",
  "matter.expose.drawer_source": "Source",
  "matter.expose.drawer_state": "State",
  "matter.expose.friendly_name": "Friendly name",
  "matter.expose.select_row": "Select row",
  "matter.fabrics.col_fabric": "Fabric #",
  "matter.fabrics.col_label": "Label",
  "matter.fabrics.col_node_id": "Node ID",
  "matter.fabrics.col_vendor": "Vendor",
  "matter.fabrics.empty": "No fabrics paired yet.",
  "matter.fabrics.node_id_rounded": "rounded",
  "matter.fabrics.node_id_rounded_hint":
    "This node id exceeds the precision this list transports, so its last digits may differ from the ones the controller shows.",
  "matter.pair.qr_payload": "QR payload",
  "select.placeholder": "Select…",
  "sysvars.create.values_placeholder": "off;on;blink",
  "ui.breadcrumb": "Breadcrumb",
  "ui.dismiss": "Dismiss",
  "ui.events_since_connect": "Events since connection",
  "diagnostics.recording_type.debug_log": "Debug log",
  "diagnostics.col.interface": "Interface",
  "diagnostics.col.type": "Type",
  "diagnostics.col.status": "Status",
  "diagnostics.col.duty_cycle": "Duty cycle",
  "diagnostics.col.carrier_sense": "Carrier sense",
  "diagnostics.utilisation_unknown": "Not reported for this interface",
  "diagnostics.col.host": "Host / Central",
  "diagnostics.col.action": "Action",
  "diagnostics.col.client": "Client",
  "diagnostics.col.score": "Score",
  "diagnostics.reliability.title": "Reliability",
  "diagnostics.reliability.help":
    "Circuit-breaker and connection state per (central, interface) pair.",
  "diagnostics.reliability.col.central": "Central",
  "diagnostics.reliability.col.interface": "Interface",
  "diagnostics.reliability.col.circuit": "Circuit",
  "diagnostics.reliability.col.state": "State",
  "diagnostics.reliability.col.requests":
    "Requests (total / executed / pending)",
  "diagnostics.reliability.col.last_failure": "Last failure",
  "diagnostics.reliability.col.last_callback": "Last callback",
  "diagnostics.reliability.circuit.closed": "Closed",
  "diagnostics.reliability.circuit.open": "Open",
  "diagnostics.reliability.circuit.half_open": "Half-open",
  "diagnostics.reliability.empty": "No interface clients reporting yet.",
  "diagnostics.values_cache.title": "Values cache",
  "diagnostics.values_cache.help":
    "Persistent VALUES-cache row count, size, and cumulative counters since process start.",
  "diagnostics.values_cache.rows": "Rows",
  "diagnostics.values_cache.bytes": "Value JSON bytes",
  "diagnostics.values_cache.restored": "Restored",
  "diagnostics.values_cache.cast_failures": "Cast failures",
  "diagnostics.values_cache.gc_deleted": "GC-deleted",
  "diagnostics.values_cache.flush_batches": "Flush batches",
  "diagnostics.values_cache.flushed_entries": "Flushed entries",
  "diagnostics.values_cache.reset": "Reset cache",
  "diagnostics.values_cache.reset_confirm_title": "Reset the VALUES cache?",
  "diagnostics.values_cache.reset_confirm_body":
    "Every cached wire value is dropped. Data points read source=unobserved until live events repopulate them.",
  "diagnostics.values_cache.reset_success": "Values cache reset.",
  "schedule.aria.weekdays": "Weekdays",
  "schedule.duration_placeholder": "e.g. 10s, 5min",
  "schedule.ramp_placeholder": "e.g. 500ms, 2s",
  "ccu_position.title": "Astro position",
  "ccu_position.latitude": "Latitude",
  "ccu_position.longitude": "Longitude",
  "ccu_position.help":
    "Reference position for the CCU's sunrise and sunset times. Wrong coordinates shift every astro schedule without any error.",
  "ccu_position.unknown": "Position not known yet.",
  "ccu_position.confirm_title": "Change astro position?",
  "ccu_position.confirm_body":
    "This changes the sunrise and sunset times {central} computes — for its own programs and for the weekly profiles edited here.",
  "ccu_position.saved": "Astro position of {central} saved.",
  "ccu_host.poweroff.action": "Shut down",
  "ccu_host.poweroff.confirm_title": "Shut the CCU down?",
  "ccu_host.poweroff.confirm_body":
    "{central} will power off. Nothing brings it back on remotely — it has to be switched on at the device.",
  "ccu_host.poweroff.triggered": "Shutdown of {central} triggered.",
  "ccu_host.safe_mode.action": "Safe mode",
  "ccu_host.safe_mode.confirm_title": "Restart into safe mode?",
  "ccu_host.safe_mode.confirm_body":
    "{central} restarts with its logic layer held down. Programs and system variables do not run until you leave safe mode again.",
  "ccu_host.safe_mode.triggered": "{central} is restarting into safe mode.",
  "ccu_host.recovery_mode.action": "Recovery",
  "ccu_host.recovery_mode.confirm_title": "Restart into the recovery system?",
  "ccu_host.recovery_mode.confirm_body":
    "{central} restarts into its recovery system and stays out of service until you leave it there. The recovery interface is reachable at the CCU's own address.",
  "ccu_host.recovery_mode.triggered":
    "{central} is restarting into the recovery system.",
  "ccu_maintenance.title": "CCU maintenance",
  "ccu_maintenance.subtitle":
    "Host-level actions for each connected CCU. Rebooting restarts the CCU and briefly drops its connection.",
  "ccu_maintenance.empty": "No CCU configured yet.",
  "ccu_maintenance.online": "Online",
  "ccu_maintenance.offline": "Offline",
  "ccu_maintenance.reboot": "Reboot CCU",
  "ccu_maintenance.rebooting": "Rebooting…",
  "ccu_maintenance.confirm_title": "Reboot CCU?",
  "ccu_maintenance.confirm_body":
    "{central} will reboot now. The connection to this CCU drops until it is back online. Continue?",
  "ccu_maintenance.triggered":
    "Reboot triggered for {central} — it will be back shortly.",
  "ccu_maintenance.admin_only": "Only administrators can reboot a CCU.",
  "ccu_update.admin_only": "Only administrators can install CCU updates.",
  "ccu_update.available": "Update available",
  "ccu_update.confirm_body":
    "{central} will download and install its firmware update and reboot — the connection drops briefly. Continue?",
  "ccu_update.backup_first": "Back up first",
  "ccu_update.backup_first.help":
    "Take a full CCU backup before the update. The update only starts once the backup is stored, and does not start at all if it fails. This can take several minutes.",
  "ccu_update.backing_up": "Backing up…",
  "ccu_update.confirm_body_with_backup":
    "A full backup of {central} is taken first; the update starts only if it succeeds. This can take several minutes — leave the page open.",
  "ccu_update.confirm_title": "Install CCU update?",
  "ccu_update.empty": "No CCU update info available yet.",
  "ccu_update.in_progress": "Installing…",
  "ccu_update.install": "Install update",
  "ccu_update.installing": "Triggering…",
  "ccu_update.not_observed": "Update status not yet fetched.",
  "ccu_update.subtitle":
    "Trigger the CCU's own firmware update. The CCU reboots during the install.",
  "ccu_update.title": "CCU system update",
  "ccu_update.triggered":
    "CCU update triggered for {central} — it will reboot.",
  "firmware_download.title": "Download firmware to a CCU",
  "firmware_download.subtitle":
    "The CCU fetches a firmware image from the URL onto the central so it can be staged for installation.",
  "firmware_download.url_label": "Firmware image URL",
  "firmware_download.url_placeholder": "https://…",
  "firmware_download.download": "Download",
  "firmware_download.downloading": "Downloading…",
  "firmware_download.triggered": "Firmware download triggered.",
  // Add-on self-update card (ADR 0057) — capability-gated (`addon_self_update`).
  "addon_update.title": "Add-on self-update",
  "addon_update.subtitle":
    "Check for and install updates to the CCU add-on package. The daemon restarts during install.",
  "addon_update.check": "Check for updates",
  "addon_update.checking": "Checking…",
  "addon_update.available": "Update available",
  "addon_update.up_to_date": "Up to date",
  "addon_update.install": "Install update",
  "addon_update.install_starting": "Starting…",
  "addon_update.installing_notice":
    "Installing the update — the daemon is restarting. This page reconnects automatically once it's back.",
  "addon_update.confirm_title": "Install add-on update?",
  "addon_update.confirm_body":
    "The daemon restarts to complete the install — the connection drops briefly and reconnects on its own. Continue?",
  "addon_update.release_notes": "Release notes",
  "addon_update.never_checked": "Never checked",
  "addon_update.field.current_version": "Installed version",
  "addon_update.field.latest_version": "Latest version",
  "addon_update.field.last_check": "Last checked",
  "addon_update.toast.check_failed": "Update check failed",
  "addon_update.toast.install_trigger_failed": "Could not start the update",
  "addon_update.toast.failed": "Add-on update failed",
  "addon_update.toast.installed": "Add-on updated to {version}",
  // --- Column labels for migrated DataTable views ---
  "messages.col.name": "Message",
  "messages.col.device": "Device",
  "messages.col.time": "Time",
  "messages.col.last_timestamp": "Last changed",
  "messages.col.type": "Type",
  "messages.col.actions": "Actions",
  "inbox.col.address": "Address",
  "inbox.col.model": "Model",
  "inbox.col.serial": "Serial",
  "inbox.col.first_seen": "First seen",
  "inbox.col.actions": "Actions",
  "audit.col.time": "Time",
  "audit.col.action": "Action",
  "audit.col.user": "User",
  "audit.col.target": "Target",
  "audit.col.changes": "Changes",
  // --- Fleet (read-only cross-CCU overview) ---
  "fleet.title": "Fleet",
  "fleet.subtitle":
    "All configured CCUs, their status, interfaces and device counts at a glance.",
  "fleet.empty": "No CCUs configured yet.",
  "fleet.empty.description":
    "Register a CCU in Settings to monitor its connection and devices here.",
  "fleet.load_error": "Could not load the CCU fleet: {error}",
  "fleet.status.online": "Online",
  "fleet.status.offline": "Offline",
  "fleet.field.host": "Host",
  "fleet.field.model": "Model",
  "fleet.field.version": "Firmware version",
  "fleet.field.serial": "Serial",
  "fleet.field.devices": "Devices",
  "fleet.field.interfaces": "Interfaces",
  "fleet.field.ccu_interfaces": "Interfaces reported by the CCU",
  "fleet.field.ccu_interfaces.unmanaged":
    "The CCU offers this interface, but this daemon is not configured for it.",
  "fleet.field.ccu_security": "CCU security",
  "fleet.field.auth_enabled.on": "Authentication required",
  "fleet.field.auth_enabled.off": "No authentication",
  "fleet.field.auth_enabled.hint":
    "Whether the CCU itself requires authentication. Also shows as “no authentication” when the CCU firmware does not answer the query.",
  "fleet.field.https_redirect.on": "HTTPS redirect on",
  "fleet.field.https_redirect.off": "HTTPS redirect off",
  "fleet.field.https_redirect.hint":
    "Whether the CCU redirects plain HTTP to HTTPS. Also shows as “off” when the CCU firmware does not answer the query.",
  "fleet.open_webui": "Open CCU WebUI",
  // --- Heating groups (read-only, GR01) ---
  "groups.title": "Heating groups",
  "groups.count": "{count} groups",
  "groups.empty": "No heating groups configured yet.",
  "groups.empty.description":
    "Heating groups are created and edited on the CCU itself; this view only mirrors the current roster.",
  "groups.field.id": "ID",
  "groups.type": "Type",
  "groups.new": "New group",
  "groups.select_ccu_first": "Select a CCU first.",
  "groups.delete.title": "Delete group?",
  "groups.delete.body":
    "Delete the heating group “{name}”? Its member wiring on the CCU is removed. This cannot be undone.",
  "groups.delete.done": "Group deleted.",
  "groups.editor.create_title": "New heating group",
  "groups.editor.edit_title": "Edit heating group",
  "groups.editor.created": "Group created.",
  "groups.editor.updated": "Group updated.",
  "groups.editor.name": "Name",
  "groups.editor.members": "Members",
  "groups.editor.no_members": "No assignable devices for this type.",
  "groups.editor.no_types": "No group types available.",
  "groups.editor.search_placeholder": "Search by name, room, type or serial…",
  "groups.editor.selection_summary": "{channels} channels · {devices} devices",
  "groups.editor.only_selected": "Only selected",
  "groups.editor.select_visible": "Select visible",
  "groups.editor.no_matches": "No matches — adjust the search or filter.",
  "groups.editor.selected": "Selected",
  "groups.editor.clear_all": "Clear all",
  "groups.editor.no_selection":
    "Nothing selected yet — tap a device or channel.",
  "groups.editor.channel_fallback": "Channel {no}",
  "groups.editor.not_selectable": "not assignable",
  "groups.editor.config_pending": "config pending",
  "groups.field.group_device_name": "Virtual device",
  "groups.operate_only_via_group": "Group-only operation",
  "groups.operate_only_via_group.help":
    "When on, the group's devices can only be operated together through the group, not individually.",
  "groups.members": "{count} members",
  "groups.members.empty": "No members",
  // --- Areas (operator-defined room groupings above CCU rooms) ---
  "areas.title": "Areas",
  "areas.hint":
    "Group CCU rooms into a larger area — a floor, a shed, a terrace roof. Distinct from alarm zones.",
  "areas.empty": "No areas configured.",
  "areas.col.rooms_count": "Rooms",
  "areas.placeholder": "Area name…",
  "areas.assign_rooms": "Assign rooms",
  "areas.delete_confirm": "Remove area?",
  "areas.delete_confirm.body":
    "Its room assignments are cleared; the rooms themselves are unaffected.",
  "areas.rooms_dialog.title": "Assign rooms — {name}",
  "areas.rooms_dialog.hint":
    "Checking a room moves it here from its current area — a room can only belong to one area at a time.",
  "areas.rooms_dialog.search_placeholder": "Search rooms…",
  "areas.rooms_dialog.empty":
    "No rooms known yet — assign a room to a device first.",
  "areas.rooms_dialog.current_area": "currently: {name}",
  "areas.toast.rooms_saved": "Rooms assigned.",
  // --- Security & Safety domain (notes/concepts/security-safety-concept.md §7.8).
  //     Classifier-driven hazard/fault classes, a fault ledger and the
  //     classified data-point inventory. Runs independently of the alarm
  //     engine above ("alarm.*") — a smoke/water/gas-only installation
  //     still gets classes and faults, only zones stays empty. ---
  "security.title": "Security & Safety",
  "security.subtitle":
    "Smoke, water, gas, tamper and other hazard classes — works with or without the alarm engine.",
  "security.tab.overview": "Overview",
  "security.tab.sources": "Sources",
  "security.tab.faults": "Faults",
  "security.intro.overview":
    "The folded severity, one tile per hazard class, the last report and the standing-fault count.",
  "security.intro.sources":
    "Every classified data point the domain knows about — filter it, and correct a wrong classification.",
  "security.intro.faults":
    "Standing faults, oldest first. Acknowledging records that you have seen it — it does not clear the condition.",
  // Hazard/fault classes, in escalation order. A class entity reports a
  // detection, not a verdict, so the names follow a verb pattern: a noun
  // reads as a finding ("Intrusion"), a verb as an observation. Kept
  // word-identical to security.entity.class.* in internal/i18n/catalogs,
  // which names the same classes on MQTT and for the client — pinned by
  // TestSPASecurityClassLabelsMatchDaemonCatalogues.
  "security.class.smoke": "Smoke detected",
  "security.class.water": "Water detected",
  "security.class.gas": "Gas detected",
  "security.class.co": "Carbon monoxide detected",
  "security.class.tamper": "Tamper detected",
  "security.class.battery": "Battery low",
  "security.class.technical": "Technical fault",
  "security.class.intrusion": "Opening or motion detected",
  "security.class.panic": "Panic triggered",
  // Folded severity.
  "security.severity.ok": "OK",
  "security.severity.info": "Info",
  "security.severity.warning": "Warning",
  "security.severity.alarm": "Alarm",
  "security.severity.critical": "Critical",
  // Standing-fault reasons.
  "security.fault_reason.unreachable": "Unreachable",
  "security.fault_reason.blocked": "Blocked",
  "security.fault_reason.device_error": "Device error",
  "security.fault_reason.central_lost": "CCU connection lost",
  "security.fault_reason.duty_cycle": "Duty-cycle limit",
  "security.fault_reason.low_battery": "Low battery",
  "security.fault_reason.tamper": "Tamper",
  // Overview.
  "security.overview.empty": "Nothing classified yet",
  "security.overview.empty.description":
    "Once a device with a smoke, water, gas, tamper or other security role comes online, it appears here automatically.",
  "security.overview.engine_healthy": "Alarm engine healthy",
  "security.overview.engine_unhealthy": "Alarm engine unhealthy",
  "security.overview.classes_title": "Hazard & fault classes",
  "security.overview.no_classes": "No sources classified yet",
  "security.overview.no_classes.description":
    "Sources appear here once the classifier finds a smoke, water, gas, tamper or other security-relevant data point.",
  "security.overview.class_active": "{count} active",
  // A class can be active without anything being wrong — an intrusion source
  // reports on a disarmed system too. This is the wording for that case, so
  // an observation never borrows an alarm's words. Colon form on purpose:
  // t() has no plural machinery, and "Reporting: 1" / "Meldet: 3" is correct
  // for every count in both locales where "{count} reporting" is not.
  "security.overview.class_reporting": "Reporting: {count}",
  "security.overview.class_inactive": "No active sources",
  "security.overview.class_known": "{count} known",
  "security.overview.class_since": "since {time}",
  "security.overview.sources_more": "+{count} more",
  "security.overview.zones_title": "Zones",
  "security.overview.zone_state_unknown": "Unknown",
  "security.overview.zones_empty": "No alarm engine configured",
  "security.overview.zones_empty.description":
    "This domain works independently of the alarm engine — that's a feature, not an error. Set up zones in the alarm panel to see them here too.",
  "security.overview.zones_open_alarm": "Open alarm panel",
  "security.overview.faults_count": "{count} open",
  "security.overview.faults_none": "No standing faults",
  "security.overview.last_alarm_title": "Last alarm report",
  "security.overview.last_fault_title": "Last fault report",
  "security.overview.no_report": "No report yet.",
  // Sources inventory.
  "security.sources.filter.class": "Class",
  "security.sources.filter.central": "CCU",
  "security.sources.filter.zone": "Zone",
  "security.sources.filter.relevant": "Relevant only",
  "security.sources.filter.active": "Active only",
  "security.sources.filter.all": "All",
  "security.sources.search": "Search…",
  "security.sources.empty": "No classified sources yet",
  "security.sources.empty.description":
    "Sources appear here once the classifier finds a smoke, water, gas, tamper or other security-relevant data point.",
  "security.sources.col.source": "Source",
  "security.sources.col.class": "Class",
  "security.sources.col.central": "CCU",
  "security.sources.col.zone": "Zone",
  "security.sources.col.relevant": "Relevant",
  "security.sources.col.active": "Active",
  "security.sources.col.override": "Override",
  "security.sources.badge.overridden": "Overridden",
  "security.sources.badge.relevant": "Relevant",
  "security.sources.badge.not_relevant": "Not relevant",
  "security.sources.badge.active": "Active",
  "security.sources.badge.inactive": "Inactive",
  "security.sources.intro.title": "What this list is",
  "security.sources.intro.body":
    "Every data point the daemon has classified as security-relevant \u2014 smoke, water, tamper, low battery, unreachable and the rest. \u201cRelevant\u201d means it counts towards the class tiles and the fault list; everything else is listed so you can find it, not because it is being watched.",
  "security.sources.intro.when":
    "You only need this page when the classifier got something wrong: a detector filed under the wrong class, or a data point that should not raise anything. Overriding one changes what the aggregates report \u2014 it does not change the alarm system, which is configured separately per zone.",
  "security.sources.intro.docs": "Read the Security and alarm guide",
  "security.sources.override.help":
    "Leave the class empty to keep the classifier\u2019s verdict. Turning off inclusion keeps the data point listed but takes it out of every aggregate. The note is for you \u2014 it records why, for whoever looks next.",
  "security.sources.override.keep": "Keep classifier verdict",
  "security.sources.override.included": "Included",
  "security.sources.override.note_placeholder": "Note (optional)",
  "security.sources.override.save": "Save override",
  "security.sources.override.reset": "Remove override",
  "security.sources.override.reset_title":
    "Return to the classifier's own verdict — the undo for a wrong override.",
  "security.sources.toast.saved": "Override saved",
  "security.sources.toast.save_failed": "Saving the override failed",
  "security.sources.toast.reset": "Override removed",
  "security.sources.toast.reset_failed": "Removing the override failed",
  // Faults.
  "security.faults.hint":
    "Acknowledging a fault only records that you have seen it — the underlying condition remains until it clears on its own.",
  "security.faults.empty": "No standing faults",
  "security.faults.empty.description":
    "Every classified source is currently healthy.",
  "security.faults.col.class": "Class",
  "security.faults.col.reason": "Reason",
  "security.faults.col.source": "Source",
  "security.faults.col.standing": "Standing for",
  "security.faults.col.status": "Status",
  "security.faults.col.actions": "Actions",
  "security.faults.status.acknowledged": "Acknowledged {time}",
  "security.faults.status.acknowledged_by": "Acknowledged {time} by {who}",
  "security.faults.status.open": "Not yet acknowledged",
  "security.faults.acknowledge_confirm.title": "Acknowledge this fault?",
  "security.faults.acknowledge_confirm.body":
    "This only records that you have seen it — the {reason} condition on {source} remains until it clears on its own.",
  "security.faults.toast.acknowledged":
    "Fault acknowledged — condition still stands",
  "security.faults.toast.acknowledge_failed": "Acknowledge failed",
  "security.faults.duration.days_hours": "{days}d {hours}h",
  "security.faults.duration.hours_minutes": "{hours}h {minutes}m",
  "security.faults.duration.minutes": "{minutes}m",
};

const DE: Catalog = {
  // --- Alarmanlage (notes/concepts/alarm-concept.md §12). Abzugrenzen von den
  //     CCU-Alarmmeldungen (siehe "messages.*"): dies ist looms eigene
  //     Einbruchmelde-Engine ("Alarmanlage"). ---
  "alarm.title": "Alarmanlage",
  "alarm.subtitle":
    "Zonen, Sensoren, Sirenen — looms lokale Einbruchmeldeanlage.",
  "alarm.tab.overview": "Übersicht",
  "alarm.tab.sensors": "Sensoren",
  "alarm.tab.outputs": "Ausgänge",
  "alarm.tab.policies": "Richtlinien",
  "alarm.tab.codes": "Codes",
  "alarm.tab.journal": "Journal",
  "alarm.tab.walktest": "Begehungstest",
  // Scharfschalt-Modi (§4).
  "alarm.mode.disarmed": "Unscharf",
  "alarm.mode.perimeter": "Hüllschutz",
  "alarm.mode.full": "Vollschutz",
  "alarm.mode.night": "Nachtschutz",
  "alarm.mode.vacation": "Urlaub",
  "alarm.mode.custom": "Benutzerdefiniert",
  // Zustände der Zustandsmaschine (§5).
  "alarm.state.disarmed": "Unscharf",
  "alarm.state.arming": "Wird scharf …",
  "alarm.state.armed": "Scharf",
  "alarm.state.pending": "Eintrittsverzögerung …",
  "alarm.state.triggered": "Alarm",
  // Übersicht (§12.1).
  "alarm.overview.empty": "Noch keine Alarmzonen",
  "alarm.overview.empty.description":
    "Lege deine erste Zone mit dem Einrichtungsassistenten an, um Räume zu schützen.",
  "alarm.overview.armed_by": "seit {time}, von {user}",
  "alarm.overview.armed_at": "seit {time}",
  "alarm.overview.silence_all": "Alle Sirenen aus",
  "alarm.overview.reset_motion_all": "Bewegung zurücksetzen ({count})",
  "alarm.overview.reset_motion_all.hint":
    "Setzt die aktuell ausgelösten Bewegungsmelder zurück. Ein ausgelöster Melder gilt als offen und kann die Scharfschaltung blockieren.",
  "alarm.overview.open_security": "Sicherheit & Sicherheitstechnik",
  "alarm.readiness.ready": "bereit",
  "alarm.readiness.blockers_title": "Blockierende Sensoren",
  "alarm.readiness.warnings_title": "Warnungen",
  // Überbrückungs-Dialog (§12.1).
  "alarm.bypass.title": "Trotzdem scharf schalten?",
  "alarm.bypass.description":
    "Diese Sensoren blockieren das Scharfschalten. Wähle die zu überbrückenden aus und erzwinge das Schärfen — nichts wird stillschweigend überbrückt.",
  "alarm.bypass.force_arm": "Erzwingen",
  "alarm.bypass.empty": "Keine blockierenden Sensoren.",
  // Countdown (§12.1).
  "alarm.countdown.exit": "Austrittsverzögerung",
  "alarm.countdown.entry": "Eintrittsverzögerung",
  // Ausgelöst-Ansicht (§12.1).
  "alarm.triggered.intrusion": "ALARM — Einbruch",
  "alarm.triggered.cause": "Ausgelöst: {sensor} ({room}), {time}",
  "alarm.triggered.cause_short": "Ausgelöst durch {sensor}",
  "alarm.triggered.since": "seit {time}",
  "alarm.triggered.silenced": "Sirenen stumm",
  // Steuer-Aktionen (§5, §12.1).
  "alarm.action.disarm": "Unscharf",
  "alarm.action.silence": "Sirenen aus",
  "alarm.action.acknowledge": "Quittieren",
  "alarm.action.reset_motion": "Bewegung zurücksetzen ({count})",
  // Toasts.
  "alarm.toast.armed": "{zone} scharf ({mode})",
  "alarm.toast.arming": "{zone} wird scharf …",
  "alarm.toast.arm_failed": "Scharfschalten fehlgeschlagen",
  "alarm.toast.disarmed": "{zone} unscharf",
  "alarm.toast.disarm_failed": "Unscharf schalten fehlgeschlagen",
  "alarm.toast.silenced": "Sirenen aus",
  "alarm.toast.motion_reset": "Bewegungsmelder zurückgesetzt: {count}",
  "alarm.toast.motion_reset_none":
    "Kein ausgelöster Bewegungsmelder zum Zurücksetzen",
  "alarm.toast.motion_reset_partial":
    "Bewegung zurückgesetzt — erfolgreich: {reset}, fehlgeschlagen: {failed}",
  "alarm.toast.motion_reset_failed": "Zurücksetzen fehlgeschlagen",
  "alarm.toast.silence_failed": "Sirenen aus fehlgeschlagen",
  "alarm.toast.acknowledged": "Quittiert",
  "alarm.toast.ack_failed": "Quittieren fehlgeschlagen",
  "alarm.toast.saved": "Gespeichert",
  "alarm.toast.save_failed": "Speichern fehlgeschlagen",
  "alarm.toast.deleted": "Zone gelöscht",
  "alarm.toast.delete_failed": "Löschen fehlgeschlagen",
  "alarm.toast.test_fired": "Test ausgelöst",
  "alarm.toast.test_failed": "Test fehlgeschlagen",
  "alarm.toast.walktest_started": "Begehungstest gestartet",
  "alarm.toast.walktest_stopped": "Begehungstest beendet",
  // Zone anlegen / bearbeiten / löschen.
  "alarm.zone.name": "Name",
  // Sensor-Auswahl (§12.2).
  "alarm.sensors.empty": "Keine Sensoren zugewiesen",
  "alarm.sensors.empty.description":
    "Füge Sicherheitssensoren hinzu und wähle, in welchen Modi sie scharf sind.",
  "alarm.sensors.add": "Sensor hinzufügen",
  "alarm.sensors.search": "Suchen …",
  "alarm.sensors.selected": "{count} ausgewählt",
  "alarm.sensors.modes": "Modi",
  "alarm.sensors.filter.room": "Raum",
  "alarm.sensors.filter.function": "Gewerk",
  "alarm.sensors.filter.area": "Bereich",
  "alarm.sensors.filter.type": "Typ",
  "alarm.sensors.filter.status": "Status",
  "alarm.sensors.filter.all": "Alle",
  "alarm.sensors.filter.unassigned": "Nur nicht zugewiesene",
  "alarm.sensors.filter.assigned": "Nur zugewiesene",
  "alarm.sensors.view.cards": "Karten",
  "alarm.sensors.view.matrix": "Matrix",
  "alarm.sensors.bulk.assign": "Modus zuweisen",
  "alarm.sensors.bulk.remove": "Entfernen",
  "alarm.sensors.detail.title": "Sensor-Details",
  "alarm.sensors.state.unreach": "nicht erreichbar",
  // Sensortypen (§6.1).
  "alarm.sensor_type.door": "Tür",
  "alarm.sensor_type.window": "Fenster",
  "alarm.sensor_type.motion": "Bewegung",
  "alarm.sensor_type.tamper": "Sabotage",
  "alarm.sensor_type.hazard": "Gefahr",
  "alarm.sensor_type.panic": "Panik",
  // Sensor-Flags (§6.2).
  "alarm.flags.title": "Flags",
  "alarm.flag.use_exit_delay": "Austrittsverzögerung",
  "alarm.flag.use_exit_delay.hint":
    "Der Sensor darf beim Verlassen aktiv sein: Aktivierungen während der Austrittsverzögerung werden ignoriert. Ohne dieses Flag lösen sie sofort aus.",
  "alarm.flag.use_entry_delay": "Eintrittsverzögerung",
  "alarm.flag.use_entry_delay.hint":
    "Eine Aktivierung startet den Eintritts-Countdown statt sofort auszulösen — die Zeit zum Unscharfschalten nach dem Betreten.",
  "alarm.flag.entry_delay_override": "Eintrittsverzögerung überschreiben (s)",
  "alarm.flag.entry_delay_override.hint":
    "Ersetzt die Eintrittsverzögerung des Modus für diesen Sensor (Sekunden) — z. B. 60 fürs Garagentor, während die Haustür bei 15 bleibt. Leer nutzt den Modus-Standard.",
  "alarm.flag.always_on": "Immer aktiv",
  "alarm.flag.always_on.hint":
    "Löst rund um die Uhr aus, unabhängig vom Scharf-Zustand — für Gefahrensensoren (Rauch, Wasser, Gas) und Panik-Taster. Die Ausgänge folgen den Gefahren-/Panik-Richtlinien aus dem Reiter Richtlinien.",
  "alarm.flag.allow_open_after_arming": "Offen beim Schärfen erlaubt",
  "alarm.flag.allow_open_after_arming.hint":
    "Der Sensor darf beim Scharfschalten offen bleiben (z. B. ein gekipptes Fenster); erst eine neue Aktivierung nach dem Schließen löst aus.",
  "alarm.flag.arm_after_closing": "Schärfen beim Schließen",
  "alarm.flag.arm_after_closing.hint":
    "Schließt dieser Sensor während der Austrittsverzögerung, wird das Scharfschalten nach kurzer Beruhigungszeit vorzeitig abgeschlossen.",
  "alarm.flag.bypass_auto": "Automatisch überbrücken",
  "alarm.flag.bypass_auto.hint":
    "Würde dieser Sensor das Scharfschalten blockieren, wird er automatisch bis zum nächsten Unscharfschalten überbrückt, statt das Scharfschalten scheitern zu lassen; die Überbrückung wird protokolliert und angezeigt.",
  "alarm.flag.trigger_when_unavailable": "Auslösen bei Ausfall",
  "alarm.flag.trigger_when_unavailable.hint":
    "Wird der Sensor im scharfen Zustand unerreichbar, zählt das als Aktivierung. Ausgeschaltet gibt es nur eine Warnung.",
  "alarm.flag.chime": "Türgong bei unscharf",
  "alarm.flag.chime.hint":
    "Spielt den Türgong auf Signalton-Ausgängen, wenn dieser Sensor bei unscharfer Zone aktiviert wird — nie während eines Begehungstests.",
  "alarm.flag.panic_silent": "Stiller Panik-Alarm",
  "alarm.flag.panic_silent.hint":
    "Aktivierungen feuern die Panik-Richtlinie ohne akustische Ausgänge — nur Benachrichtigungen. Für Panik-Taster, die vor Ort lautlos bleiben müssen.",
  "alarm.flag.hold_time": "Haltezeit (s)",
  "alarm.flag.hold_time.hint":
    "Die Aktivierung muss so viele Sekunden anstehen, bevor sie zählt — filtert zappelige Bewegungsmelder und klappernde Türen. Wird der Sensor vorher wieder inaktiv, verfällt die Aktivierung. Gilt nie für Immer-aktiv-Sensoren (Gefahr/Panik).",
  "alarm.flag.group": "Verbund-Gruppe",
  "alarm.flag.group.hint":
    "Sensoren mit demselben Gruppennamen lösen erst aus, wenn ein zweiter Sensor der Gruppe binnen 60 Sekunden aktiviert wird. Eine Einzelaktivierung löst keinen Alarm aus, wird aber im Journal protokolliert.",
  "alarm.matrix.sensor": "Sensor",
  // Ausgangs-Auswahl (§7, §12.2).
  "alarm.outputs.empty": "Keine Ausgänge zugewiesen",
  "alarm.outputs.empty.description":
    "Füge Sirenen oder Lichter hinzu und ordne sie pro Modus laut/still zu.",
  "alarm.outputs.add": "Ausgang hinzufügen",
  "alarm.outputs.expert": "Expertenmodus",
  "alarm.outputs.expert.hint":
    "Alle modellierten Aktoren anzeigen, nicht nur kuratierte Sirenen/Lichter.",
  "alarm.outputs.test": "Test auslösen",
  "alarm.outputs.test_optical_only": "Nur optisch",
  "alarm.outputs.test_optical_only.hint": "Testet nur mit Licht — ohne Ton.",
  "alarm.outputs.test.confirm.title": "Ausgang testen?",
  "alarm.outputs.test.confirm.body":
    "Das aktiviert kurz das echte Gerät (Sirene/Licht). Nur optisch schont die Nachbarn.",
  "alarm.outputs.outdoor": "Außen",
  "alarm.outputs.outdoor.hint":
    "Markiert diesen Ausgang als außen, sodass Richtlinien mit „Außenausgänge ausschließen“ ihn überspringen.",
  "alarm.outputs.shared_with_ccu": "Von CCU-Programmen genutzt",
  "alarm.outputs.shared_with_ccu.hint":
    "Der Ausgang wird auch von CCU-Programmen geschaltet: Loom schaltet ihn bei unscharfer Zone nie automatisch ab.",
  "alarm.outputs.duration": "Dauer (s)",
  "alarm.outputs.duration.hint":
    "Sekunden, die eine Aktivierung läuft; akustische Aktivierungen sind hart auf 600 s begrenzt. Leer nutzt den begrenzten Standard.",
  "alarm.outputs.tone": "Ton",
  "alarm.outputs.tone.hint":
    "Ton-Bezeichner aus der Tonliste des Geräts. Leer spielt den Standard-Alarmton des Geräts.",
  "alarm.outputs.optical_pattern": "Optisches Muster",
  "alarm.outputs.optical_pattern.hint":
    "Lichtmuster-Bezeichner aus der Liste des Geräts. Leer nutzt den Geräte-Standard.",
  "alarm.outputs.switched_caveat":
    "Komfort-Klasse: kein Sabotagekontakt, keine Batteriepufferung, leicht ausgesteckt.",
  "alarm.outputs.smoke_caveat":
    "Rauchmelder dienen zusätzlich als Sirenen — keine geräteseitige Dauer, nur per Engine überwacht, und wiederholte Alarmtöne verkürzen die Batterielaufzeit. Am besten nur im Vollschutz.",
  "alarm.outputs.device_default": "Geräte-Standard",
  "alarm.outputs.channel_mismatch":
    "Dieser Kanal kann die gewählte Klasse nicht tragen — die Zuordnung stammt aus einer Version ohne Kanalprüfung und hat nie ausgelöst. Speichern ist blockiert, bis sie repariert oder entfernt ist.",
  "alarm.outputs.channel_mismatch.repair": "Kanal reparieren",
  "alarm.outputs.candidates.empty":
    "Keine geeigneten Kanäle für diese Klasse. Der Expertenmodus listet alle Geräte ohne Fähigkeitsfilter.",
  "alarm.outputs.candidates.load_failed":
    "Laden der Ausgangs-Kandidaten fehlgeschlagen",
  "alarm.outputs.soundfile": "Sounddatei",
  "alarm.outputs.soundfile.hint":
    "MP3-Sounddatei für Signaltöne. Leer nutzt den Geräte-Standard.",
  "alarm.outputs.sysvar.central": "Zentrale",
  "alarm.outputs.sysvar.central.hint":
    "CCU, auf der die Variable liegt — der Spiegel schreibt sie dort (und legt verwaltete Variablen dort an).",
  "alarm.outputs.channel.hint":
    "Kanaladresse als <Gerät>:<Kanal>, z. B. 0001D3C9A4B2:3.",
  "alarm.outputs.chirp_arm_tone.hint":
    "Ton-Bezeichner für den Scharf-Quittungston, aus der Tonliste des Geräts. Leer überspringt ihn auf diesem Ausgang.",
  "alarm.outputs.chirp_disarm_tone.hint":
    "Ton-Bezeichner für den Unscharf-Quittungston. Leer überspringt ihn auf diesem Ausgang.",
  "alarm.outputs.sysvar.name": "Variablenname",
  "alarm.outputs.sysvar.name.hint":
    "Wird auf der CCU automatisch als Werteliste-Variable angelegt und spiegelt den Zonenzustand (Unscharf … Alarm).",
  "alarm.outputs.sysvar.existing": "Bestehende Alarm-Variable verwenden",
  "alarm.outputs.sysvar.existing.hint":
    "Beschreibt eine eigene Variable vom Typ Alarm: wahr bei ausgelöstem Alarm, sonst falsch. Die Variable wird nie angelegt oder umtypisiert und nimmt keine eingehenden Befehle an.",
  "alarm.outputs.sysvar.existing.badge": "bestehend",
  "alarm.outputs.sysvar.pick": "Alarm-Variable",
  "alarm.outputs.sysvar.none":
    "Keine Variablen vom Typ Alarm auf dieser Zentrale.",
  "alarm.outputs.sysvar.load_failed":
    "Laden der Systemvariablen fehlgeschlagen",
  "alarm.outputs.sysvar.allow_disarm": "Unscharf über Variable erlauben",
  "alarm.outputs.sysvar.allow_disarm.hint":
    "Aus (Standard): ein CCU-Schreibzugriff kann nur scharf schalten — nie unscharf. Nur aktivieren, wenn jedes CCU-Programm mit Schreibzugriff vertrauenswürdig ist.",
  "alarm.outputs.notification.note":
    "Sendet bei Alarm ein Benachrichtigungs-Ereignis an die gewählten Kanäle — ohne Gerät. Die Kanäle werden nach dem Hinzufügen auf der Karte konfiguriert.",
  "alarm.outputs.notify.mqtt": "MQTT-Ereignis",
  "alarm.outputs.notify.mqtt.hint":
    "Veröffentlicht einen NOTIFICATION-Eintrag auf dem MQTT-Alarm-Ereignis-Topic der Zone.",
  "alarm.outputs.notify.webhook": "Webhook-Ereignis",
  "alarm.outputs.notify.webhook.hint":
    "Leitet ein alarm_panel.notification-Ereignis an die ausgehenden Webhook-Empfänger weiter.",
  // Ausgangsklassen (§7).
  "alarm.output_class.acoustic_siren": "Akustische Sirene",
  "alarm.output_class.acoustic_siren.hint":
    "Ein echtes Sirenengerät (z. B. HmIP-ASIR): Ton und Dauer einstellbar, jede Aktivierung von der Engine begrenzt und mit verifiziertem Stopp.",
  "alarm.output_class.switched_siren": "Steckdosen-Sirene",
  "alarm.output_class.switched_siren.hint":
    "Eine Netzstecker-Sirene hinter einem Schaltaktor. Komfort-Klasse: kein Sabotagekontakt, keine Batteriepufferung, leicht ausgesteckt; der Aktor muss geräteseitiges Auto-Aus (ON_TIME) unterstützen.",
  "alarm.output_class.smoke_sounder": "Rauchmelder-Sirene",
  "alarm.output_class.smoke_sounder.hint":
    "Lässt eingebundene Rauchmelder bei Einbruchalarm ertönen. Kostet Melder-Batterie, löst meist die ganze Gruppe aus und bietet keinen Live-Test.",
  "alarm.output_class.optical_siren": "Optische Sirene",
  "alarm.output_class.optical_siren.hint":
    "Der optische Kanal einer Sirene — signalisiert ohne Lärm und darf länger laufen als die akustische Begrenzung.",
  "alarm.output_class.alarm_light": "Alarm-Licht",
  "alarm.output_class.alarm_light.hint":
    "Ein Schalt- oder Dimmaktor als Alarm-Licht: an bei Auslösung, aus bei Stummschalten oder Unscharfschalten.",
  "alarm.output_class.chirp": "Signalton",
  "alarm.output_class.chirp.hint":
    "Nur kurze Quittungstöne: Scharf-/Unscharf-Bestätigung, Countdown-Ticks und Türgong — nie der laute Alarm.",
  "alarm.output_class.notification": "Benachrichtigung",
  "alarm.output_class.notification.hint":
    "Sendet bei Alarm ein gezieltes Benachrichtigungs-Ereignis (MQTT, WebSocket, Webhook) — einmalig beim Auslösen, wird durch Stummschalten nie abgebrochen. Jeder Kanal ist pro Ausgang schaltbar.",
  "alarm.output_class.sysvar_mirror": "Systemvariable",
  "alarm.output_class.sysvar_mirror.hint":
    "Pflegt eine CCU-Systemvariable, die den Alarmzustand spiegelt — entweder eine verwaltete Werteliste-Variable (wird automatisch angelegt) oder eine bestehende Variable vom Typ Alarm (wahr bei ausgelöstem Alarm).",
  // Journal (§12.5).
  "alarm.journal.empty": "Keine Journaleinträge",
  "alarm.journal.filter.zone": "Zone",
  "alarm.journal.filter.class": "Kategorie",
  "alarm.journal.filter.from": "Von",
  "alarm.journal.filter.to": "Bis",
  "alarm.journal.filter.all": "Alle",
  "alarm.journal.export_csv": "CSV exportieren",
  "alarm.journal.col.when": "Zeit",
  "alarm.journal.col.zone": "Zone",
  "alarm.journal.col.class": "Kategorie",
  "alarm.journal.col.event": "Ereignis",
  "alarm.journal.col.actor": "Von",
  "alarm.journal.col.source": "Quelle",
  // Journal-Kategorien (§13.1).
  "alarm.journal_class.arm": "Scharf",
  "alarm.journal_class.disarm": "Unscharf",
  "alarm.journal_class.trigger": "Auslösung",
  "alarm.journal_class.silence": "Stumm",
  "alarm.journal_class.bypass": "Überbrückung",
  "alarm.journal_class.fault": "Störung",
  "alarm.journal_class.test": "Test",
  "alarm.journal_class.config": "Konfiguration",
  "alarm.journal_class.maintenance": "Wartung",
  "alarm.journal_event.acknowledged": "Quittiert",
  "alarm.journal_event.acoustic_budget_exhausted":
    "Akustik-Budget aufgebraucht",
  "alarm.journal_event.activation_during_downtime":
    "Aktiviert, während der Dienst aus war",
  "alarm.journal_event.always_on_activation":
    "Dauerüberwachter Sensor ausgelöst",
  "alarm.journal_event.arm_failed_on_restore":
    "Scharfschalten beim Wiederherstellen fehlgeschlagen",
  "alarm.journal_event.arm_reminder": "Erinnerung zum Scharfschalten",
  "alarm.journal_event.armed": "Scharf geschaltet",
  "alarm.journal_event.armed_after_closing":
    "Scharf geschaltet, nachdem alles geschlossen war",
  "alarm.journal_event.arming_resumed": "Scharfschaltung fortgesetzt",
  "alarm.journal_event.arming_started": "Scharfschaltung gestartet",
  "alarm.journal_event.auto_rearm_cancelled":
    "Automatische Wiederscharfschaltung abgebrochen",
  "alarm.journal_event.auto_rearm_deferred":
    "Automatische Wiederscharfschaltung verschoben",
  "alarm.journal_event.auto_rearm_failed":
    "Automatische Wiederscharfschaltung fehlgeschlagen",
  "alarm.journal_event.auto_rearm_mode_unavailable":
    "Modus für die automatische Wiederscharfschaltung nicht verfügbar",
  "alarm.journal_event.auto_rearm_resumed":
    "Automatische Wiederscharfschaltung fortgesetzt",
  "alarm.journal_event.auto_rearm_scheduled":
    "Automatische Wiederscharfschaltung geplant",
  "alarm.journal_event.auto_rearmed": "Automatisch wieder scharf geschaltet",
  "alarm.journal_event.central_lost_while_armed":
    "Verbindung zur CCU im scharfen Zustand verloren",
  "alarm.journal_event.central_restored": "CCU wieder erreichbar",
  "alarm.journal_event.code_action_failed": "Code-Aktion fehlgeschlagen",
  "alarm.journal_event.code_locked_out": "Code-Eingabe gesperrt",
  "alarm.journal_event.code_lockout": "Code-Sperre gestartet",
  "alarm.journal_event.code_missing": "Code erforderlich",
  "alarm.journal_event.code_permission_denied":
    "Code für diese Aktion nicht berechtigt",
  "alarm.journal_event.cross_zone_first_hit":
    "Erste Auslösung über Bereichsgrenze",
  "alarm.journal_event.disarmed": "Unscharf geschaltet",
  "alarm.journal_event.disarmed_post_trigger":
    "Nach einem Alarm unscharf geschaltet",
  "alarm.journal_event.duress": "Bedrohungscode eingegeben",
  "alarm.journal_event.failed_to_arm": "Scharfschalten fehlgeschlagen",
  "alarm.journal_event.implausible_clock_on_restore":
    "Unplausible Uhrzeit beim Wiederherstellen",
  "alarm.journal_event.incident_load_failed":
    "Vorfall konnte nicht geladen werden",
  "alarm.journal_event.incident_lost_on_restore":
    "Vorfall beim Wiederherstellen verloren",
  "alarm.journal_event.incident_persist_failed":
    "Vorfall konnte nicht gespeichert werden",
  "alarm.journal_event.invalid_code": "Ungültiger Code eingegeben",
  "alarm.journal_event.keypad_blocked": "Bedienteil gesperrt",
  "alarm.journal_event.keypad_press_unmatched":
    "Eingabe am Bedienteil ohne Treffer",
  "alarm.journal_event.mode_removed_while_armed":
    "Scharfer Modus aus der Konfiguration entfernt",
  "alarm.journal_event.motion_reset": "Bewegungsmelder zurückgesetzt",
  "alarm.journal_event.orphan_incident_adopted":
    "Verwaisten Vorfall übernommen",
  "alarm.journal_event.orphan_incident_closed":
    "Verwaisten Vorfall geschlossen",
  "alarm.journal_event.output_fire_failed":
    "Signalgeber konnte nicht ausgelöst werden",
  "alarm.journal_event.output_stop_failed":
    "Signalgeber konnte nicht gestoppt werden",
  "alarm.journal_event.output_stop_unverified":
    "Stopp des Signalgebers nicht bestätigt",
  "alarm.journal_event.pending_demoted_implausible_clock":
    "Eintrittsverzögerung verworfen: unplausible Uhrzeit",
  "alarm.journal_event.pending_elapsed_while_down":
    "Eintrittsverzögerung abgelaufen, während der Dienst aus war",
  "alarm.journal_event.pending_resumed": "Eintrittsverzögerung fortgesetzt",
  "alarm.journal_event.pending_started": "Eintrittsverzögerung gestartet",
  "alarm.journal_event.pre_alarm_escalated":
    "Voralarm zum Vollalarm eskaliert",
  "alarm.journal_event.pre_alarm_restored_as_full":
    "Voralarm als Vollalarm wiederhergestellt",
  "alarm.journal_event.reconcile_stopped_unowned_siren":
    "Fremde laufende Sirene gestoppt",
  "alarm.journal_event.refire_account_failed":
    "Erneutes Auslösen konnte nicht verbucht werden",
  "alarm.journal_event.restart_loop_breaker_degraded":
    "Neustartschleifen-Schutz ausgelöst",
  "alarm.journal_event.retrigger_account_failed":
    "Erneute Auslösung konnte nicht verbucht werden",
  "alarm.journal_event.retrigger_cycle": "Erneute Auslösung",
  "alarm.journal_event.schedule_arm_failed":
    "Geplantes Scharfschalten fehlgeschlagen",
  "alarm.journal_event.sensor_activity": "Sensoraktivität",
  "alarm.journal_event.sensor_activity_pending":
    "Sensoraktivität während der Eintrittsverzögerung",
  "alarm.journal_event.sensor_bypassed": "Sensor überbrückt",
  "alarm.journal_event.sensor_config_unparseable":
    "Sensorkonfiguration nicht lesbar",
  "alarm.journal_event.sensor_sabotage": "Sabotage am Sensor",
  "alarm.journal_event.sensor_unavailable_while_armed":
    "Sensor im scharfen Zustand nicht erreichbar",
  "alarm.journal_event.silence_persist_failed":
    "Stummschaltung konnte nicht gespeichert werden",
  "alarm.journal_event.silence_requested": "Stummschaltung angefordert",
  "alarm.journal_event.silenced": "Stummgeschaltet",
  "alarm.journal_event.silenced_incident_restored":
    "Stummgeschalteter Vorfall wiederhergestellt",
  "alarm.journal_event.sounding_siren_adopted": "Laufende Sirene übernommen",
  "alarm.journal_event.state_persist_failed":
    "Zustand konnte nicht gespeichert werden",
  "alarm.journal_event.sysvar_arm_failed":
    "Scharfschalten per Systemvariable fehlgeschlagen",
  "alarm.journal_event.sysvar_disarm_failed":
    "Unscharfschalten per Systemvariable fehlgeschlagen",
  "alarm.journal_event.sysvar_disarm_refused":
    "Unscharfschalten per Systemvariable abgelehnt",
  "alarm.journal_event.sysvar_intent_ambiguous":
    "Mehrdeutiger Befehl per Systemvariable",
  "alarm.journal_event.tamper_while_disarmed":
    "Sabotage im unscharfen Zustand",
  "alarm.journal_event.trigger_window_elapsed_while_down":
    "Alarmfenster abgelaufen, während der Dienst aus war",
  "alarm.journal_event.triggered": "Ausgelöst",
  "alarm.journal_event.triggered_restored": "Alarm wiederhergestellt",
  "alarm.journal_event.triggered_restored_implausible_clock":
    "Alarm mit unplausibler Uhrzeit wiederhergestellt",
  "alarm.journal_event.unknown_persisted_state":
    "Unbekannter gespeicherter Zustand",
  "alarm.journal_event.walktest_finished": "Begehungstest beendet",
  "alarm.journal_event.walktest_sensor_seen":
    "Sensor im Begehungstest erkannt",
  "alarm.journal_event.walktest_started": "Begehungstest gestartet",
  "alarm.journal_event.zone_config_unparseable":
    "Bereichskonfiguration nicht lesbar",
  "alarm.journal_event.zone_removed_while_armed":
    "Scharfer Bereich aus der Konfiguration entfernt",
  // Begehungstest (§12.4).
  "alarm.walktest.start": "Test starten",
  "alarm.walktest.stop": "Test beenden",
  "alarm.walktest.active": "Test läuft",
  "alarm.walktest.inactive": "Kein Test aktiv",
  "alarm.walktest.select_zone": "Zone wählen",
  "alarm.walktest.progress": "{seen}/{total} Sensoren geprüft",
  "alarm.walktest.tested": "geprüft",
  "alarm.walktest.untested": "ausstehend",
  "alarm.walktest.empty": "Keine Sensoren in dieser Zone.",
  // Einrichtungsassistent (§12.3).
  "alarm.wizard.launch": "Einrichtungsassistent",
  "alarm.wizard.step.zones": "Zonen",
  "alarm.wizard.step.sensors": "Sensoren",
  "alarm.wizard.step.outputs": "Ausgänge",
  "alarm.wizard.step.delays": "Verzögerungen & Töne",
  "alarm.wizard.step.codes": "Codes & Benutzer",
  "alarm.wizard.step.done": "Fertig",
  "alarm.wizard.next": "Weiter",
  "alarm.wizard.back": "Zurück",
  "alarm.wizard.skip": "Überspringen",
  "alarm.wizard.finish": "Fertigstellen",
  "alarm.wizard.codes_later":
    "PIN-Codes und Funkschlüssel werden im Codes-Tab verwaltet, sobald diese Zone existiert — hier gibt es noch nichts einzurichten.",
  // Zustands-Chip (§12.5, S7).
  "alarm.health.healthy": "Alarmanlage OK",
  "alarm.health.unhealthy": "Alarmanlage gestört",
  // Per-tab intro lines rendered by the alarm section shell under the
  // tab bar — one orientation sentence per view.
  "alarm.intro.overview":
    "Schalte jede Zone scharf oder unscharf und behandle einen ausgelösten Alarm. Stummschalten stoppt die Sirenen, lässt den Vorfall aber offen; Unscharfschalten beendet ihn; Quittieren markiert ihn nur als gesehen.",
  "alarm.intro.sensors":
    "Wähle, welche Sensoren jede Zone überwachen und in welchen Scharf-Modi sie zählen. Die Detailansicht stellt das Verhalten pro Sensor ein, etwa Eintrittsverzögerung und Überbrückung; die Matrix-Ansicht ist der schnellste Weg, viele Sensoren auf einmal zu prüfen.",
  "alarm.intro.outputs":
    "Binde Sirenen, Lichter, Signaltöne und Benachrichtigungsziele als Alarmfolgen ein und stelle Ton, Dauer und Modus-Zuordnung pro Ausgang ein. Jeder Ausgang lässt sich kurz testen; die Option „nur optisch“ schont die Nachbarn.",
  "alarm.intro.policies":
    "Regeln pro Zone jenseits von Scharf/Unscharf: wann ein Code verlangt wird, welche Ausgänge Gefahren- und Panik-Auslöser rund um die Uhr feuern, wie ein Voralarm die Eskalation abmildert und was nach dem Ende einer Auslösephase passiert.",
  "alarm.intro.codes":
    "PIN-Codes, Codetastatur-Slots und Funkschlüssel, die die Alarmanlage scharf-/unscharfschalten oder stummschalten können — unabhängig von Login-Konten, z. B. für Haushaltsmitglieder ohne Zugang zu dieser Oberfläche.",
  "alarm.intro.journal":
    "Das dauerhafte Protokoll von allem, was die Alarm-Engine tut oder beobachtet — Scharfschaltungen, Auslösungen, Überbrückungen, Störungen und Tests. Filtere nach Zone, Kategorie und Zeitraum oder exportiere die aktuelle Ansicht als CSV.",
  "alarm.intro.walktest":
    "Testet Sensoren, ohne die Zone scharf zu schalten: Starte eine Sitzung, gehe durchs Haus und löse jeden Sensor aus — jede Aktivierung färbt ihre Zeile grün, und es wird kein Alarm ausgelöst. Das Ergebnis landet im Journal.",
  "alarm.outputs.field.class": "Ausgabeklasse",
  "alarm.outputs.level": "Dimmerstufe (0–1)",
  "alarm.outputs.level.hint":
    "Dimmstufe für aktorbasierte Ausgänge, 0–1. Leer behält die letzte Stufe des Geräts.",
  "alarm.sensors.add.no_devices": "Keine passenden Gerätekanäle gefunden.",
  "alarm.sensors.add.show_all": "Alle Kanäle anzeigen",
  "alarm.sensors.zone": "Zone",
  "alarm.zone_switch.discard.title": "Ungespeicherte Änderungen",
  "alarm.zone_switch.discard.body":
    "Ihre ungespeicherten Änderungen für diese Zone gehen verloren, wenn Sie zu einer anderen Zone wechseln. Trotzdem wechseln?",
  "alarm.zone_switch.discard.confirm": "Verwerfen und wechseln",
  "alarm.sensors.field.channel": "Kanaladresse",
  "alarm.sensors.field.device": "Gerät",
  "alarm.sensors.field.name": "Name",
  "alarm.sensors.field.parameter": "Parameter",
  "alarm.sensors.select_all": "Alle gefilterten auswählen",
  "alarm.toast.walktest_start_failed":
    "Begehungstest konnte nicht gestartet werden",
  "alarm.toast.walktest_stop_failed":
    "Begehungstest konnte nicht beendet werden",
  "alarm.wizard.zone.default_name": "Erdgeschoss",
  "alarm.wizard.zone.hint":
    "Eine Zone ist eine unabhängig scharfschaltbare Einheit — zum Beispiel eine pro Etage.",
  "alarm.wizard.delay.entry": "Eintrittsverzögerung (s)",
  "alarm.wizard.delay.exit": "Austrittsverzögerung (s)",
  "alarm.wizard.delay.trigger": "Alarmdauer (s)",
  "alarm.wizard.delays.hint":
    "Die Austrittsverzögerung lässt Zeit zum Verlassen nach dem Scharfschalten; die Eintrittsverzögerung gibt Zeit zum Unscharfschalten nach dem Öffnen der Tür. Die Alarmdauer begrenzt, wie lange eine Alarmphase (und ihre Sirenen) läuft — höchstens 600 s pro Zyklus.",
  "alarm.wizard.finish.hint":
    "Die Zone wird unscharf angelegt. Führe vor dem Verlassen auf die Anlage einen Begehungstest durch.",
  "alarm.wizard.outputs.empty": "Keine geeigneten Ausgangskanäle gefunden.",
  "alarm.wizard.outputs.empty.description":
    "Nutze anschließend den Ausgaben-Tab für die Aufnahme im Expertenmodus für jedes Gerät.",
  "alarm.wizard.outputs.hint":
    "Wähle unten die Sirenen, Leuchten und weiteren Ausgänge aus, die aufgenommen werden sollen — Ton, Dauer und Modus-Zuordnung feintunst du anschließend im Ausgaben-Tab.",
  "alarm.wizard.sensors.empty": "Keine passenden Geräte gefunden.",
  "alarm.wizard.sensors.empty.description":
    "Versuche eine andere Suche oder aktiviere oben den Alle-anzeigen-Schalter, um die Kandidatenliste über sicherheitsrelevante Geräte hinaus zu erweitern.",
  "alarm.wizard.sensors.hint":
    "Wähle unten die Tür-, Fenster- und Bewegungssensoren aus, die aufgenommen werden sollen — nach Name oder Adresse suchen oder alle Geräte anzeigen.",
  "alarm.wizard.sort.name": "Name",
  "alarm.wizard.sort.room": "Raum",
  "alarm.wizard.sort.model": "Modell",
  "alarm.wizard.summary.delay_line": "{mode} {exit}/{entry}/{trigger}s",
  "alarm.wizard.summary.delays": "Verzögerungen",
  // Alarm codes (notes/concepts/alarm-concept.md §11).
  "alarm.codes.add": "Code hinzufügen",
  "alarm.codes.zones": "Zonen",
  "alarm.codes.zones.all": "Alle Zonen",
  "alarm.codes.delete.confirm.body":
    'Code "{name}" löschen? Das lässt sich nicht rückgängig machen.',
  "alarm.codes.delete.confirm.title": "Code löschen?",
  "alarm.codes.disabled": "Deaktiviert",
  "alarm.codes.duress.badge": "Duress",
  "alarm.codes.duress.warning":
    "Ein Zwangs-Code (Duress) schaltet die Zone wie ein normaler Code unscharf – am Panel ist kein Unterschied sichtbar –, löst aber im Hintergrund still ein Duress-Ereignis an die konfigurierten Benachrichtigungsziele aus. Im sichtbaren Journal erscheint bis zur Klärung nichts; das vollständige Audit-Protokoll wird intern weitergeführt. Diesen Code niemals leichtfertig weitergeben.",
  "alarm.codes.edit": "Code bearbeiten",
  "alarm.codes.empty": "Noch keine Codes",
  "alarm.codes.empty.description":
    "Füge einen PIN-Code, einen Codetastatur-Slot oder einen Funkschlüssel hinzu, damit sich die Anlage scharf-/unscharfschalten oder stummschalten lässt.",
  "alarm.codes.error.binding_json": "Bindung muss gültiges JSON sein.",
  "alarm.codes.error.name_required": "Name ist erforderlich.",
  "alarm.codes.error.pin_required":
    "Für einen neuen PIN-Code ist ein PIN-Code erforderlich.",
  "alarm.codes.field.zones": "Zonen",
  "alarm.codes.field.zones.help":
    "Wähle aus, für welche Zonen dieser Code gilt. Lässt du alle Kästchen leer, gilt er für alle Zonen.",
  "alarm.codes.field.binding": "Hardware-Bindung",
  "alarm.codes.field.binding.help":
    "Rohes JSON zur physischen Bindung dieser Code-Art — z. B. die Kanaladresse der Codetastatur oder der Tastenkanal des Funkschlüssels. Leer lassen für keine Bindung.",
  "alarm.codes.field.duress": "Stiller Notfall-Code (Duress)",
  "alarm.codes.field.enabled": "Aktiviert",
  "alarm.codes.field.kind": "Art",
  "alarm.codes.field.kind.hint":
    "Ein PIN wird am PIN-Pad oder auf anonymen Wegen eingegeben; Codetastatur-Slot und Funkschlüssel binden einen Hardware-Slot bzw. eine Funkfernbedienung, sodass deren Aktionen unter diesem Namen laufen.",
  "alarm.codes.field.name": "Name",
  "alarm.codes.field.pin": "PIN-Code",
  "alarm.codes.field.pin.help":
    "4–8-stelliger PIN-Code, wird als gesalzener Hash gespeichert – der Daemon gibt ihn nie wieder aus.",
  "alarm.codes.field.pin.keep":
    "Leer lassen, um den aktuellen PIN-Code zu behalten",
  "alarm.codes.field.pin.placeholder": "PIN-Code eingeben",
  "alarm.codes.field.valid_from": "Gültig ab",
  "alarm.codes.field.valid_until": "Gültig bis",
  "alarm.codes.field.validity.help":
    "Beide Felder leer lassen für einen Code ohne Ablaufdatum.",
  "alarm.codes.kind.keypad_slot": "Codetastatur-Slot",
  "alarm.codes.kind.pin": "PIN-Code",
  "alarm.codes.kind.remote_key": "Funkschlüssel",
  "alarm.codes.remote.key": "Funktaste",
  "alarm.codes.remote.expert": "Roh-JSON",
  "alarm.codes.remote.expert.hint":
    "Das Binding-Dokument direkt bearbeiten — nötig für virtuelle Fernbedienungskanäle oder Sonderfälle.",
  "alarm.codes.remote.no_candidates":
    "Keine Funk- oder Wandtaster-Tasten gefunden. Erst die Fernbedienung anlernen oder Roh-JSON verwenden.",
  "alarm.codes.remote.alarm_keyfob": "Alarm-Fernbedienung",
  "alarm.codes.remote.candidates_failed": "Laden der Funktasten fehlgeschlagen",
  "alarm.codes.remote.parameter": "Auslöser",
  "alarm.codes.remote.parameter.hint":
    "Welcher Druck der gebundenen Taste die Aktion auslöst — kurz oder lang.",
  "alarm.codes.remote.param.press_short": "Kurzer Tastendruck",
  "alarm.codes.remote.param.press_long": "Langer Tastendruck",
  "alarm.codes.remote.action": "Aktion",
  "alarm.codes.remote.action.hint":
    "Was die Taste tut: in einen Modus scharf schalten, unscharf schalten, stummschalten oder Panik.",
  "alarm.codes.remote.zone.hint": "Alarmzone, auf die die Aktion wirkt.",
  "alarm.codes.remote.action.arm": "Scharf",
  "alarm.codes.remote.action.disarm": "Unscharf",
  "alarm.codes.remote.action.silence": "Stummschalten",
  "alarm.codes.remote.action.panic": "Panik",
  "alarm.codes.remote.zone": "Zone",
  "alarm.codes.error.remote_incomplete":
    "Funktaste, Auslöser, Aktion und Zone auswählen.",
  "alarm.codes.perm.arm": "Scharf schalten",
  "alarm.codes.perm.disarm": "Unscharf schalten",
  "alarm.codes.perm.silence": "Sirenen aus",
  "alarm.codes.perms": "Berechtigungen",
  "alarm.codes.perms.hint":
    "Was dieser Code darf: scharf schalten, unscharf schalten und Sirenen stummschalten.",
  "alarm.codes.unavailable": "Alarm-Codes nicht verfügbar",
  "alarm.codes.unavailable.description":
    "Das Alarm-Code-Subsystem ist auf diesem Daemon nicht eingerichtet.",
  "alarm.codes.validity.open": "Unbegrenzt",
  // Chirp tone labels (notes/concepts/alarm-concept.md §15 row 23). The driver
  // reads three tone labels: arm squawk, disarm squawk, and the tick
  // tone (countdown ticks, entry warning, and the door chime).
  "alarm.outputs.chirp_arm_tone": "Quittungston Scharf",
  "alarm.outputs.chirp_disarm_tone": "Quittungston Unscharf",
  "alarm.outputs.chirp_tick_tone": "Tick- & Türgong-Ton",
  "alarm.outputs.chirp_tick_tone.hint":
    "Für Countdown-Ticks, Eintrittswarnung und Türgong. Ein leerer Ton-Bezeichner überspringt diese Ton-Art auf diesem Ausgang.",
  // PIN pad (notes/concepts/alarm-concept.md §12.1).
  "alarm.pinpad.arm_title": "Code zum Scharfschalten eingeben — {mode}",
  "alarm.pinpad.backspace": "Rücktaste",
  "alarm.pinpad.clear": "Löschen",
  "alarm.pinpad.digit": "Ziffer {digit}",
  "alarm.pinpad.disarm_title": "Code zum Unscharfschalten von {zone} eingeben",
  "alarm.pinpad.entered": "{count} Ziffern eingegeben",
  "alarm.pinpad.placeholder": "Code eingeben",
  "alarm.pinpad.title": "Code eingeben",
  // Policy editor (notes/concepts/alarm-concept.md §11, §15 rows 19/21/22).
  "alarm.policies.code.hint":
    "Operator-Sitzungen (REST, WebSocket, hmcli) umgehen diese Prüfungen immer — der dokumentierte Break-Glass-Pfad. Ein dabei eingegebener Zwangs-Code löst aber trotzdem einen stillen Alarm aus.",
  "alarm.policies.code.require_arm": "Code zum Scharfschalten erforderlich",
  "alarm.policies.code.require_arm.hint":
    "Verlangt einen gültigen Code, bevor die Zone scharf schaltet. Standardmäßig aus — Scharfschalten ist die sichere Richtung und bleibt ein Fingertipp.",
  "alarm.policies.code.require_disarm":
    "Code zum Unscharfschalten erforderlich",
  "alarm.policies.code.require_disarm.always": "Immer",
  "alarm.policies.code.require_disarm.default":
    "Automatisch (an, sobald Codes existieren)",
  "alarm.policies.code.require_disarm.hint":
    "Automatisch verlangt einen Code, sobald für diese Zone ein aktiver Code existiert. Eine Zone ohne Codes verlangt nie einen — ein Aussperren ist damit ausgeschlossen.",
  "alarm.policies.code.require_disarm.never": "Nie",
  "alarm.policies.code.require_silence": "Code zum Stummschalten erforderlich",
  "alarm.policies.code.require_silence.hint":
    "Gilt nur für anonyme Eingabewege — MQTT, Codetastatur und Funkschlüssel. Authentifizierte Operator-Sitzungen umgehen diese Prüfung immer.",
  "alarm.policies.code.source.keypad": "Codetastatur",
  "alarm.policies.code.source.mqtt": "MQTT",
  "alarm.policies.code.source.remote": "Funkschlüssel",
  "alarm.policies.output.exclude_outdoor": "Außenausgänge ausschließen",
  "alarm.policies.output.exclude_outdoor.hint":
    "Überspringt als außen markierte Ausgänge (z. B. eine Außensirene); Innen-Ausgänge feuern weiterhin.",
  "alarm.policies.output.silent": "Lautlos (keine Sirene)",
  "alarm.policies.output.silent.hint":
    "Unterdrückt alle akustischen Ausgänge dieser Richtlinie — Benachrichtigungen, optische Signale und Alarm-Licht feuern weiterhin.",
  "alarm.policies.output.smoke_sounders": "Rauchmelder-Sirenen einbeziehen",
  "alarm.policies.output.smoke_sounders.hint":
    "Lässt zusätzlich die eingebundenen Rauchmelder-Sirenen ertönen. Bewusst einsetzen: Jede Aktivierung kostet unwiederbringlich Melder-Batterie und löst meist die ganze Rauchmelder-Gruppe aus.",
  "alarm.policies.posttrigger": "Wenn die Auslösephase endet",
  "alarm.policies.posttrigger.disarm": "Unscharf schalten",
  "alarm.policies.posttrigger.hint":
    "Eine Auslösephase ist immer zeitlich begrenzt (Standard 180 s, höchstens 600 s pro Zyklus); Sirenen stoppen an ihrem Ende in jedem Fall. Diese Einstellung bestimmt, was die Zone danach tut: im vorherigen Modus scharf bleiben oder unscharf schalten.",
  "alarm.policies.posttrigger.return_to_armed": "Zurück zu scharf",
  "alarm.policies.prealarm.empty":
    "Für diese Zone sind noch keine Modi eingerichtet — lege sie zuerst im Einrichtungsassistenten an.",
  "alarm.policies.prealarm.hint":
    "Startet vor der vollen Auslösung eine leise Voralarm-Phase: Nur Quittungston-, Benachrichtigungs- und Licht-Ausgänge feuern für diese Sekunden, danach eskaliert die volle Ausgangs-Richtlinie. Ein Stummschalten während dieser Phase verhindert die Eskalation. 0 deaktiviert die Phase.",
  "alarm.policies.rearm.hint":
    'Schaltet die Zone diese Sekunden nach einem Unscharfschalten am Ende der Auslösephase wieder in den Modus vor dem Vorfall scharf; wirkt nur, wenn "Wenn die Auslösephase endet" auf Unscharf schalten steht. Der Countdown setzt sich bei jeder Sensoraktivität zurück.',
  "alarm.policies.rearm.seconds": "Automatisches Wiederscharfschalten nach (s)",
  "alarm.policies.schedules.add": "Zeitplan hinzufügen",
  "alarm.policies.schedules.auto_arm": "Automatisch scharf schalten",
  "alarm.policies.schedules.auto_arm.hint":
    "Wenn aktiv, schaltet die Zone zu dieser Zeit automatisch scharf. Wenn inaktiv, erscheint nur eine Erinnerung.",
  "alarm.policies.schedules.days": "Tage",
  "alarm.policies.schedules.empty": "Noch keine Zeitpläne",
  "alarm.policies.schedules.mode": "Modus",
  "alarm.policies.schedules.time": "Uhrzeit",
  "alarm.policies.section.codes": "Codes",
  "alarm.policies.section.codes.hint":
    "Alarm-Codes werden im Reiter Codes verwaltet und sind unabhängig von Login-Konten. Diese Schalter legen fest, wann ein Code eingegeben werden muss; sie wirken nur auf anonyme Eingabewege wie MQTT, Codetastatur und Funkschlüssel.",
  "alarm.policies.section.hazard": "Gefahrenausgänge",
  "alarm.policies.section.hazard.hint":
    "Immer aktive Ausgangs-Richtlinie für Gefahren-Auslöser (Rauch, Wasser, Gas) — diese Sensoren feuern rund um die Uhr, unabhängig vom Scharf-Modus.",
  "alarm.policies.section.panic": "Panikausgänge",
  "alarm.policies.section.panic.hint":
    "Immer aktive Ausgangs-Richtlinie für Panik-Auslöser — unabhängig vom Scharf-Modus. Ein als stiller Panik-Auslöser markierter Sensor unterdrückt akustische Ausgänge bei seinen Auslösungen unabhängig von dieser Richtlinie.",
  "alarm.policies.section.prealarm": "Voralarm",
  "alarm.policies.section.rearm":
    "Nachlauf & automatisches Wiederscharfschalten",
  "alarm.policies.section.schedules": "Zeitpläne",
  "alarm.policies.section.schedules.hint":
    "Tägliche Scharfschalt-Zeitpläne für diese Zone, ausgewertet in der lokalen Zeitzone des Daemons. Ohne ausgewählte Tage feuert ein Eintrag jeden Tag. Mit automatischem Scharfschalten wird die Zone wirklich scharf geschaltet; andernfalls erscheint nur eine Erinnerung, wenn die Zone nicht im erwarteten Modus ist.",
  "audit.title": "Änderungs-Verlauf",
  "audit.empty": "Noch keine Änderungen aufgezeichnet.",
  "audit.empty.description":
    "Konfigurationsänderungen werden hier mit Bearbeiter, betroffener Einstellung und Zeitstempel protokolliert.",
  "audit.entries": "{count} Einträge",
  "audit.filter.all": "Alle Aktionen",
  "audit.from": "Von",
  "audit.to": "Bis",
  "audit.export_csv": "CSV exportieren",
  "audit.prev": "Zurück",
  "audit.next": "Weiter",
  "audit.page": "Seite {page}",
  "audit.changes": "Änderungen",
  "audit.col.parameter": "Parameter",
  "audit.col.before": "Vorher",
  "audit.col.after": "Nachher",
  "audit.action.paramset_write": "Konfiguration",
  "audit.action.link_paramset_write": "Verknüpfung",
  "audit.action.link_add": "Verknüpfung hinzugefügt",
  "audit.action.link_remove": "Verknüpfung entfernt",
  "audit.action.schedule_write": "Zeitplan",
  "audit.action.active_profile": "Profil",
  "audit.action.data_point_write": "Wert",
  "audit.action.addon_update_install": "Add-on-Update",
  "audit.action.alarm_acknowledge": "Alarm quittiert",
  "audit.action.alarm_arm": "Alarm scharf geschaltet",
  "audit.action.alarm_code_change": "Alarmcode geändert",
  "audit.action.alarm_config_change": "Alarmkonfiguration",
  "audit.action.alarm_disarm": "Alarm unscharf geschaltet",
  "audit.action.alarm_motion_reset": "Bewegungsmelder zurückgesetzt",
  "audit.action.alarm_output_test": "Test des Signalgebers",
  "audit.action.alarm_silence": "Alarm stummgeschaltet",
  "audit.action.alarm_walk_test": "Begehungstest",
  "audit.action.area_change": "Bereich geändert",
  "audit.action.backup_pre_update": "Sicherung vor dem Update",
  "audit.action.backup_upload": "Sicherung importiert",
  "audit.action.backup_delete": "Sicherung gelöscht",
  "audit.action.central_create": "CCU hinzugefügt",
  "audit.action.central_delete": "CCU entfernt",
  "audit.action.central_update": "CCU geändert",
  "audit.action.channel_flags": "Kanal-Kennzeichen",
  "audit.action.config_section_delete": "Konfigurationsabschnitt gelöscht",
  "audit.action.config_section_update": "Konfigurationsabschnitt gespeichert",
  "audit.action.device_assignment": "Gerätezuordnung",
  "audit.action.device_communication_test": "Kommunikationstest",
  "audit.action.device_config_restore": "Gerätekonfiguration zurückgespielt",
  "audit.action.device_install_mode": "Anlernmodus",
  "audit.action.device_replace": "Gerät ersetzt",
  "audit.action.device_search": "Gerätesuche",
  "audit.action.device_team_set": "Geräteteam",
  "audit.action.diagram_config": "Diagrammkonfiguration",
  "audit.action.group_admin": "Gruppenverwaltung",
  "audit.action.incidents_clear": "Vorfälle gelöscht",
  "audit.action.install_mode": "Anlernmodus",
  "audit.action.install_mode_local": "Lokales Anlernen",
  "audit.action.link_activate": "Verknüpfung aktiviert",
  "audit.action.link_update": "Verknüpfung geändert",
  "audit.action.matter_commissioning": "Matter-Kopplung",
  "audit.action.matter_exposure_bulk": "Matter-Freigabe (Sammeländerung)",
  "audit.action.matter_exposure_update": "Matter-Freigabe",
  "audit.action.matter_fabric_revoke": "Matter-Fabric entzogen",
  "audit.action.matter_factory_reset": "Matter-Kopplungen entfernt",
  "audit.action.matter_force_sync": "Matter-Topologie neu synchronisiert",
  "audit.action.matter_share": "Matter-Freigabe geteilt",
  "audit.action.program_delete": "Programm gelöscht",
  "audit.action.program_execute": "Programm ausgeführt",
  "audit.action.recording_toggle": "Aufzeichnung umgeschaltet",
  "audit.action.room_function": "Raum / Gewerk",
  "audit.action.system_ccu_position": "CCU-Standort",
  "audit.action.system_ccu_poweroff": "CCU ausgeschaltet",
  "audit.action.system_ccu_reboot": "CCU neu gestartet",
  "audit.action.system_ccu_recovery_mode": "CCU-Wiederherstellungsmodus",
  "audit.action.system_ccu_safe_mode": "CCU-Sicherheitsmodus",
  "audit.action.system_firmware_download": "Firmware-Download",
  "audit.action.tls_cert_upload": "TLS-Zertifikat",
  "audit.action.token_create": "Token erstellt",
  "audit.action.token_revoke": "Token widerrufen",
  "audit.action.logging.override_set": "Log-Level-Override gesetzt",
  "audit.action.logging.override_reset": "Log-Level-Override zurückgesetzt",
  "audit.action.logging.default_level_set": "Standard-Log-Level geändert",
  "audit.action.diagnostics.capture_start": "Diagnose-Aufzeichnung gestartet",
  "audit.action.diagnostics.capture_stop": "Diagnose-Aufzeichnung gestoppt",
  "audit.action.system.restart_requested": "Daemon-Neustart angefordert",
  "audit.action.cache_clear": "Cache geleert",
  "audit.action.un_ignore_update": "Update nicht mehr ignoriert",
  "audit.action.user_create": "Benutzer angelegt",
  "audit.action.user_delete": "Benutzer gelöscht",
  "audit.action.user_update": "Benutzer geändert",
  "backup.title": "Backups",
  "backup.subtitle": "CCU-Sicherungen auf dem Daemon-Host.",
  "backup.empty": "Noch keine Backups vorhanden.",
  "backup.upload": "Importieren…",
  "backup.uploading": "Wird importiert…",
  "backup.upload.help":
    "Ein .sbk-Archiv von anderswo übernehmen, damit es wie ein lokales Backup zurückgespielt werden kann. Das Archiv wird vor dem Speichern geprüft.",
  "backup.uploaded": "Backup {id} importiert.",
  "backup.uploaded_with_version":
    "Backup {id} importiert (von Firmware {version}).",
  "backup.trigger": "Backup anstoßen",
  "backup.trigger_central": "Ziel-CCU",
  "backup.triggering": "Erstelle…",
  "backup.confirm.title": "Backup wiederherstellen?",
  "backup.confirm.body":
    "Die CCU wird ihren aktuellen Zustand verlieren und mit dem Inhalt dieses Backups überschrieben. Diese Aktion lässt sich nicht rückgängig machen.",
  "backup.col.created": "Erstellt am",
  "backup.col.central": "CCU",
  "backup.col.size": "Größe",
  "backup.col.id": "ID",
  "backup.col.action": "Aktion",
  "backup.download": "Download",
  "backup.started": "Backup angestoßen (ID {id}).",
  "backup.storage.label": "Speicherpfad",
  "backup.storage.unknown": "nicht gemeldet",
  "backup.storage.unavailable":
    "Kein Speicherverzeichnis — der Daemon konnte es nicht anlegen. Backups können nicht abgelegt werden.",
  "backup.storage.summary": "{count} Archive · {bytes}",
  "backup.delete": "Löschen",
  "backup.deleting": "Wird gelöscht…",
  "backup.delete_confirm.title": "Sicherung löschen?",
  "backup.delete_confirm.body":
    "{name} wird endgültig aus dem Speicher des Daemons entfernt. Ist das die einzige Kopie der Konfiguration dieser CCU, gibt es keinen Weg zurück.",
  "backup.deleted": "Sicherung {id} gelöscht.",
  "backup.delete_failed": "Löschen von {id} fehlgeschlagen: {error}",
  "backup.restore_started": "Restore von {id} angestoßen.",
  "common.acknowledge": "Quittieren",
  "common.add": "Hinzufügen",
  "common.cancel": "Abbrechen",
  "common.close": "Schließen",
  "common.copy": "Kopieren",
  "common.delete": "Entfernen",
  "common.edit": "Bearbeiten",
  "common.enable": "Aktivieren",
  "common.disable": "Deaktivieren",
  "common.error": "Fehler:",
  "common.refresh": "Aktualisieren",
  "common.loading": "Lade…",
  "common.modified": "geändert",
  "common.new": "+ Neu",
  "common.no": "Nein",
  "common.none": "keine",
  "common.paste": "Einfügen",
  "common.reload": "Neu laden",
  "common.remove": "Entfernen",
  "loglevels.title": "Log-Level-Overrides",
  "loglevels.subtitle":
    "Logging einzelner Subsysteme anheben oder absenken. Overrides greifen hierarchisch (z. B. openccu-loom.client).",
  "loglevels.default": "Standard: {level}",
  "loglevels.empty":
    "Keine Overrides — jedes Subsystem folgt dem Standard-Level.",
  "loglevels.permanent": "dauerhaft",
  "loglevels.expires_in_min": "läuft in {mins} min ab",
  "loglevels.expires_soon": "läuft bald ab",
  "loglevels.path_label": "Logger-Pfad",
  "loglevels.level_label": "Level",
  "loglevels.ttl_label": "TTL (min)",
  "loglevels.ttl_permanent": "dauerhaft",
  "loglevels.add": "Override hinzufügen",
  "loglevels.added": "Override für {path} gesetzt.",
  "loglevels.removed": "Override für {path} entfernt.",
  "loglevels.admin_only":
    "Nur Administratoren können Log-Level-Overrides ändern.",
  "account.password.title": "Passwort ändern",
  "account.password.subtitle": "Passwort für Ihr Konto ({user}) aktualisieren.",
  "account.password.current": "Aktuelles Passwort",
  "account.password.new": "Neues Passwort",
  "account.password.confirm": "Neues Passwort bestätigen",
  "account.password.submit": "Passwort ändern",
  "account.password.changed": "Passwort geändert.",
  "account.password.mismatch": "Passwörter stimmen nicht überein.",
  "account.password.too_short": "Mindestens {min} Zeichen verwenden.",
  "tls.title": "TLS-Zertifikat",
  "tls.subtitle":
    "PEM-Zertifikat und Schlüssel hochladen. Der Listener (API + SPA) lädt das Zertifikat neu — ohne Neustart.",
  "tls.cert_label": "Zertifikat (PEM)",
  "tls.key_label": "Privater Schlüssel (PEM)",
  "tls.upload": "Hochladen & neu laden",
  "tls.uploaded": "Zertifikat ersetzt und neu geladen.",
  "tls.not_enabled":
    "TLS ist nicht aktiviert. Zuerst north.rest.tls_cert_file / tls_key_file setzen.",
  "common.reset": "Zurücksetzen",
  "common.download": "Herunterladen",
  "common.restore": "Wiederherstellen",
  "common.save": "Speichern",
  "common.saving": "Speichern…",
  "common.search": "Suchen…",
  "common.yes": "Ja",
  "devices.empty": "Keine Geräte gefunden.",
  "devices.loading": "Lade Geräte…",
  "devices.title": "Geräte",
  "devices.initializing": "Geräte werden von CCU '{name}' geladen …",
  "devices.initializing_banner":
    "CCU '{name}' initialisiert noch (Geräte {loaded}/{total}) — deren Geräte erscheinen automatisch.",
  "central.readiness.ready": "Bereit",
  "central.readiness.waiting": "Wartet auf CCU",
  "central.readiness.loading_hub": "Initialisiert (Namen)",
  "central.readiness.loading_devices":
    "Initialisiert (Geräte {loaded}/{total})",
  "central.readiness.offline": "Offline",
  "central.readiness.unknown": "Unbekannt",
  "matter.readiness.waiting":
    "Wartet auf CCU-Initialisierung — die Kopplung wird verfügbar, sobald mindestens eine CCU bereit ist.",
  "matter.readiness.partial":
    "CCU '{name}' initialisiert noch — deren Geräte erscheinen nach Abschluss automatisch in der Kopplung.",
  "firmware.title": "Firmware",
  "firmware.subtitle": "Firmware-Versionen und OTA-Update-Status.",
  "firmware.updates_available":
    "{count} Gerät(e) haben ein Firmware-Update verfügbar.",
  "firmware.no_updates": "Keine Geräte mit verfügbaren Firmware-Updates.",
  "firmware.filter.all": "Alle Geräte",
  "firmware.filter.updatable": "Updates verfügbar",
  "firmware.col.device": "Gerät",
  "firmware.col.model": "Modell",
  "firmware.col.current": "Installiert",
  "firmware.col.available": "Verfügbar",
  "firmware.col.state": "Status",
  "firmware.col.action": "Aktion",
  "firmware.update": "Aktualisieren",
  "firmware.triggering": "Starte…",
  "firmware.in_progress": "Läuft…",
  "firmware.up_to_date": "Aktuell",
  "firmware.awaiting_transfer": "Übertragung ans Gerät steht aus",
  "firmware.triggered": "Firmware-Update für {name} angestoßen.",
  "firmware.confirm_update":
    'Firmware-Update für "{name}" anstoßen? Das Gerät ist während des Updates kurz nicht erreichbar.',
  "firmware.duty_cycle_warning":
    "Der Duty Cycle der Funkschnittstelle ist hoch ({value}%). Die Funkübertragung kann stocken, bis sich der Funk erholt hat.",
  "firmware.count": "{count} von {total} Geräten",
  "firmware.state.UNKNOWN": "Unbekannt",
  "firmware.state.UP_TO_DATE": "Aktuell",
  "firmware.state.LIVE_UP_TO_DATE": "Aktuell",
  "firmware.state.NEW_FIRMWARE_AVAILABLE": "Update verfügbar",
  "firmware.state.LIVE_NEW_FIRMWARE_AVAILABLE": "Update verfügbar",
  "firmware.state.DELIVER_FIRMWARE_IMAGE": "Übertrage…",
  "firmware.state.LIVE_DELIVER_FIRMWARE_IMAGE": "Übertrage…",
  "firmware.state.READY_FOR_UPDATE": "Bereit",
  "firmware.state.DO_UPDATE_PENDING": "Ausstehend…",
  "firmware.state.PERFORMING_UPDATE": "Aktualisiere…",
  "firmware.state.BACKGROUND_UPDATE_NOT_SUPPORTED": "Nicht unterstützt",
  "diagnostics.title": "Diagnose",
  "diagnostics.subtitle": "Health, Interfaces und Vorfälle",
  "diagnostics.health": "Gesundheit",
  "diagnostics.interfaces": "Interfaces",
  "diagnostics.rssi.device": "Gerät",
  "diagnostics.rssi.reachable": "Erreichbar",
  "diagnostics.rssi.device_dbm": "Gerät (dBm)",
  "diagnostics.rssi.peer_dbm": "Gegenstelle (dBm)",
  "diagnostics.rssi.battery": "Batterie",
  "nav.signal": "Funk & Batterie",
  "page.title.signal": "Funk & Batterie — OpenCCU-Loom",
  "signal.title": "Funk & Batterie",
  "signal.count": "{count} Geräte",
  "signal.hint":
    "Empfangsfeldstärke und Batterie pro Gerät, aus dem Wartungskanal. Funktioniert für HmIP und BidCos.",
  "signal.empty": "Keine Geräte melden RSSI.",
  "signal.empty.description":
    "Funk- und Batteriewerte erscheinen hier, sobald ein Gerät mit seiner CCU kommuniziert hat.",
  "signal.low_battery": "Schwach",
  "diagnostics.incidents": "Vorfälle",
  "diagnostics.empty.components": "Keine Komponenten registriert.",
  "diagnostics.empty.interfaces": "Keine Interfaces konfiguriert.",
  "diagnostics.empty.incidents": "Keine Vorfälle.",
  "diagnostics.connected": "verbunden",
  "diagnostics.disconnected": "getrennt",
  "diagnostics.reconnect": "Reconnect",
  "diagnostics.reconnect_done": "{id}: Reconnect ausgelöst.",
  "diagnostics.health_score": "Gesundheitswert (0–100)",
  "diagnostics.download_dump": "Diagnose herunterladen",
  "diagnostics.logging": "Logging",
  "diagnostics.log_default": "Standard",
  "diagnostics.no_overrides": "Keine Level-Overrides aktiv.",
  "diagnostics.log_path": "Logger-Pfad",
  "diagnostics.log_level": "Level",
  "diagnostics.ttl_seconds": "TTL (s)",
  "diagnostics.apply": "Anwenden",
  "diagnostics.permanent": "permanent",
  "diagnostics.unavailable": "Nicht verfügbar",
  "diagnostics.log_level_applied": "Log-Level übernommen.",
  "diagnostics.capture": "Aufzeichnung",
  "diagnostics.capture_status.running": "Läuft",
  "diagnostics.capture_status.stopped": "Gestoppt",
  "diagnostics.capture_status.expired": "Abgelaufen",
  "diagnostics.capture_status.aborted": "Abgebrochen",
  "diagnostics.incident_severity.info": "Info",
  "diagnostics.incident_severity.warning": "Warnung",
  "diagnostics.incident_severity.error": "Fehler",
  "diagnostics.incident_severity.critical": "Kritisch",
  "diagnostics.duration_seconds": "Dauer (s)",
  "diagnostics.anonymise": "Anonymisieren",
  "diagnostics.stop": "Stoppen",
  "diagnostics.client_health": "Client-Gesundheit",
  "diagnostics.primary": "Primary",
  "diagnostics.healthy": "gesund",
  "diagnostics.unhealthy": "ungesund",
  "diagnostics.in_recovery": "in Recovery",
  "health.status.healthy": "Gesund",
  "health.status.degraded": "Beeinträchtigt",
  "health.status.unhealthy": "Fehlerhaft",
  "health.status.unknown": "Unbekannt",
  "health.note.initial_sync_connected": "Erst-Sync: verbunden",
  "health.note.initial_sync_not_connected": "Erst-Sync: nicht verbunden",
  "health.note.client_connected": "Client verbunden",
  "health.note.breaker_closed": "Sicherung geschlossen",
  "health.note.breaker_half_open": "Sicherung halb offen",
  "health.note.breaker_open": "Sicherung offen",
  "health.note.breaker_open_escalated": "Sicherung offen (eskaliert)",
  "health.note.recovery_started": "Wiederherstellung gestartet",
  "health.note.recovery_completed": "Wiederherstellung abgeschlossen",
  "health.note.recovery_failed_escalated":
    "Wiederherstellung fehlgeschlagen (eskaliert)",
  "diagnostics.last_ok": "Zuletzt OK",
  "diagnostics.last_fail": "Letzter Fehler",
  "diagnostics.last_event": "Letztes Event",
  "diagnostics.consecutive_failures": "Aufeinanderfolg. Fehler",
  "diagnostics.reconnect_attempts": "Reconnect-Versuche",
  "diagnostics.central": "Zentrale",
  "diagnostics.system_gauges": "System-Metriken",
  "diagnostics.rpc_recording.active": "Aktiv",
  "diagnostics.rpc_recording.inactive": "Inaktiv",
  "diagnostics.rpc_recording.running_hint":
    "🔴 Aufzeichnung läuft · übersteht Neustart",
  "diagnostics.rpc_recording.stop": "Beenden",
  "diagnostics.rpc_recording.started": "RPC-Aufzeichnung gestartet.",
  "diagnostics.rpc_recording.stopped": "RPC-Aufzeichnung beendet.",
  // --- Unified recordings hub ---
  "diagnostics.recordings.section_title": "Aufzeichnungen",
  "diagnostics.recordings.new_title": "Neue Aufzeichnung",
  "diagnostics.recordings.type": "Typ",
  "diagnostics.recordings.type.log": "Debug-Log",
  "diagnostics.recordings.type.rpc": "RPC-Verkehr",
  "diagnostics.recordings.type.both": "Beides",
  "diagnostics.recordings.scope": "CCU-Bereich",
  "diagnostics.recordings.scope_all": "Alle CCUs",
  "diagnostics.recordings.start": "Aufzeichnung starten",
  "diagnostics.recordings.running_title": "Aufzeichnung läuft",
  "diagnostics.recordings.col_type": "Typ",
  "diagnostics.recordings.col_scope": "CCU / Bereich",
  "diagnostics.recordings.col_start": "Start / Status",
  "diagnostics.recordings.col_size": "Größe / Einträge",
  "diagnostics.recordings.col_action": "Aktion",
  "diagnostics.recordings.empty": "Noch keine Aufzeichnungen.",
  "diagnostics.recordings.anonymise_hint":
    "Anonymisieren ersetzt operator-spezifische Felder (Login-Subject, Benutzername) — Interface-Namen, Geräteadressen und Host-IPs bleiben sichtbar.",
  "diagnostics.recordings.retention_hint":
    "Debug-Log-Aufzeichnungen nutzen einen rollierenden RAM-Puffer für die eingestellte Dauer. RPC-Aufzeichnungen behalten die gesamte Sitzung im Speicher bis zum Beenden und überstehen Daemon-Neustarts.",
  "diagnostics.recordings.format_map": "Map",
  "diagnostics.recordings.format_golden": "Golden",
  "diagnostics.recordings.until": "läuft bis {time}",
  "diagnostics.recordings.anonymised": "anonymisiert",
  "diagnostics.recordings.duration_open_hint":
    "0 = offen (Server-Limit 60 min)",
  "inbox.title": "Posteingang",
  "inbox.subtitle":
    "Geräte, die die CCU im Anlernmodus erkannt hat, aber noch nicht übernommen wurden.",
  "inbox.empty":
    "Posteingang ist leer. Aktiviere den Anlernmodus auf der Geräte-Seite, um neue Geräte zu sehen.",
  "inbox.accept": "Übernehmen",
  "inbox.accepted": "{name} übernommen.",
  "inbox.pending_creation_badge": "Wartet auf Freigabe",
  "inbox.pending_creation_hint":
    "Verzögerte Geräteanlage ist aktiv: Das Gerät existiert auf der CCU, hat hier aber erst nach der Übernahme Datenpunkte.",
  "inbox.accept_dialog.title": "Gerät übernehmen",
  "inbox.accept_dialog.subtitle":
    "Optional {address} konfigurieren, bevor es in die Registry aufgenommen wird. Alles leer lassen, um nur zu übernehmen.",
  "inbox.accept_dialog.name_label": "Name",
  "inbox.accept_dialog.name_placeholder": "Gerätename (optional)",
  "inbox.accept_dialog.include_channels": "Kanäle mitbenennen",
  "inbox.accept_dialog.rooms_label": "Räume",
  "inbox.accept_dialog.functions_label": "Gewerke",
  "inbox.accept_dialog.group_label": "Heizungsgruppe",
  "inbox.accept_dialog.group_none": "— keine —",
  "inbox.accept_dialog.group_hint":
    "Das Gerät nach dem Annehmen optional einer Heizungsgruppe hinzufügen.",
  "inbox.group_assign.done": "Zur Gruppe „{group}“ hinzugefügt.",
  "inbox.group_assign.no_channel":
    "Das Gerät hat keinen für diese Gruppe zuweisbaren Kanal — später manuell hinzufügen.",
  "inbox.group_assign.failed": "Gruppen-Zuordnung fehlgeschlagen.",
  "inbox.accept_dialog.submit": "Übernehmen",
  "inbox.accept_dialog.catalog_error":
    "Räume und Gewerke konnten nicht geladen werden.",
  "messages.title": "Meldungen",
  "messages.alarms": "Alarme",
  "messages.service": "Service-Meldungen",
  "messages.empty.alarms": "Keine Alarme.",
  "messages.empty.alarms.description":
    "Alarme erscheinen hier, sobald ein Gerät eine Störung meldet.",
  "messages.empty.service": "Keine Service-Meldungen.",
  "messages.empty.service.description":
    "Service-Meldungen erscheinen hier, z. B. bei schwacher Batterie oder Sabotage.",
  "messages.quittable_only": "Nur quittierbare",
  "messages.all_types": "Alle Typen",
  "messages.acknowledged": "Quittiert.",
  "messages.summary": "{alarms} Alarme · {services} Service-Meldungen",
  "messages.ackable": "quittierbar",
  "messages.ack_all.button": "Alle bestätigen",
  "messages.ack_all.confirm_alarms": "Alle Alarmmeldungen quittieren?",
  "messages.ack_all.confirm_services":
    "Alle quittierbaren Service-Meldungen quittieren?",
  "messages.ack_all.done": "{count} Meldungen quittiert.",
  "messages.type.generic": "Allgemein",
  "messages.type.sticky": "Sticky",
  "messages.type.config_pending": "Config ausstehend",
  "messages.type.alarm": "Alarm",
  "messages.type.update_pending": "Update ausstehend",
  "messages.type.communication": "Kommunikation",
  "messages.suppress": "Dauerhaft ausblenden",
  "messages.suppress.confirm":
    "Diese Service-Meldung dauerhaft auf der CCU unterdrücken? Das Gerät meldet sie nicht mehr, bis die Unterdrückung aufgehoben wird.",
  "messages.suppress.button": "Ausblenden",
  "messages.suppressed": "Unterdrückt.",
  "messages.suppressed.tab": "Unterdrückt",
  "messages.suppressed.empty": "Keine unterdrückten Service-Meldungen.",
  "messages.suppressed.empty.description":
    "Dauerhaft ausgeblendete Service-Meldungen erscheinen hier, damit du sie wieder einblenden kannst.",
  "messages.suppressed.col.parameter": "Parameter",
  "messages.suppressed.col.channel": "Kanal",
  "messages.suppressed.all_parameters": "Alle Parameter",
  "messages.unsuppress.button": "Wiederherstellen",
  "messages.unsuppress.confirm":
    "Unterdrückung aufheben und diese Service-Meldung wieder zulassen?",
  "messages.unsuppressed": "Unterdrückung aufgehoben.",
  "nav.alarm": "Alarmanlage",
  "nav.security": "Sicherheit & Sicherheitstechnik",
  "nav.audit": "Verlauf",
  "nav.backups": "Backups",
  "nav.devices": "Geräte",
  "nav.overview": "Übersicht",
  "nav.diagnostics": "Diagnose",
  "nav.energy": "Energie",
  "nav.diagrams": "Diagramme",
  "nav.favorites": "Favoriten",
  "nav.firmware": "Firmware",
  "favorites.title": "Favoriten",
  "favorites.subtitle":
    "Ihre angehefteten Geräte und Systemvariablen, browserübergreifend synchronisiert.",
  "favorites.empty":
    "Noch keine Favoriten. Heften Sie ein Gerät auf seiner Detailseite an.",
  "favorites.pin": "Anheften",
  "favorites.pinned": "Angeheftet",
  "favorites.unpin": "Lösen",
  "favorites.added": "{label} zu Favoriten hinzugefügt.",
  "favorites.removed": "{label} aus Favoriten entfernt.",
  "favorites.kind.channel": "Kanal",
  "favorites.kind.program": "Programm",
  "favorites.program_started": "{label} gestartet.",
  "favorites.pin_channel": "Kanal zu Favoriten hinzufügen",
  "favorites.unpin_channel": "Kanal aus Favoriten entfernen",
  "favorites.pin_program": "Programm zu Favoriten hinzufügen",
  "favorites.unpin_program": "Programm aus Favoriten entfernen",
  "favorites.kind.device": "Gerät",
  "favorites.kind.sysvar": "Systemvariable",
  "nav.inbox": "Posteingang",
  "nav.fleet": "CCUs",
  "nav.groups": "Gruppen",
  "nav.links": "Direktverknüpfungen",
  "nav.schedules": "Zeitprogramme",
  "nav.logout": "Abmelden",
  "nav.messages": "Meldungen",
  "nav.programs": "Programme",
  "nav.settings": "Einstellungen",
  "nav.sysvars": "Variablen",
  "nav.about": "Info",
  // --- Info (#/about) ---
  "about.title": "Info",
  "about.subtitle":
    "Version, Build und Laufzeitdaten dieses OpenCCU-Loom-Daemons.",
  "about.load_error": "Laden fehlgeschlagen: {error}",
  "about.section.daemon": "Daemon",
  "about.field.version": "Version",
  "about.field.commit": "Commit",
  "about.field.build_date": "Build-Datum",
  "about.field.runtime": "Build-Variante",
  "about.runtime.addon": "CCU-/RaspberryMatic-Add-on",
  "about.runtime.standalone": "Standalone (Binary, Docker oder HA-Add-on)",
  "about.field.started_at": "Gestartet",
  "about.field.uptime": "Laufzeit",
  "about.field.api_version": "API-Version",
  "about.field.capabilities": "Capabilities",
  "about.section.centrals": "Zentralen",
  "about.centrals.empty": "Keine Zentralen konfiguriert.",
  "about.centrals.firmware": "CCU-Firmware",
  "about.centrals.update_available": "Update {version} verfügbar",
  "about.section.license": "Lizenz & Links",
  "about.license.text":
    "OpenCCU-Loom ist MIT-lizenzierte Open-Source-Software. Das Binary bettet CCU-Metadaten-Extrakte ein, die der eQ-3 HomeMatic Software License (nicht-kommerziell) unterliegen.",
  "about.links.github": "GitHub",
  "about.links.releases": "Releases & Changelog",
  "about.links.notices": "Third-Party-Hinweise",
  "about.links.docs": "Benutzerhandbuch",
  "nav.leave_title": "Ungespeicherte Änderungen",
  "nav.leave_body":
    "Es gibt ungespeicherte Änderungen, die beim Verlassen dieser Ansicht verloren gehen. Trotzdem verlassen?",
  "nav.leave_confirm": "Verlassen",
  // --- Übersicht (Kachel-Dashboard über alle Geräte, Roadmap B8) ---
  "overview.title": "Übersicht",
  "overview.subtitle": "Alle Geräte Ihres Bestands, gruppiert und filterbar.",
  "overview.group_by": "Gruppieren nach:",
  "overview.group_mode.room": "Raum",
  "overview.group_mode.function": "Gewerk",
  "overview.group_mode.central": "CCU",
  "overview.filter.all_functions": "Alle Gewerke",
  "overview.filter.central_title": "CCU",
  "overview.filter.room_title": "Raum",
  "overview.filter.function_title": "Gewerk",
  "overview.filter.area_title": "Bereich",
  "overview.search_placeholder": "Geräte durchsuchen…",
  "overview.empty": "Noch keine Geräte.",
  "overview.empty_filtered": "Keine Geräte entsprechen den aktuellen Filtern.",
  "overview.load_error": "Geräte konnten nicht geladen werden: {error}",
  "overview.group.count": "{count} Geräte",
  "overview.group.loading": "Kacheln werden geladen…",
  "overview.group.error": "Kacheln konnten nicht geladen werden: {error}",
  "overview.group.empty": "Keine steuerbaren Kanäle auf diesen Geräten.",
  "overview.unassigned_room": "Ohne Zuordnung",
  "overview.unassigned_function": "Ohne Zuordnung",
  "overview.unassigned_central": "Unbekannte CCU",
  "overview.expand": "Gruppe aufklappen",
  "overview.collapse": "Gruppe einklappen",
  "unignore.subtitle":
    "Versteckte Parameter als reguläre Datenpunkte verfügbar machen. Verwendung auf eigene Gefahr.",
  "unignore.warning":
    "Häufige Schreibvorgänge auf MASTER-Paramset-Werte können Geräte beschädigen.",
  "unignore.central_label": "Zentrale:",
  "unignore.search_placeholder": "Nach Name filtern…",
  "unignore.add_pattern": "Hinzufügen",
  "unignore.add_pattern_placeholder":
    "PARAMETER oder PARAMETER:PARAMSET@MODEL:CHANNEL",
  "unignore.save": "Speichern",
  "unignore.discard": "Verwerfen",
  "unignore.no_centrals": "Keine Zentralen registriert.",
  "unignore.no_candidates": "Keine versteckten Parameter verfügbar.",
  "unignore.no_match": "kein Treffer",
  "unignore.parse_errors_title":
    "Einige Muster konnten nicht angewendet werden:",
  "unignore.saved": "Un-Ignore-Liste aktualisiert ({count} Muster).",
  "unignore.saved_with_errors": "Gespeichert mit {count} Parse-Fehler(n).",
  "unignore.save_failed": "Speichern fehlgeschlagen: {err}",
  "unignore.stats":
    "{total} versteckte Parameter · {active} aktiviert · {pending} geändert",
  "unignore.pending_changes": "Nicht gespeichert: {count}",
  "unignore.filter.categories": "Kategorie",
  "unignore.filter.paramset": "Paramset",
  "unignore.filter.only_enabled": "Nur aktivierte",
  "unignore.filter.hidden_notice":
    "Durch den Kategoriefilter ausgeblendet: {count}.",
  "unignore.filter.show_all": "Alle anzeigen",
  "unignore.filter.reset": "Filter zurücksetzen",
  "unignore.no_filter_match": "Kein Parameter passt zum Filter.",
  "unignore.no_filter_match_hint":
    "Suche erweitern oder eine Kategorie wieder einblenden.",
  "unignore.toggle_parameter": "{parameter} aktivieren oder deaktivieren",
  "unignore.toggle_scopes": "Gerätetypen für {parameter} anzeigen",
  "unignore.remove_pattern": "Muster {pattern} entfernen",
  "unignore.orphans_title": "Muster ohne passenden Parameter",
  "unignore.orphans_hint":
    "Früher gespeichert oder von Hand eingetragen — aktuell trägt sie kein Gerät im Bestand.",
  "unignore.scope.all_devices": "Alle Geräte",
  "unignore.scope.all_channels": "alle Kanäle",
  "unignore.scope.partial": "Bereiche: {count}",
  "unignore.scope.models": "Gerätetypen: {count}",
  "unignore.scope.channel": "Kanal {channel}",
  "unignore.scope.device_count": "Geräte: {count}",
  "unignore.reason.operation_mode": "Kanalmodus",
  "unignore.reason.master_gate": "MASTER-Einstellung",
  "unignore.reason.week_profile": "Wochenprogramm",
  "unignore.reason.device_specific": "Gerätespezifisch",
  "unignore.reason.hidden": "Intern verwendet",
  "unignore.reason.ignore_list": "Ausgeschlossen",
  "unignore.reason.wildcard_prefix": "Namenspräfix",
  "unignore.reason.wildcard_suffix": "Namenssuffix",
  "unignore.reason_detail.wildcard_prefix": "Präfix {pattern}",
  "unignore.reason_detail.wildcard_suffix": "Suffix {pattern}",
  "unignore.reason.channel_restricted": "Anderer Kanal",
  "unignore.reason.event_suppressed": "Events unterdrückt",
  "unignore.reason.internal_flag": "Intern",
  "unignore.reason.read_only": "Diagnose-Bit",
  "unignore.reason.unknown": "Unbekannt",
  "unignore.reason_help.operation_mode":
    "Der Betriebsmodus des Kanals schließt diesen Parameter aus. Ein anderer Modus blendet ihn ohne Un-Ignore-Eintrag ein.",
  "unignore.reason_help.master_gate":
    "MASTER-Konfigurationswert außerhalb der Freigabeliste für diesen Gerätetyp und Kanal.",
  "unignore.reason_help.week_profile":
    "Eine Zelle eines Wochenprogramms (P1_ENDTIME_MONDAY_1, 01_WP_LEVEL). Ein einzelnes Thermostat hat davon hunderte; bearbeiten Sie das Profil stattdessen im Zeitprogramm-Editor.",
  "unignore.reason_help.device_specific":
    "Gezielt für diesen Gerätetyp unterdrückt.",
  "unignore.reason_help.hidden":
    "Der Datenpunkt existiert und wird an anderer Stelle ausgewertet (Wartungskanal, kombinierter Datenpunkt), erscheint aber nicht einzeln.",
  "unignore.reason_help.ignore_list":
    "Steht auf der eingebauten Liste von Parametern, die nie zu Datenpunkten werden.",
  "unignore.reason_help.wildcard_prefix":
    "Trifft auf ein unterdrücktes Namenspräfix zu (ADJUSTING_, ERR_TTM_, HANDLE_, IDENTIFY_, PARTY_START_, PARTY_STOP_, STATUS_FLAG_).",
  "unignore.reason_help.wildcard_suffix":
    "Trifft auf ein unterdrücktes Namenssuffix zu (_OVERFLOW, _OVERRUN, _REPORTING, _RESULT, _STATUS, _SUBMIT).",
  "unignore.reason_help.channel_restricted":
    "Nur auf einem anderen Kanal dieses Geräts vorgesehen.",
  "unignore.reason_help.event_suppressed":
    "Events dieses Parameters werden für diesen Gerätetyp gefiltert.",
  "unignore.reason_help.internal_flag":
    "Die CCU kennzeichnet den Parameter als INTERNAL — ein Service-Wert, kein Betriebswert.",
  "unignore.reason_help.read_only":
    "Weder schreibbar noch event-fähig: Die CCU sendet ihn nie von sich aus, er aktualisiert sich nur beim Abfragen.",
  "unignore.reason_help.unknown":
    "Keine bekannte Regel erklärt diese Unterdrückung. Bitte melden.",
  "programs.title": "Programme",
  "programs.empty": "Keine Programme.",
  "programs.run": "Ausführen",
  "programs.running": "Läuft…",
  "settings.title": "Einstellungen",
  "settings.subtitle": "Daemon-Konfiguration und UI-Voreinstellungen",
  "settings.expert_mode": "Expert-Modus",
  "settings.expert_mode_hint":
    "Tiefe Tuning-Felder einblenden (Reliability, Callback-Ports, Matter-Internals). Standard: aus.",
  "settings.live_edit_disabled":
    "Live-Bearbeitung deaktiviert — das Daten-Verzeichnis ist read-only. Settings via config.yaml + Neustart pflegen.",
  "settings.restart_required":
    "Diese Änderung greift erst nach dem nächsten Daemon-Neustart.",
  "settings.save": "Speichern",
  "settings.save_and_restart": "Speichern und neu starten",
  "settings.saved": "Gespeichert.",
  "settings.save_failed": "Speichern fehlgeschlagen: {err}",
  "settings.reset": "Auf Standard zurücksetzen",
  "settings.section_unset": "Aktuell aktive eingebaute Standardwerte.",
  "settings.values_admin_only":
    "Nur Administratoren können die aktuellen Konfigurationswerte sehen und ändern. Die Einstellungen sind hier ohne sie aufgeführt.",
  "config.source.bootstrap": "Aus der Bootstrap-Konfigurationsdatei",
  "config.source.db": "Über die Oberfläche gespeichert",
  "config.source.env": "Durch Umgebungsvariable überschrieben",
  "config.source.default": "Standardwert",
  "config.source.short.bootstrap": "yaml",
  "config.source.short.db": "live",
  "config.source.short.env": "env",
  "config.source.short.default": "default",
  "config.field.locale": "Oberflächensprache",
  "config.field.data_dir": "Daten-Verzeichnis",
  "config.field.bootstrap.allow_first_run_setup": "Ersteinrichtung erlauben",
  "config.field.logging.level": "Log-Level",
  "config.field.logging.format": "Log-Format",
  "config.field.logging.overrides": "Pro-Subsystem-Levelüberschreibungen",
  "config.field.north.mqtt.enabled": "MQTT-Bridge aktiv",
  "config.field.north.mqtt.broker_url": "Broker-URL",
  "config.field.north.mqtt.client_id": "MQTT Client-ID",
  "config.field.north.mqtt.username": "Broker-Benutzer",
  "config.field.north.mqtt.password": "Broker-Passwort",
  "config.field.north.mqtt.topic_base": "Topic-Präfix",
  "config.field.north.mqtt.raw_enabled": "Rohebene veröffentlichen",
  "config.field.north.mqtt.discovery_enabled": "HA-Discovery veröffentlichen",
  "config.field.north.mqtt.protocol_version": "MQTT-Protokollversion",
  "config.field.north.mqtt.payload_format": "Payload-Format",
  "config.field.north.mqtt.sub_devices_enabled":
    "Ein HA-Gerät pro Kanal-Gruppe",
  "config.field.north.matter.enabled": "Matter-Bridge aktiv",
  "config.field.north.matter.enable_time_sync":
    "TimeSynchronization-Cluster mounten",
  "config.field.north.matter.listen": "UDP-Bind-Adresse",
  "config.field.north.matter.vendor_id": "Vendor-ID",
  "config.field.north.matter.product_id": "Product-ID",
  "config.field.north.matter.node_label": "Bridge-Label",
  "config.field.north.matter.discriminator": "Commissioning-Discriminator",
  "config.field.north.matter.prefer_ipv4": "IPv4 erzwingen",
  "config.field.north.matter.expose_secondary_channels":
    "Sekundärkanäle exponieren",
  "config.field.north.matter.mdns_advertise": "mDNS-Advertiser",
  "config.field.north.matter.dev_rotate_unique_ids":
    "UniqueID pro Boot rotieren (Dev)",
  "config.field.north.matter.commissioning.passcode": "Setup-Code",
  "config.field.north.matter.commissioning.salt": "PBKDF2-Salt",
  "config.field.north.matter.commissioning.iterations": "PBKDF2-Iterationen",
  "config.field.north.matter.commissioning.concurrent_pairings":
    "Parallele Pairings",
  "config.field.north.matter.commissioning.ephemeral_window":
    "Ephemeres Pairing-Fenster",
  "config.field.north.matter.case.node_id": "Node-ID",
  "config.field.north.matter.case.fabric_id": "Fabric-ID",
  "config.field.north.matter.attestation.dac_path": "DAC-Zertifikatspfad",
  "config.field.north.matter.attestation.dac_key_path": "DAC-Schlüsselpfad",
  "config.field.north.matter.attestation.pai_path": "PAI-Zertifikatspfad",
  "config.field.north.matter.attestation.cd_path":
    "CD-Pfad (Certification Declaration)",
  "config.field.north.discovery.mdns.enabled": "mDNS-Bekanntgabe",
  "config.field.north.discovery.mdns.instance_name": "mDNS-Instanzname",
  "config.field.north.discovery.ssdp.enabled": "CCUs per SSDP finden",
  "config.field.north.discovery.ssdp.interval": "CCU-Suchintervall",
  "config.field.north.rest.enabled": "REST-API aktiv",
  "config.field.north.rest.listen": "REST-Bind-Adresse",
  "config.field.north.rest.public_url": "Öffentliche URL",
  "config.field.north.rest.tls_cert_file": "TLS-Zertifikatsdatei",
  "config.field.north.rest.tls_key_file": "TLS-Schlüsseldatei",
  "config.field.north.rest.cors": "Erlaubte CORS-Origins",
  "config.field.north.rest.auth.basic_enabled": "HTTP-Basic-Auth",
  "config.field.north.rest.auth.bearer_enabled": "Bearer-Token-Auth",
  "config.field.north.rest.auth.oidc.enabled": "OIDC aktiv",
  "config.field.north.rest.auth.oidc.issuer": "OIDC-Issuer-URL",
  "config.field.north.rest.auth.oidc.client_id": "OIDC Client-ID",
  "config.field.north.rest.auth.oidc.redirect_url": "OIDC-Redirect-URL",
  "config.field.north.rest.rate_limit.enabled": "REST-Rate-Limit",
  "config.field.north.rest.rate_limit.requests_per_second":
    "Refill-Rate (Req/s)",
  "config.field.north.rest.rate_limit.burst": "Burst-Kapazität",
  "config.field.north.ui.enabled": "Bootstrap-UI aktiv",
  "config.field.north.ui.embedded": "In Home Assistant eingebettet",
  "config.field.north.ui.embedded_scope": "Geltungsbereich des eingebetteten Modus",
  "config.field.north.ui.profiles": "Navigationsprofile",
  "config.field.callback.host": "Callback-Bind-Adresse",
  "config.field.callback.port": "XML-RPC-Callback-Port",
  "config.field.callback.bin_port": "BIN-RPC-Callback-Port",
  "config.field.callback.port_range": "Ephemerer Port-Bereich",
  "config.field.callback.public_host": "Öffentlicher Hostname (NAT)",
  "config.field.callback.max_connections": "Max. Callback-Verbindungen",
  "config.field.callback.restrict_source_ips":
    "Callbacks auf CCU-IPs beschränken",
  "config.field.ccu_data.translations_path": "Pfad zum Übersetzungsarchiv",
  "config.field.ccu_data.easymode_path": "Pfad zum Easymode-Archiv",
  "config.field.north.rest.auth.users": "Bootstrap-Benutzer",
  "config.field.north.rest.auth.tokens": "Bootstrap-API-Tokens",
  "config.field.north.rest.auth.oidc.client_secret": "OIDC Client-Secret",
  "config.field.north.rest.auth.oidc.role_claim": "OIDC-Rollen-Claim",
  "config.field.north.rest.auth.ccu.enabled": "CCU-Anmeldung aktiv",
  "config.field.north.rest.auth.ccu.primary": "CCU ist primär",
  "config.field.north.rest.auth.ccu.central": "Zentrale (CCU)",
  "config.field.north.rest.auth.ccu.min_user_level": "Minimales User-Level",
  "config.field.north.rest.auth.ccu.role_mapping": "Rollen-Zuordnung",
  "config.field.north.rest.auth.ha_ingress.enabled": "HA-Ingress-Passthrough",
  "config.field.north.rest.auth.ha_ingress.trusted_proxy_cidr":
    "Vertrauenswürdiges Proxy-CIDR",
  "config.field.north.rest.auth.ha_ingress.role": "Gewährte Rolle",
  "config.field.north.rest.openapi_spec_path": "OpenAPI-Spec-Pfad",
  "config.field.north.rest.openapi_validate":
    "Anfragen gegen OpenAPI-Spec prüfen",
  "config.field.north.rest.ws.replay_capacity":
    "WebSocket-Replay-Ringpuffer-Größe",
  "config.field.persistence.values_cache.enabled": "VALUES-Cache aktiv",
  "config.field.persistence.values_cache.flush_interval":
    "Cache-Flush-Intervall",
  "config.field.persistence.values_cache.disabled_centrals":
    "Ausgeschlossene CCUs",
  "config.field.backup.dir": "Backup-Verzeichnis",
  "config.field.backup.schedule": "Intervall für automatische Backups",
  "config.field.backup.keep_last": "Letzte N Backups behalten",
  "config.field.alarm.enabled": "Alarmanlage aktiviert",
  "config.field.alarm.default_siren_seconds": "Standard-Sirenendauer (s)",
  "config.field.alarm.max_acoustic_per_incident_seconds":
    "Akustik-Budget pro Vorfall (s)",
  "config.field.alarm.stop_verify_seconds": "Prüffenster Sirenenstopp (s)",
  "config.field.alarm.journal_retention_days": "Journal-Aufbewahrung (Tage)",
  "config.field.alarm.restart_loop_breaker":
    "Neustart-Schleifenbegrenzer (Reaktivierungen)",
  "config.field.alarm.duress_visibility":
    "Sichtbarkeit von Bedrohungscode und stiller Panik",
  "config.field.persistence.history.enabled": "Verlaufsaufzeichnung aktiv",
  "config.field.persistence.history.retention": "Aufbewahrungszeitraum",
  "config.field.persistence.history.energy_price_per_kwh": "Strompreis pro kWh",
  "config.field.persistence.history.energy_currency": "Währungsbezeichnung",
  "config.field.persistence.history.retention_hourly":
    "Aufbewahrung Stunden-Rollup",
  "config.field.persistence.history.retention_daily":
    "Aufbewahrung Tages-Rollup",
  "config.field.persistence.history.flush_interval": "Verlaufs-Flush-Intervall",
  "config.field.persistence.history.include": "Parameter einschließen",
  "config.field.persistence.history.exclude": "Parameter ausschließen",
  "config.field.persistence.history.disabled_centrals": "Ausgeschlossene CCUs",
  "config.field.persistence.history.export.enabled": "Verlaufsexport aktiv",
  "config.field.persistence.history.export.kind": "Export-Backend",
  "config.field.persistence.history.export.endpoint": "Export-Endpunkt",
  "config.field.persistence.history.export.org": "InfluxDB-Organisation",
  "config.field.persistence.history.export.bucket": "InfluxDB-Bucket",
  "config.field.persistence.history.export.token_env":
    "Token-Umgebungsvariable",
  "config.field.reliability.command_retry_initial_delay":
    "Retry-Anfangsverzögerung",
  "config.field.reliability.command_throttle_inter_command_delay":
    "Throttle-Befehlsabstand",
  "config.field.centrals": "CCUs",
  "config.field.centrals.name": "Name",
  "config.field.centrals.host": "Host",
  "config.field.centrals.port": "Port",
  "config.field.centrals.json_rpc_port": "JSON-RPC-Port",
  "config.field.centrals.username": "Benutzername",
  "config.field.centrals.password": "Passwort",
  "config.field.centrals.tls": "TLS",
  "config.field.centrals.tls_insecure_skip_verify": "TLS-Prüfung überspringen",
  "config.field.centrals.primary_interface": "Primäres Interface",
  "config.field.centrals.ports": "Interface-Ports",
  "config.field.centrals.visibility.un_ignore": "Un-Ignore-Muster",
  "config.field.centrals.interfaces": "Interfaces",
  "config.field.centrals.interfaces.name": "Interface-Name",
  "config.field.centrals.interfaces.port": "Interface-Port",
  "config.field.centrals.interfaces.remote_path": "Remote-Pfad",
  "config.field.centrals.interfaces.rpc_type": "RPC-Typ",
  "config.field.centrals.check_connection_interval":
    "Verbindungsprüfungsintervall",
  "config.field.centrals.behavior.delay_new_device_creation":
    "Neue Geräte zurückstellen",
  "config.field.centrals.behavior.enable_device_firmware_check":
    "Firmware-Update-Entitäten",
  "config.field.centrals.behavior.enable_program_scan": "Programme scannen",
  "config.field.centrals.behavior.enable_sysvar_scan":
    "Systemvariablen scannen",
  "config.field.centrals.behavior.include_internal_programs":
    "Interne Programme einschließen",
  "config.field.centrals.behavior.include_internal_sysvars":
    "Interne Systemvariablen einschließen",
  "config.field.centrals.behavior.light_last_brightness":
    "Letzte Helligkeit wiederherstellen",
  "config.field.centrals.behavior.program_markers": "Programm-Marker",
  "config.field.centrals.behavior.sysvar_markers": "Systemvariablen-Marker",
  "config.field.centrals.behavior.sysvar_scan_interval":
    "Systemvariablen-Scan-Intervall",
  "config.field.centrals.behavior.use_group_channel_for_cover_state":
    "Gruppenkanal für Rollladenstatus",
  "config.field.north.mcp.enabled": "MCP-Server aktiv",
  "config.field.north.mcp.allow_writes": "Schreibzugriff erlauben",
  "config.field.north.mcp.path": "MCP-Mount-Pfad",
  "config.field.north.webhook.enabled": "Ausgehenden Webhook aktivieren",
  "config.field.north.webhook.url": "Webhook-URL",
  "config.field.north.webhook.secret": "Signatur-Schlüssel",
  "config.field.north.webhook.events": "Ereignis-Filter",
  "config.field.north.webhook.centrals": "CCU-Filter",
  "config.field.north.webhook.parameter_glob": "Parameter-Glob",
  "config.field.north.webhook.timeout_ms": "Zustellungs-Timeout (ms)",
  "config.field.north.webhook.inbound.enabled":
    "Eingehenden Webhook aktivieren",
  "config.field.north.webhook.inbound.token": "Eingangs-Token",
  "config.field.north.mqtt.retain_cleanup_window_ms":
    "Retain-Cleanup-Fenster (ms)",
  "config.field.north.rest.csrf_enabled": "CSRF-Schutz",
  "config.field.north.rest.csrf_secure": "CSRF Secure-Cookie",
  "config.field.north.rest.tracing.otlp_endpoint": "OTLP-Trace-Endpunkt",
  "config.field.addon_update.check_interval": "Update-Prüfintervall",
  "config.field.addon_update.enabled": "Hintergrund-Updateprüfung",
  "config.help.locale":
    "Standard-Sprache der SPA beim ersten Aufruf. Operatoren können pro Benutzer in den Einstellungen umschalten.",
  "config.help.data_dir":
    "Verzeichnis für SQLite-Datenbank, Sessions, Backups, Logs. Muss schreibbar sein; wird beim ersten Start angelegt.",
  "config.help.bootstrap.allow_first_run_setup":
    "Hält die unauthentifizierte Ersteinrichtung (/setup) erreichbar, solange keine Anmeldequelle existiert. Auf false gesetzt bleibt sie auch bei leerer Benutzertabelle geschlossen — die beabsichtigte Folge ist eine Aussperrung, die nur eine YAML-Änderung mit Neustart aufhebt. Bootstrap-Ebene: in der Konfigurationsdatei ändern, nicht hier.",
  "config.help.logging.level":
    "Filter-Schwelle des strukturierten Loggers. debug zeigt Wire-Level-Traces; info ist der typische Operator-Level.",
  "config.help.logging.format":
    "Handler-Form. json für Produktion / Log-Shipper; text oder text-color für Terminal-Output.",
  "config.help.logging.overrides":
    "Per-Subsystem-Level-Überschreibungen, dot-separierter Logger-Pfad. Spezifischste Überschreibung gewinnt.",
  "config.help.north.mqtt.enabled":
    "Hauptschalter der MQTT-Bridge. Aus = keine Broker-Verbindung, keine Topics.",
  "config.help.north.mqtt.broker_url":
    "tcp://host:port (plain), tls://host:port (TLS) oder mqtt:// / mqtts://-Schema. Pflicht wenn MQTT aktiv.",
  "config.help.north.mqtt.client_id":
    "MQTT-Client-Identifier. Muss pro Broker-Verbindung eindeutig sein.",
  "config.help.north.mqtt.username":
    "Broker-Benutzer für authentifizierte Broker. Leer für anonyme Broker.",
  "config.help.north.mqtt.password":
    "Broker-Passwort — verschlüsselt at-rest im SQLite-File, in Backups redaktiert. Bevorzugt via OPENCCU_LOOM_MQTT_PASSWORD-Env-Variable setzen.",
  "config.help.north.mqtt.topic_base":
    "Präfix für jedes Raw- und Discovery-Topic. Ändern, wenn mehrere Daemons gegen denselben Broker laufen.",
  "config.help.north.mqtt.raw_enabled":
    "Veröffentlicht pro-DataPoint-State unter <topic_base>/<interface>/… — die rohe Ebene für non-HA-Konsumenten. Discovery braucht sie: Wird Discovery eingeschaltet, wird dies mit eingeschaltet, denn Discovery-Payloads verweisen ausschließlich auf Topics der Rohebene.",
  "config.help.north.mqtt.discovery_enabled":
    "Emittiert Home-Assistant-Discovery-Payloads, sodass HA die Geräte automatisch registriert. Setzt die Rohebene voraus — die Payloads benennen deren Topics, daher wird „Rohebene veröffentlichen“ mit aktiviert.",
  "config.help.north.mqtt.protocol_version":
    'MQTT-Dialekt: "5" (Standard) oder "3.1.1" für Broker ohne MQTT-5.0-Unterstützung. Kein stilles Downgrade — ein v5-Connect gegen einen v3-Broker schlägt mit benanntem Fehler fehl.',
  "config.help.north.mqtt.payload_format":
    "Reserviert, aktuell ohne Wirkung: State-Topics tragen unabhängig von dieser Einstellung immer den JSON-Envelope {value, available, modified_at}. Einen primitiven Scalar-Modus (bare) gibt es derzeit nicht.",
  "config.help.north.mqtt.sub_devices_enabled":
    "Multi-Channel-Group-Geräte als ein HA-Gerät pro Kanal-Gruppe rendern. Zeigt Parent + N Children in HA.",
  "config.help.north.matter.enabled":
    "Hauptschalter der Matter-Bridge. Standard aus. Aktiv = UDP-Listener und mDNS-Records gehen hoch.",
  "config.help.north.matter.enable_time_sync":
    "Mountet den optionalen TimeSynchronization-Cluster (0x0038) am Matter-Root-Endpoint. Standard aus — auf einer RootNode nur optional, und manche Controller (z. B. Apple Home) lehnen die Bridge beim Pairing ab, wenn er auftaucht. Nur aktivieren, wenn ein Controller eine Zeit-Sync-Oberfläche braucht; danach neu koppeln.",
  "config.help.north.matter.listen":
    "UDP-Bind-Adresse des Matter-Listeners. :5540 ist die IANA-Default; :0 lässt das OS wählen (für Tests). Amazon Alexa kann Bridges nur auf Port 5540 koppeln.",
  "config.help.north.matter.vendor_id":
    "IANA-vergebene Vendor-ID. 0xFFF1 ist der Test-/Dev-Block — niemals in Produktion ausliefern.",
  "config.help.north.matter.product_id":
    "Vendor-vergebene Produkt-ID. Default 0x8000.",
  "config.help.north.matter.node_label":
    "Nutzer-sichtbarer Label des Bridge-Nodes (sichtbar in Apple Home, Google Home, …).",
  "config.help.north.matter.discriminator":
    "12-bit Matter-Commissioning-Discriminator. Mit dem Passcode bildet er den manuellen Setup-Code.",
  "config.help.north.matter.prefer_ipv4":
    "Erzwingt IPv4-only auf dem Matter-UDP-Socket. Default aus = IPv6-Dual-Stack-Socket, der auch IPv4 akzeptiert (Standardwahl).",
  "config.help.north.matter.expose_secondary_channels":
    "Standardmäßig aus: Ein Mehrkanalgerät (Schalter, Dimmer, Rollladen, Schloss, Sirene, Ventil) projiziert einen einzelnen Matter-Endpoint aus seinem Primärkanal. Aktivieren, um auch die sekundären Aktor-Kanäle als eigene Endpoints zu exponieren. Nur Matter — MQTT, HA-Discovery und REST führen immer alle Kanäle.",
  "config.help.north.matter.mdns_advertise":
    "mDNS-Advertiser-Implementierung. Ohne Wert gilt `zeroconf`: operational + commissionable Records werden im Netz veröffentlicht — Voraussetzung für das Koppeln per QR-Code. `noop` hält die Records nur in-memory (Tests / Out-of-band-Discovery); Commissioner finden die Bridge dann nicht.",
  "config.help.north.matter.dev_rotate_unique_ids":
    "Nur für Entwicklung: mischt einen pro-Boot 16-Byte-Random-Salt in die Matter UniqueID jedes Bridged-Endpoints. Apple Home / Google Home brauchen eine STABILE UniqueID über Restarts hinweg, um Geräte wiederzuerkennen — in Produktion AUS lassen.",
  "config.help.north.matter.commissioning.passcode":
    "27-bit Matter-Setup-Code (Spec §5.1.6.4) — zwischen 00000001 und 99999998. Pflicht, damit Commissioner-Pairings akzeptiert werden; 0 lässt den PASE-Acceptor inaktiv.",
  "config.help.north.matter.commissioning.salt":
    "PBKDF2-Salt, persistent mit dem Passcode gespeichert (16–32 Byte gemäß Spec §3.10). Leer = fester Development-Salt — niemals in Produktion ausliefern.",
  "config.help.north.matter.commissioning.iterations":
    "PBKDF2-Iterationszahl (1000..100000 gemäß Spec §3.10). Default 1000. Höher = mehr CPU beim Pairing, härter gegen Brute-Force-Angriffe auf abgehörte Transcripts.",
  "config.help.north.matter.commissioning.concurrent_pairings":
    "Isoliert den PASE-Adapter pro Exchange-ID, sodass mehrere Commissioner parallel pairen können. Default aus — Singleton-Adapter reicht für den typischen 1-Commissioner-Flow und ist speichersparsamer.",
  "config.help.north.matter.commissioning.ephemeral_window":
    "Erzeugt bei jedem in der SPA geöffneten Commissioning-Fenster einen frischen Discriminator + Passcode + Verifier. Empfohlen für Produktion: Pairing-Codes rotieren automatisch, der statische Passcode dient nur noch als langlebiger Label-Code-Fallback.",
  "config.help.north.matter.case.node_id":
    "64-bit Operational-Node-Identifier innerhalb der Fabric. 0 deaktiviert den CASE-Responder ganz — gebraucht solange kein Commissioner einen NOC installiert hat.",
  "config.help.north.matter.case.fabric_id":
    "64-bit Fabric-Identifier, zu der die Bridge gehört. Pflicht wenn Node-ID ≠ 0.",
  "config.help.north.matter.attestation.dac_path":
    "Pfad zum vom Vendor gelieferten Device Attestation Certificate (PEM oder DER). Leer = ephemerer self-signed Development-DAC, der nur unter chip-tool --bypass-attestation-verifier validiert.",
  "config.help.north.matter.attestation.dac_key_path":
    "Pfad zum P-256-Privatschlüssel des DAC (PEM PKCS#8 oder DER). MUSS zum Public-Key im DAC-Zertifikat passen.",
  "config.help.north.matter.attestation.pai_path":
    "Pfad zum Product Attestation Intermediate-Zertifikat. Wird von der CSA zusammen mit dem DAC ausgeliefert.",
  "config.help.north.matter.attestation.cd_path":
    "Pfad zur CSA-signierten Certification Declaration (CMS-/PKCS#7-Nachricht). Die CD pinnt Vendor + Produkt als zertifiziertes Matter-Gerät.",
  "settings.section.intro.north.matter":
    "Native-Go Matter-Bridge, die ausgewählte CCU-Geräte als Matter-Accessories zur Verfügung stellt. Standardmäßig aus. Produktion benötigt Vendor-Attestation-Material (DAC / PAI / CD) im Experten-Bereich; Entwicklung pairt mit chip-tool --bypass-attestation-verifier.",
  "settings.section.intro.north.mcp":
    "MCP-Server (Model Context Protocol), der CCU-Geräte LLM-Agenten als Tools über den REST-Listener bereitstellt. Standardmäßig aus und nur lesend, bis zusätzlich „Schreibzugriff erlauben“ aktiviert ist. Änderungen greifen erst nach einem Daemon-Neustart. Siehe ADR 0025.",
  "config.help.north.mcp.enabled":
    "Hauptschalter des MCP-Servers (Streamable-HTTP auf dem REST-Listener). Standardmäßig aus. Greift erst nach einem Daemon-Neustart.",
  "config.help.north.mcp.allow_writes":
    "Schreibfähige MCP-Tools (z. B. set_datapoint) freischalten. Standardmäßig aus — der Server allein ist nur lesend; für Agenten-gesteuerte Steuerung aktivieren. Greift erst nach einem Daemon-Neustart.",
  "config.help.north.mcp.path":
    "HTTP-Mount-Pfad des MCP-Transports auf dem REST-Listener. Leer = /mcp. Greift erst nach einem Daemon-Neustart.",
  "config.help.north.webhook.enabled":
    "Hauptschalter für den ausgehenden Webhook. Aktiv sendet der Daemon bei Datenpunkt-, System-Status- und Incident-Ereignissen eine signierte JSON-Nutzlast per POST an die konfigurierte URL. Standardmäßig aus. Greift erst nach einem Daemon-Neustart.",
  "config.help.north.webhook.url":
    "Absolute http(s)-Adresse, an die jedes Ereignis per POST gesendet wird. Leer deaktiviert die Zustellung auch bei aktivem Schalter.",
  "config.help.north.webhook.secret":
    "Gemeinsamer Schlüssel für die HMAC-SHA256-Signatur des Bodys im Header X-OpenCCU-Signature. Leer = keine Signatur (der Empfänger kann die Echtheit nicht prüfen).",
  "config.help.north.webhook.events":
    "Positivliste der zuzustellenden Ereignistyp-Tags (z. B. datapoint.value_changed). Leer stellt alle unterstützten Ereignisse zu.",
  "config.help.north.webhook.centrals":
    "Positivliste der CCU-Namen, deren Ereignisse zugestellt werden. Leer stellt Ereignisse aller CCUs zu.",
  "config.help.north.webhook.parameter_glob":
    "Optionaler Glob (z. B. *TEMPERATURE*), der Datenpunkt-Ereignisse auf passende Parameternamen beschränkt. Leer = kein Parameterfilter; andere Ereignistypen sind nicht betroffen.",
  "config.help.north.webhook.timeout_ms":
    "HTTP-Timeout pro Zustellung in Millisekunden. Null oder negativ verwendet den Standard von 10000 ms.",
  "config.help.north.webhook.inbound.enabled":
    "Hauptschalter für die eingehende Webhook-REST-Schnittstelle (POST /api/v1/webhook/value und /api/v1/webhook/program). Standardmäßig aus. Die Routen werden nur bei aktivem Schalter eingehängt, daher greift eine Änderung erst nach einem Daemon-Neustart. Eingehende Anfragen sind echte Geräte-Schreibvorgänge / Programmausführungen.",
  "config.help.north.webhook.inbound.token":
    "Optionaler Bearer-Token, der zusätzlich zur normalen Auth-Kette akzeptiert wird, damit ein reiner Header-Aufrufer (z. B. eine Türklingel) ohne Sitzung oder Benutzer-Login senden kann. Als Authorization: Bearer <token> gesendet. Leer = nur die normale Auth-Kette gilt.",
  "config.help.north.discovery.mdns.enabled":
    "Daemon via mDNS / Zeroconf im LAN bekannt machen, damit z. B. Home Assistant ihn auto-erkennt.",
  "config.help.north.discovery.mdns.instance_name":
    "Linkester Label des mDNS-SRV/TXT-Records. Leer = OS-Hostname.",
  "config.help.north.discovery.ssdp.enabled":
    "Das LAN regelmäßig per SSDP/UPnP nach Homematic-/OpenCCU-Zentralen durchsuchen, damit sie mit einem Klick übernommen werden können. Nur lesend — es verlassen keine Daemon-Daten das LAN.",
  "config.help.north.discovery.ssdp.interval":
    "Wie oft die Suche wiederholt wird (z. B. 60s). Leer = 60 Sekunden.",
  "config.help.north.rest.enabled":
    "Hauptschalter des REST-/WebSocket-Servers. Aus = Daemon hat keine Operator-Oberfläche.",
  "config.help.north.rest.listen":
    "Bind-Adresse von REST + WebSocket. :8119 lauscht auf allen Interfaces; mit host:-Präfix einschränken.",
  "config.help.north.rest.public_url":
    "Von außen erreichbare Basis-URL dieses Daemons (Schema + Host [+ Port]), z. B. https://loom.example.com. Dient zum Bilden absoluter Links wie der OIDC-Redirect-URL und zur Ableitung des Secure-Cookie-Verhaltens. Leer lassen, um sie pro Anfrage abzuleiten — setzen, wenn hinter einem Reverse Proxy oder unter eigener Domain.",
  "config.help.north.rest.tls_cert_file":
    "Pfad zur PEM-Zertifikatskette. Zusammen mit der Schlüsseldatei setzen, um API + SPA über HTTPS auf demselben Port auszuliefern; beide leer lassen für reines HTTP hinter einem TLS-terminierenden Proxy. Ein hochgeladenes Zertifikat wird in diesen Pfad geschrieben und für Hot-Reload überwacht — der Upload ersetzt den Dateiinhalt, nicht die Wahl des Speicherorts.",
  "config.help.north.rest.tls_key_file":
    "Pfad zum PEM-Privatschlüssel passend zur Zertifikatsdatei. Zusammen mit dem Zertifikat erforderlich, um HTTPS zu aktivieren; ein hochgeladener Schlüssel wird hierher geschrieben und bei Änderung neu geladen.",
  "config.help.north.rest.cors":
    'Erlaubte Browser-Origins für Cross-Origin-REST-Aufrufe. Leer = CORS aus; ["*"] nur für Entwicklung.',
  "config.help.north.rest.auth.basic_enabled":
    "Akzeptiere HTTP-Basic-Credentials auf geschützten Routen. Nützlich für curl + CI. Standard: an; false lehnt Basic-Auth auch mit konfigurierten Benutzern ab.",
  "config.help.north.rest.auth.bearer_enabled":
    "Akzeptiere Bearer-Tokens via Authorization-Header. Für Automation. Standard: an; false lehnt Tokens auch mit konfigurierten Einträgen ab.",
  "config.help.north.rest.auth.oidc.enabled":
    "OpenID Connect Single-Sign-On aktivieren. Die Login-Seite zeigt einen SSO-Button, wenn konfiguriert.",
  "config.help.north.rest.auth.oidc.issuer":
    "Issuer-URL (ohne trailing slash). Das .well-known/openid-configuration-Dokument wird beim Daemon-Start geladen.",
  "config.help.north.rest.auth.oidc.client_id":
    "Public-Client-Identifier, beim IdP registriert. PKCE-Flow.",
  "config.help.north.rest.auth.oidc.redirect_url":
    "Muss mit der beim IdP registrierten URL übereinstimmen. Zeigt auf den OIDC-Callback-Handler.",
  "config.help.north.rest.rate_limit.enabled":
    "Per-Identity-Token-Bucket-Rate-Limit auf REST-Requests. Überschuss bekommt HTTP 429.",
  "config.help.north.rest.rate_limit.requests_per_second":
    "Steady-State-Token-Refill-Rate pro Identity. 10 ist ein sinnvoller Startwert.",
  "config.help.north.rest.rate_limit.burst":
    "Token-Bucket-Größe — maximale gleichzeitige Requests pro Identity vor Drosselung.",
  "config.help.north.ui.enabled":
    "Bootstrap-UI-Oberfläche (Login, /setup-Wizard, /health). Die SPA selbst läuft auf dem REST-Listener.",
  "config.help.north.ui.embedded":
    "Aktivieren, wenn Home Assistant die Konfigurationsoberfläche dieses Daemons besitzt — die Integration Homematic(IP) Local also gegen diesen Daemon läuft. Blendet aus, was HA bereits besitzt. Wird nicht aus Ingress abgeleitet: das Add-on wird auch ohne die Integration betrieben. Wo die Einstellung greift, ist eine eigene Frage — siehe „Geltungsbereich des eingebetteten Modus“.",
  "config.help.north.ui.embedded_scope":
    "Wo der eingebettete Modus greift. „Nur in Home Assistant“ (Standard) reduziert die Oberfläche nur für Aufrufe, die über Home Assistant hereinkommen; wer die eigene Adresse dieses Daemons öffnet, behält die volle Oberfläche — er hat sich bewusst für Loom statt für das HA-Panel entschieden, und der Grund fürs Ausblenden (Home Assistant zeigt denselben Editor) trifft auf ihn nicht zu. „Überall“ reduziert auf jedem Weg.",
  "config.help.north.ui.profiles":
    "Navigationsabweichungen je Profil, bearbeitet unter Einstellungen → Navigation & Ansichten. Wird sparsam gespeichert, damit später ergänzte Ansichten den Standard behalten, den ihr eigener Code vergibt.",
  "config.help.callback.host":
    "Lokales Interface, auf dem die XML-RPC- + BIN-RPC-Callback-Listener binden. Einschränken via Firewall, nicht via Bind-Adresse.",
  "config.help.callback.port":
    "Port des XML-RPC-Callback-Listeners. 0 = OS wählt ephemeren Port; der Daemon meldet ihn bei jedem CCU-Reconnect neu.",
  "config.help.callback.bin_port":
    "Port des BIN-RPC-Callback-Listeners (CUxD). 0 führt zum Standardwert 8129, nicht zu einem vom Betriebssystem vergebenen Port — anders als beim XML-RPC-Port oben gibt es für diesen Listener keinen Port-Bereich als Ausweg, daher braucht jeder Daemon auf demselben Host hier einen eigenen, expliziten Wert.",
  "config.help.callback.port_range":
    "Optionaler Port-Bereich <lo>-<hi>; der Callback-Listener bindet den ersten freien Port darin. Hat Vorrang vor dem XML-RPC-Port oben. Wenn der Daemon hinter einer engen Firewall sitzt.",
  "config.help.callback.public_host":
    "Hostname, den der Daemon der CCU bei init() ansagt. Setzen wenn der Daemon hinter NAT läuft.",
  "config.help.callback.max_connections":
    "Obergrenze gleichzeitiger Verbindungen pro Callback-Listener (XML-RPC und BIN-RPC). Begrenzt Speicher-/Goroutine-Verbrauch, falls ein nicht vertrauenswürdiger LAN-Host den Socket flutet. 0 = Standard (64).",
  "config.help.callback.restrict_source_ips":
    "Nur Callbacks von den konfigurierten CCU-IPs plus Loopback annehmen. Ergänzt eine Quell-IP-Allowlist zusätzlich zum Verbindungslimit. Standardmäßig aus; aktivieren, wenn außer den CCUs kein legitimer Host die Callback-Ports erreicht.",
  "config.help.ccu_data.translations_path":
    "Dateisystempfad zum OCCU-Übersetzungs-ZIP. Standard ist das im Binary eingebettete Archiv; nur überschreiben, um ein eigenes Extrakt zu testen.",
  "config.help.ccu_data.easymode_path":
    "Dateisystempfad zum OCCU-Easymode-ZIP. Standard ist das eingebettete Archiv; nur überschreiben, um ein eigenes Extrakt zu testen.",
  "config.help.north.rest.auth.users":
    'Seed-only-Benutzerliste, die einmalig beim ersten Start geladen wird. Nach dem ersten Start Benutzer im Tab "Benutzer" verwalten; Einträge hier werden ignoriert, sobald die Datenbank existiert.',
  "config.help.north.rest.auth.tokens":
    'Seed-only-Token-Liste, die einmalig beim ersten Start geladen wird. Nach dem ersten Start Tokens im Tab "API-Tokens" verwalten; Einträge hier werden ignoriert, sobald die Datenbank existiert.',
  "config.help.north.rest.auth.oidc.client_secret":
    "Vertrauliches Client-Secret des IdP. Leer für Public Clients (PKCE-only). Bevorzugt per Umgebungsvariable setzen.",
  "config.help.north.rest.auth.oidc.role_claim":
    'JWT-Claim-Name, aus dem der Daemon die Benutzerrolle (admin / user) liest. Default "role".',
  "config.help.north.rest.auth.ccu.enabled":
    "Anmeldung an die Benutzerdatenbank der genannten CCU delegieren. Nutzer melden sich mit ihren CCU-Konten an; lokale Nutzer bleiben als Break-Glass-Fallback. Neustart erforderlich.",
  "config.help.north.rest.auth.ccu.primary":
    "Wenn aktiv, wird zuerst die CCU geprüft, lokale Nutzer sind der Break-Glass-Fallback. Aus macht lokale Nutzer primär und die CCU zum letzten Mittel. Break-Glass gilt in beiden Richtungen.",
  "config.help.north.rest.auth.ccu.central":
    "Name der konfigurierten Zentrale, deren Benutzerdatenbank die Anmeldung prüft. Leer wählt die erste konfigurierte Zentrale.",
  "config.help.north.rest.auth.ccu.min_user_level":
    "CCU-Nutzer unterhalb dieses UserLevels ablehnen (8 Admin, 2 Operator, 1 Gast; 0 wird immer abgelehnt). Default 1 lässt jeden echten Nutzer zu.",
  "config.help.north.rest.auth.ccu.role_mapping":
    'Standard-Zuordnung CCU-UserLevel→Loom-Rolle überschreiben. Schlüssel sind das UserLevel als String ("8", "2", "1"); Werte "admin" / "operator" / "viewer". Leer = Defaults (≥8 admin, ≥2 operator, ≥1 viewer).',
  "config.help.north.rest.auth.ha_ingress.enabled":
    "Home Assistant Ingress vertrauen: eine vom Supervisor geproxyte Anfrage gilt als authentifizierter Admin — ohne Login. Standard (nicht gesetzt) = an im HA-Add-on, aus im normalen Build; An/Aus überschreibt. Nur sicher mit panel_admin: true des Add-ons (nur HA-Admins erreichen Ingress); echte Tokens/Sessions gewinnen weiterhin. Neustart erforderlich.",
  "config.help.north.rest.auth.ha_ingress.trusted_proxy_cidr":
    "Netz, aus dem der echte Peer der Ingress-Anfrage stammen muss. Leer nutzt den HA-Supervisor-Standard 172.30.32.0/23. X-Forwarded-For wird nie vertraut.",
  "config.help.north.rest.auth.ha_ingress.role":
    'Loom-Rolle für eine vertrauenswürdige Ingress-Anfrage: "admin" (Standard), "operator" oder "viewer".',
  "config.help.north.rest.openapi_spec_path":
    "Override-Pfad für die OpenAPI-YAML. Standard ist die zur Build-Zeit eingebettete Kopie. Expert: nur setzen, um die Spec während der Entwicklung ohne Neubau zu patchen.",
  "config.help.north.rest.openapi_validate":
    "Jede eingehende REST-Anfrage zur Laufzeit gegen assets/openapi.yaml prüfen. Default true. Nur auf sehr leistungsschwacher Hardware deaktivieren, wenn der ~1-ms-Overhead messbar ist.",
  "config.help.north.rest.ws.replay_capacity":
    "Ringpuffer-Tiefe für das subscribe-with-since-Feature des WebSocket. Default 1024 Events. Auf speicherbeschränkten Hosts reduzieren; bei Burst-Verlusten erhöhen.",
  "config.help.reliability.command_retry_initial_delay":
    "Erste Backoff-Wartezeit nach einem vorübergehenden CCU-Schreibfehler (verdoppelt sich bei jedem Retry). Default 2 s (produktionsgehärtet); niedriger für schnelle Test-Setups.",
  "config.help.reliability.command_throttle_inter_command_delay":
    "Minimaler Abstand zwischen zwei gedrosselten Befehlen pro CCU-Interface. Default 0 (keine Drossel). Auf ~50–500 ms erhöhen wenn BidCos-RF Duty-Cycle-Fehler zeigt.",
  "config.help.persistence.values_cache.enabled":
    "Lokales Caching der zuletzt gelesenen VALUES-Paramsets jedes Datenpunkts, damit ein Daemon-Restart nicht jeden Paramset frisch von der CCU lesen muss. Default: AN.",
  "config.help.persistence.values_cache.flush_interval":
    "Wie oft gepufferte Schreibvorgänge auf Disk geschrieben werden. Default 60 s — kurz genug, um einen Crash gut zu überstehen, lang genug, um Bursts zu sammeln.",
  "config.help.persistence.values_cache.disabled_centrals":
    "Liste von Central-Namen (eine pro Zeile), deren Datenpunkte NICHT gecached werden. Praktisch für Test-Rigs in Multi-CCU-Setups.",
  "config.help.backup.dir":
    "Wo heruntergeladene CCU-Archive abgelegt werden. Leer bedeutet <Datenverzeichnis>/backups. Bei einer Installation als CCU-Zusatzsoftware liegt das Datenverzeichnis in genau dem Bereich, den die CCU selbst sichert — ein Pfad auf externem Speicher verhindert daher, dass die CCU-Backups mit jedem Archiv weiter wachsen. Wirksam nach einem Neustart des Daemons.",
  "config.help.backup.schedule":
    "Wie oft jede konfigurierte CCU automatisch gesichert wird (z. B. 24h). Null deaktiviert geplante Backups; manuelle Backups über die Backups-Ansicht funktionieren weiter. Das erste automatische Backup läuft ein Intervall nach dem Start, nicht sofort.",
  "config.help.backup.keep_last":
    "Begrenzt, wie viele geplante Backups pro CCU aufbewahrt werden: nach jedem erfolgreichen Backup werden die ältesten darüber hinaus gelöscht. Null behält alle.",
  "config.help.alarm.enabled":
    "Hauptschalter für die Alarmanlage. Solange keine Zonen konfiguriert sind, bleibt die Anlage in jedem Fall inaktiv. Wirksam nach einem Neustart des Daemons.",
  "config.help.alarm.default_siren_seconds":
    "Standarddauer der akustischen Aktivierung in Sekunden, verwendet wenn ein Alarmausgang keine eigene Sirenendauer festlegt.",
  "config.help.alarm.max_acoustic_per_incident_seconds":
    "Kumuliertes akustisches Budget in Sekunden pro Vorfall über alle Reaktivierungen und Neustarts hinweg, damit ein hängender Sensor eine Sirene nicht dauerhaft auslösen kann.",
  "config.help.alarm.stop_verify_seconds":
    "Wie lange, in Sekunden, ein unbestätigter Sirenenstopp-Befehl wiederholt wird, bevor er zu einem Health-Incident eskaliert.",
  "config.help.alarm.journal_retention_days":
    "Wie viele Tage Alarm-Journal-Einträge aufbewahrt werden, bevor sie bereinigt werden. Null deaktiviert die Aufbewahrungsbegrenzung (Einträge bleiben dauerhaft erhalten).",
  "config.help.alarm.restart_loop_breaker":
    "Begrenzt, wie oft ein wiederherstellungsgetriebener Ausgang innerhalb eines Vorfalls erneut auslösen darf, bevor die Anlage auf reine optische Signalisierung und Benachrichtigungen zurückstuft.",
  "config.help.alarm.duress_visibility":
    "Wo die Verwendung eines Bedrohungscodes oder eine stille Panikauslösung erscheinen darf. „hidden“ erreicht ausschließlich den Webhook — ohne konfigurierten Webhook wird niemand benachrichtigt. „notify_only“ (Standard) sendet zusätzlich die Meldung, erreicht also ein Handy, schreibt sie aber nie in gespeicherte Zustände oder auf lokale Bildschirme. „full“ behandelt sie wie jeden anderen Alarm. Die Gefahr ist nicht ein unsicheres Home Assistant, sondern dass die Person neben Ihnen denselben Bildschirm sieht. Ob Home Assistant die Meldung als Banner auf dem Sperrbildschirm anzeigt, steuert diese Einstellung nicht — dafür einen Benachrichtigungskanal ohne Vorschau einrichten.",
  "config.help.persistence.history.enabled":
    "Hauptschalter der Messwerthistorie. Standardmäßig aus (Opt-in) — wenn aktiv, öffnet der Daemon history.db und startet den Retention-Job.",
  "config.help.persistence.history.retention":
    "Wie lange Rohmesswerte aufbewahrt werden; 0 = Standardwert von 30 Tagen (720 h), ältere Zeilen werden vom Retention-Job gelöscht.",
  "config.help.persistence.history.energy_price_per_kwh":
    "Preis einer Kilowattstunde; die Energie-Ansicht zeigt damit Kosten neben dem Verbrauch. Bei 0 werden gar keine Kosten angezeigt — ein Tarif von 0 würde jeden Betrag als 0,00 darstellen.",
  "config.help.persistence.history.energy_currency":
    "Bezeichnung für die aus dem Tarif berechneten Beträge (Symbol oder Code, z. B. € oder CHF). Standard ist das Euro-Zeichen. Reine Beschriftung — es wird nichts umgerechnet.",
  "config.help.persistence.history.retention_hourly":
    "Wie lange die Stunden-Rollup-Ebene aufbewahrt wird; 0 = Standardwert von 13 Monaten. Stunden-Zeilen werden vor diesem Cutoff in die Tages-Ebene gefaltet.",
  "config.help.persistence.history.retention_daily":
    "Wie lange die Tages-Rollup-Ebene aufbewahrt wird; 0 (Standard) = für immer, da Tages-Zeilen sehr klein sind (eine Zeile pro Datenpunkt und Tag).",
  "config.help.persistence.history.flush_interval":
    "Wie oft der Recorder einen Batch von Messwerten in history.db schreibt; 0 = Daemon-Standard von 5 s.",
  "config.help.persistence.history.include":
    "Parameter-Name-Globs, die aufgezeichnet werden (z. B. TEMPERATURE, *POWER*); leer (Standard) = alle numerischen VALUES-Parameter.",
  "config.help.persistence.history.exclude":
    "Parameter-Name-Globs, die von der Aufzeichnung ausgeschlossen werden; Exclude gewinnt immer über Include — leer (Standard) = kein Ausschluss.",
  "config.help.persistence.history.disabled_centrals":
    "Central-Namen, deren Datenpunkte nicht aufgezeichnet werden; leer (Standard) = alle aktiven Centrals.",
  "config.help.persistence.history.export.enabled":
    "Push-Exporter aktivieren, der jeden aufgezeichneten Messwert an einen externen Zeitreihenspeicher weiterleitet (Standard: InfluxDB); standardmäßig aus.",
  "config.help.persistence.history.export.kind":
    'Exporter-Backend; leer oder "influxdb" wählt den InfluxDB-v2-Line-Protocol-Writer (aktuell einziges Backend).',
  "config.help.persistence.history.export.endpoint":
    "Basis-URL des Zeitreihenspeichers, z. B. http://influx:8086.",
  "config.help.persistence.history.export.org":
    "InfluxDB-v2-Organisationsname, dem der Ziel-Bucket gehört.",
  "config.help.persistence.history.export.bucket":
    "InfluxDB-v2-Bucket, in den die Messwerte geschrieben werden.",
  "config.help.persistence.history.export.token_env":
    "Name der Umgebungsvariablen, die das InfluxDB-Schreib-Token hält; das Token wird nie direkt in der Konfiguration gespeichert.",
  "config.help.centrals":
    "Alle konfigurierten CCUs. Verwaltung im dedizierten CCUs-Tab — Einträge in config.yaml werden als Bootstrap-Seeds behandelt.",
  "config.help.centrals.name": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.host": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.port": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.json_rpc_port": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.username": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.password": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.tls": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.tls_insecure_skip_verify": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.primary_interface": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.ports": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.visibility.un_ignore": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.interfaces": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.interfaces.name": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.interfaces.port": "Im CCUs-Tab verwaltet.",
  "config.help.centrals.interfaces.remote_path": "URL-Pfad, an den die XML-RPC-Aufrufe dieser Schnittstelle gehen. Leer lassen für den CCU-Standard (/RPC2, bei VirtualDevices /groups); einen absoluten Pfad nur setzen, wenn ein Reverse-Proxy die Schnittstelle umleitet.",
  "config.help.centrals.interfaces.rpc_type": "Transport dieser Schnittstelle. Er ergibt sich aus dem Schnittstellennamen — CUxD spricht BIN-RPC, alle anderen XML-RPC — dieses Feld bestätigt das nur; ein widersprüchlicher Wert wird beim Laden der Konfiguration abgelehnt.",
  "config.help.centrals.check_connection_interval":
    "Wie oft der Daemon die CCU im Hintergrund anpingt; 0 = Compiler-Standard von 30 s, negativ = Prüfung deaktiviert.",
  "config.help.centrals.behavior.delay_new_device_creation":
    "Neu angelernte Geräte zurückhalten, bis du sie übernimmst: Sie stehen im Posteingang und bekommen erst nach der Übernahme Datenpunkte. Standard: aus.",
  "config.help.centrals.behavior.enable_device_firmware_check":
    "Für jedes Gerät, das Firmware-Updates meldet, eine Firmware-Update-Entität anzeigen. Standard: an.",
  "config.help.centrals.behavior.enable_program_scan":
    "CCU-Programme abrufen und als Hub-Entitäten bereitstellen; deaktivieren um das Programm-Discovery vollständig zu überspringen. Standard: an.",
  "config.help.centrals.behavior.enable_sysvar_scan":
    "CCU-Systemvariablen abrufen und als Hub-Entitäten bereitstellen; deaktivieren um das Systemvariablen-Discovery vollständig zu überspringen. Standard: an.",
  "config.help.centrals.behavior.include_internal_programs":
    "CCU-interne Programme (nicht für Benutzersteuerung gedacht) in die Hub-Entitätsfläche einschließen. Standard: aus.",
  "config.help.centrals.behavior.include_internal_sysvars":
    "CCU-interne Systemvariablen in die Hub-Entitätsfläche einschließen. Standard: an.",
  "config.help.centrals.behavior.light_last_brightness":
    "Beim Einschalten eines Lichts die zuletzt von der CCU gemeldete Helligkeit (≠ 0) wiederherstellen statt auf 100 % zu gehen. Standard: an.",
  "config.help.centrals.behavior.program_markers":
    "Token, die die CCU-Beschreibung eines Programms tragen kann. Wie bei Systemvariablen entscheiden Marker nur, wie ein Programm ankommt, nicht ob es importiert wird: Programme mit Marker-Treffer kommen in Home Assistant aktiviert an, alle übrigen deaktiviert. HX ist ein freier Marker für eigene Filterung, und INTERNAL schließt zusätzlich die internen Programme der CCU ein — was hier ins Gewicht fällt, weil die CCU die meisten gewöhnlichen Programme als intern kennzeichnet. HAHM wirkt hier nicht; es macht Systemvariablen schreibbar, und Programme haben keinen Wert zum Schreiben. Leer: alles wird importiert, alles deaktiviert.",
  "config.help.centrals.behavior.sysvar_markers":
    "Token, die die CCU-Beschreibung einer Systemvariablen tragen kann. Marker entscheiden nicht, ob eine Variable importiert wird — importiert wird alles —, sondern nur wie sie ankommt: Variablen mit Marker-Treffer kommen in Home Assistant aktiviert an, alle übrigen deaktiviert und werden pro Entität eingeschaltet. HAHM macht eine Variable schreibbar (Schalter, Auswahl, Zahl, Text statt nur lesendem Sensor). HX ist ein freier Marker für eigene Filterung. INTERNAL schließt zusätzlich die internen Variablen der CCU ein. Leer: alles wird importiert, alles deaktiviert.",
  "config.help.centrals.behavior.sysvar_scan_interval":
    "Wie oft der Daemon Systemvariablen von der CCU aktualisiert. 0 verwendet den Standard von 30 Sekunden; unter 3 Sekunden wird abgelehnt, weil jeder Durchlauf die CCU einen Skriptlauf auf einem Einzel-Thread-Interpreter kostet.",
  "config.help.centrals.behavior.use_group_channel_for_cover_state":
    "Rollladen-Position vom LEVEL-Wert des Gruppenkanals statt vom eigenen Kanal melden. Standard: an.",
  "config.help.north.mqtt.retain_cleanup_window_ms":
    "Wie lange (in Millisekunden) der Daemon auf alle retained Messages des Brokers wartet, bevor die Retain-Cleanup-Eviction-Liste verarbeitet wird; 0 = 2000 ms.",
  "config.help.north.rest.csrf_enabled":
    "Double-Submit-Cookie/Header-CSRF-Schutz auf mutierenden REST-Endpunkten aktivieren; standardmäßig an für Browser-Deployments — nur für reine API-Token-Setups ohne Session-Cookies deaktivieren.",
  "config.help.north.rest.csrf_secure":
    "Secure-Flag auf dem CSRF-Cookie setzen; aktivieren, wenn der Daemon hinter einem HTTPS-/TLS-Terminator betrieben wird.",
  "config.help.north.rest.tracing.otlp_endpoint":
    "Basis-URL eines OTLP/HTTP-Trace-Collectors (z. B. http://jaeger:4318); leer (Standard) = kein Span-Export.",
  "config.help.addon_update.check_interval":
    "Wie oft der Daemon im Hintergrund bei GitHub nach einem neuen Add-on-Release sucht, plus ein zufälliger Jitter von bis zu 1 Stunde, damit nicht die ganze Flotte gleichzeitig anfragt. 0 nutzt den Standard von 24 h; zum Abschalten der Hintergrundprüfung dient der Aktiviert-Schalter.",
  "config.help.addon_update.enabled":
    "Im Hintergrund bei GitHub nach neuen Add-on-Releases suchen (Prüfung beim Start plus wiederkehrendes Intervall); Standard an. Der manuelle „Nach Updates suchen“-Button und das Installieren bleiben auch bei Deaktivierung verfügbar.",
  "settings.section.intro.persistence":
    "Lokaler Disk-Cache der CCU-Datenpunkt-Werte. Erlaubt dem Daemon, Neustarts ohne kompletten Paramset-Re-Read zu überstehen. Standardmäßig AN mit 60-Sekunden-Flush — nur anfassen, wenn man Cache-Verhalten debuggt.",
  "settings.section.intro.reliability":
    "Knöpfe für den Southbound-Transport-Stack (XML-RPC/BIN-RPC Retry, Throttle, Circuit Breaker). Defaults spiegeln aiohomematic; nur anfassen wenn die CCU Duty-Cycle-Fehler zeigt oder bei einer Latenz-Regression.",
  "settings.section.intro.callback":
    "XML-RPC- und BIN-RPC-Callback-Listener, in die die CCU State-Change-Events pusht. Defaults binden auf 0.0.0.0 mit OS-gewählten Ports; nur überschreiben wenn der Daemon hinter NAT oder einer engen Firewall sitzt.",
  "settings.interface": "Oberfläche",
  "settings.language": "Sprache",
  "settings.start_route": "Startseite",
  "settings.start_route.default": "Geräteliste (Standard)",
  "settings.start_route.help":
    "Die Ansicht, die nach dem Anmelden geöffnet wird. Ein direkt aufgerufener Link hat immer Vorrang vor dieser Einstellung.",
  "settings.start_route.saved": "Startseite gespeichert",
  "settings.theme": "Design",
  "settings.theme.light": "Hell",
  "settings.theme.dark": "Dunkel",
  "settings.theme.system": "System",
  "settings.appearance.design": "Design",
  "settings.appearance.design.help":
    "Wähle den visuellen Stil. In Home Assistant folgt die Oberfläche automatisch deinem HA-Theme.",
  "settings.appearance.design.loom": "OpenCCU-Loom",
  "settings.appearance.design.ha": "Home Assistant",
  "settings.appearance.design.embedded_hint": "Von Home Assistant gesteuert",
  "settings.daemon": "Daemon",
  "settings.copy_json": "JSON kopieren",
  "settings.rooms": "Räume",
  "settings.functions": "Gewerke",
  "settings.users": "Benutzer",
  "settings.tokens": "API-Tokens",
  "settings.show_raw": "Roh-JSON anzeigen",
  "settings.system": "System",
  "settings.enabled": "Aktiv",
  "settings.startup_capture": "Aufzeichnung beim Start",
  "settings.startup_capture_help":
    "Öffnet eine Diagnose-Aufzeichnung als erstem Boot-Schritt, damit Wire-/Paramset-/Callback-Init im Archiv landen. Wirkt beim nächsten Neustart.",
  "settings.startup_capture_saved":
    "Gespeichert. Wirkt beim nächsten Neustart.",
  "settings.mqtt.reload_title": "MQTT-Änderungen anwenden",
  "settings.mqtt.reload_description":
    "Nach dem Speichern oben sind die neuen Werte zwar im Konfigurationsspeicher, aber der laufende MQTT-Stack nutzt weiter die alte Broker-Verbindung. Mit dieser Schaltfläche wird der Stack ohne Daemon-Neustart neu aufgebaut. Die neue Verbindung wird aufgebaut, bevor die alte getrennt wird — schlägt sie fehl, läuft der alte Stack unverändert weiter.",
  "settings.mqtt.reload": "MQTT jetzt neu laden",
  "settings.mqtt.reload_running": "Lädt neu …",
  "settings.mqtt.reload_success": "MQTT neu geladen ({ms} ms).",
  "settings.mqtt.reload_failed": "Neuladen fehlgeschlagen: {err}",
  "settings.restart_daemon": "Daemon neu starten",
  "settings.restart_daemon_help":
    "Sendet SIGTERM an den Daemon. In Produktion (systemd / Docker) startet der Supervisor automatisch neu; in Dev musst du selbst starten.",
  "settings.restart_daemon_unsupervised":
    "Deaktiviert — kein Supervisor erkannt (kein systemd, Docker oder Kubernetes). Ein Restart würde den Daemon offline lassen. Mit OPENCCU_LOOM_SUPERVISOR=1 überschreibbar.",
  "settings.secret_env_override":
    "Zur Laufzeit per Env-Variable {name} überschreibbar. Wenn gesetzt, hat sie Vorrang vor dem hier eingegebenen Wert.",
  "settings.secret_from_env":
    "Aktuell aus der Env-Variable {name} aufgelöst; Env-Variable löschen, damit der hier eingegebene Wert greift.",
  "settings.secret_not_set": "Nicht gesetzt — kein Wert hinterlegt.",
  "connectivity.ccu": "CCU",
  "connectivity.mqtt": "MQTT",
  "connectivity.matter": "Matter",
  "connectivity.green": "OK",
  "connectivity.amber": "eingeschränkt",
  "connectivity.red": "fehlgeschlagen",
  "connectivity.grey": "deaktiviert",
  "connectivity.no_components":
    "Nicht in die Health-Probe des Daemons eingehängt.",
  "settings.restart_confirm":
    "Daemon wirklich neu starten? CCU-Verbindungen sind für ein paar Sekunden weg.",
  "settings.restart_signalled":
    "Shutdown signalisiert — warte auf den Supervisor.",
  "settings.restarting": "Wird neu gestartet…",
  "admin.cache_clear.button": "CCU-Cache leeren",
  "admin.cache_clear.title": "CCU-Cache leeren",
  "admin.cache_clear.body":
    "Verwirft alle CCU-abgeleiteten In-Memory- und On-Disk-Caches (Gerätedaten, Paramsets, Values, Master-Profile). Der Daemon lädt alles beim nächsten Zugriff neu von der CCU. Konfiguration, Sichtbarkeitseinstellungen, Auth-Sessions und Matter-Zustand bleiben unverändert.",
  "admin.cache_clear.confirm": "Cache leeren",
  "admin.cache_clear.success":
    "Cache geleert — {devices} Geräte, {paramsets} Paramsets, {values} Values, {centrals} Centrals neu initialisiert.",
  "admin.cache_clear.error": "Cache-Leerung fehlgeschlagen: {err}",
  "admin.cache_clear.heading": "CCU-Cache leeren",
  "admin.cache_clear.help":
    "Entfernt alle CCU-abgeleiteten Caches ohne Neustart. Nützlich nach dem Import von Daten oder wenn der Daemon vom CCU-Zustand abweicht.",
  "settings.callback_ports": "Callback-Ports",
  "settings.feature_off": "aus",
  "settings.live_edit_pending":
    "Live-Bearbeitung folgt in Phase 11 — bis dahin werden Daemon-Werte aus config.yaml gelesen und sind hier nur sichtbar.",
  "settings.users_managed_yaml":
    "Benutzer + API-Tokens werden derzeit ausschließlich über die config.yaml gepflegt. Live-Edit folgt in Phase 11.",
  "settings.tokens_secret":
    "Token-Werte werden niemals offengelegt; angezeigt werden nur die letzten sechs Zeichen als Fingerprint.",
  "settings.rooms_help":
    "Aus den Geräte-Metadaten der CCU abgeleitet. Räume und Gewerke werden pro Gerät im Detail-Header zugewiesen.",
  // --- Settings-Sidebar-Gruppen ---
  "settings.group.general": "Allgemein & System",
  "settings.group.bridges": "Bridges (Northbound)",
  "settings.group.ccus": "CCUs & Anbindung",
  "settings.group.security": "Sicherheit & Zugriff",
  "settings.group.advanced": "Erweitert",
  // --- Settings-Sektion-Untergruppen (innerhalb eines Tabs) ---
  "config.subgroup.general": "Allgemein",
  "config.subgroup.auth": "Authentifizierung",
  "config.subgroup.oidc": "OIDC (OpenID Connect)",
  "config.subgroup.rate_limit": "Rate Limiting",
  "config.subgroup.ws": "WebSocket",
  "config.subgroup.tracing": "Tracing",
  "config.subgroup.commissioning": "Inbetriebnahme",
  "config.subgroup.case": "CASE-Sitzung",
  "config.subgroup.attestation": "Attestierung",
  "config.subgroup.mdns": "mDNS",
  "config.subgroup.ssdp": "SSDP",
  "config.subgroup.history": "Messwert-Verlauf",
  "config.subgroup.values_cache": "VALUES-Cache",
  "config.subgroup.behavior": "Verhalten",
  // --- Geräte-Parametergruppen (Kanal-MASTER-Paramset-Editor) ---
  "config.paramgroup.temperature": "Temperatur",
  "config.paramgroup.timing": "Zeit & Dauer",
  "config.paramgroup.display": "Anzeige",
  "config.paramgroup.transmission": "Übertragung & Kommunikation",
  "config.paramgroup.powerup": "Einschaltverhalten",
  "config.paramgroup.boost": "Boost",
  "config.paramgroup.button": "Tastenverhalten",
  "config.paramgroup.threshold": "Schwellwerte & Bedingungen",
  "config.paramgroup.status": "Status & Meldungen",
  "config.paramgroup.other": "Weitere Einstellungen",
  // --- Settings-Tabs ---
  "settings.tab.general": "Allgemein",
  "settings.tab.ccus": "CCUs",
  "settings.tab.mqtt": "MQTT",
  "settings.tab.matter": "Matter",
  "settings.tab.mcp": "MCP",
  "settings.tab.discovery": "Discovery (mDNS)",
  "settings.tab.rest": "API & WebSocket",
  "settings.tab.oidc": "OIDC",
  "settings.tab.ccu_auth": "CCU-Anmeldung",
  "settings.ccu_auth.hint":
    "Anmeldung an die CCU-eigene Benutzerdatenbank delegieren. Wenn aktiviert, melden sich Nutzer mit ihren CCU-Konten an; lokale Nutzer bleiben als Break-Glass-Fallback. Änderungen werden nach einem Daemon-Neustart wirksam.",
  "settings.tab.callback": "Callback-Ports",
  "settings.tab.reliability": "Zuverlässigkeit",
  "settings.tab.persistence": "Persistenz",
  "settings.tab.visibility": "Ausgeblendete Parameter",
  "settings.tab.groups": "Räume & Gewerke",
  "settings.tab.users": "Benutzer",
  "settings.tab.tokens": "API-Tokens",
  "settings.tab.system": "System",
  "settings.tab.changes": "Geänderte Einstellungen",
  "settings.tab.navviews": "Navigation & Ansichten",

  // --- Editor für Oberflächenprofile -----------------------------
  "navviews.banner":
    "Eine ausgeblendete Ansicht verschwindet aus der Navigation — für alle Benutzer dieses Daemons. API-Token, Loom-Konten und MQTT bleiben unberührt; die regeln Sie über Rollen und Token.",
  "navviews.scope.title": "Wo gilt der eingebettete Modus?",
  "navviews.scope.inside_ha": "Nur in Home Assistant",
  "navviews.scope.inside_ha.desc":
    "Die reduzierte Navigation gilt im Home-Assistant-Panel. Wer die eigene Adresse dieses Daemons öffnet, behält die volle Oberfläche — er hat sich bewusst für Loom statt für das Panel entschieden, der Grund fürs Ausblenden trifft auf ihn nicht zu.",
  "navviews.scope.always": "Überall",
  "navviews.scope.always.desc":
    "Die reduzierte Navigation gilt auf jedem Weg, auch beim Direktzugriff. Sinnvoll für einen Daemon, dessen Oberfläche immer gleich aussehen soll.",
  "navviews.scope.here.inside": "Sie sind im Home-Assistant-Panel",
  "navviews.scope.here.direct": "Sie haben diesen Daemon direkt geöffnet",
  "navviews.toast.scope_saved": "Geltungsbereich gespeichert.",
  "navviews.mode.title": "In Home Assistant eingebettet",
  "navviews.mode.desc":
    "Aktivieren Sie das, wenn Home Assistant die Konfigurationsoberfläche dieses Daemons besitzt — die Integration Homematic(IP) Local also gegen diesen Daemon konfiguriert ist. Loom blendet dann aus, was Home Assistant selbst bereitstellt: eigene Anmeldung, Benutzer- und Token-Verwaltung, die CCU-Verbindung, die Geräte-Editoren, Matter und die Auswertungs-Diagramme.",
  "navviews.mode.live": "Aktives Profil",
  "navviews.mode.views_visible": "{visible} von {total} Ansichten sichtbar",
  "navviews.mode.deviations": "{count} Abweichungen vom Standard",
  "navviews.profile.editing": "Bearbeitetes Profil",
  "navviews.profile.standalone": "Eigenständig",
  "navviews.profile.embedded": "Eingebettet",
  "navviews.profile.live": "aktiv",
  "navviews.profile.reset": "Profil auf Standardwerte zurücksetzen",
  "navviews.search": "Ansicht suchen…",
  "navviews.filter.label": "Ansichten filtern",
  "navviews.filter.all": "Alle",
  "navviews.filter.visible": "Sichtbar",
  "navviews.filter.hidden": "Ausgeblendet",
  "navviews.filter.changed": "Vom Standard abweichend",
  "navviews.group.overview": "Übersicht",
  "navviews.group.automation": "Automatisierung",
  "navviews.group.diagnose": "Diagnose",
  "navviews.group.bridges": "Bridges",
  "navviews.group.system": "System",
  "navviews.group.settings": "Einstellungs-Tabs",
  "navviews.group.device": "Gerätedetail-Tabs",
  "navviews.group.count": "{visible} von {total} sichtbar",
  "navviews.group.show_all": "Alle einblenden",
  "navviews.group.hide_all": "Alle ausblenden",
  "navviews.row.ha_owns": "Home Assistant stellt das bereit.",
  "navviews.row.multi_central":
    "Standardmäßig sichtbar, weil dieser Daemon {count} CCUs bedient: Home Assistant spricht pro Config-Eintrag genau eine CCU an — für die übrigen ist dies der einzige Editor.",
  "navviews.row.locked": "Kann nicht ausgeblendet werden — {why}",
  "navviews.row.unavailable": "Nicht verfügbar — {why}",
  "navviews.row.role_admin": "Nur für Administratoren sichtbar.",
  "navviews.row.opens_hidden":
    "„{target}“ ist ausgeblendet, daher verlinken die Einträge dieser Übersicht nicht. Die Liste selbst bleibt.",
  "navviews.row.opened_by_hidden":
    "Hier ausgeblendet entfällt auch der Sprung aus „{source}“. Diese Übersicht behält ihre Liste.",
  "navviews.row.changed_from": "Geändert · Standard: {default}",
  "navviews.row.default_visible": "sichtbar",
  "navviews.row.default_hidden": "ausgeblendet",
  "navviews.row.reset_one": "Auf Standard zurücksetzen",
  "navviews.preview.title": "Vorschau",
  "navviews.preview.sub_live": "So sieht die Navigation nach dem Speichern aus.",
  "navviews.preview.sub_other":
    "Vorschau des Profils {profile} — derzeit nicht aktiv.",
  "navviews.preview.none": "In diesem Bereich ist nichts sichtbar.",
  "navviews.save.count": "{count} ungespeicherte Änderungen",
  "navviews.save.discard": "Verwerfen",
  "navviews.save.save": "Änderungen speichern",
  "navviews.toast.saved":
    "Navigation gespeichert. Alle Benutzer sehen das neue Layout beim nächsten Seitenwechsel.",
  "navviews.toast.reset":
    "Profil auf Standardwerte zurückgesetzt — noch nicht gespeichert.",
  "navviews.toast.discarded": "Änderungen verworfen.",
  "navviews.toast.mode_on":
    "Eingebetteter Modus ist aktiv. Das Profil „Eingebettet“ ist jetzt aktiv.",
  "navviews.toast.mode_off":
    "Eingebetteter Modus ist aus. Das Profil „Eigenständig“ ist jetzt aktiv.",
  "navviews.toast.error": "Speichern fehlgeschlagen",
  "navviews.dlg.hide_title": "„{surface}“ ausblenden?",
  "navviews.dlg.hide_ok": "Ausblenden",
  "navviews.dlg.mode_on_title": "In den eingebetteten Modus wechseln?",
  "navviews.dlg.mode_on_text":
    "Home Assistant wird der Ort für Identität, CCU-Verbindung und Geräte-Editoren, diese Oberfläche bietet sie dann nicht mehr an. Das ändert, was jeder Benutzer hier sieht — nichts daran, was Home Assistant oder die APIs dürfen.",
  "navviews.dlg.mode_on_ok": "Zu eingebettet wechseln",
  "navviews.dlg.mode_off_title": "Eingebetteten Modus verlassen?",
  "navviews.dlg.mode_off_text":
    "Diese Oberfläche liefert wieder ihren vollen Umfang aus — auch die Ansichten, die Home Assistant ebenfalls bereitstellt.",
  "navviews.dlg.mode_off_ok": "Zu eigenständig wechseln",
  "navviews.dlg.will_hide":
    "{views} Ansichten und {tabs} Einstellungs-Tabs werden ausgeblendet.",
  "navviews.dlg.will_show":
    "{views} Ansichten und {tabs} Einstellungs-Tabs kommen zurück.",
  "navviews.dlg.reset_title": "Profil {profile} zurücksetzen?",
  "navviews.dlg.reset_text":
    "Alle {count} Abweichungen dieses Profils gehen auf die Standardwerte zurück. Geschrieben wird erst beim Speichern.",
  "navviews.dlg.reset_ok": "Profil zurücksetzen",
  "navviews.warn.alarm_armed":
    "Solange das Alarmsystem scharf ist, gibt es mit ausgeblendetem Panel keinen Weg mehr, es in dieser Oberfläche zu entschärfen — MQTT, REST und die Home-Assistant-Integration funktionieren weiter.",
  "navviews.warn.security_faults":
    "Meldungen aus Sicherheit & Gefahrenmelder können nur in dieser Ansicht quittiert werden.",
  "navviews.warn.last_ccu_editor":
    "Neue CCUs lassen sich dann nur noch über die Konfigurationsdatei oder die REST-API hinzufügen.",
  "navviews.why.core": "die Geräteliste ist der Zweck dieser Oberfläche.",
  "navviews.why.settings":
    "ohne Einstellungen gibt es keinen Weg zurück — auch nicht zu diesem Editor.",
  "navviews.why.editor":
    "dieser Editor wäre dann nur noch per YAML oder REST-API reparierbar.",
  "navviews.why.about":
    "die einzige Stelle in der App, die Version und Build nennt — dort beginnt jede Support-Anfrage.",
  "navviews.why.identity":
    "im eigenständigen Betrieb ist das die einzige Stelle, um Passwort oder Token zu wechseln.",
  "navviews.gate.matter": "die Matter-Bridge ist ausgeschaltet.",
  "navviews.gate.history": "die Messwert-Historie ist ausgeschaltet.",

  // --- Oberflächenprofile (Einstellungen → Navigation & Ansichten) ---
  "surface.desc.nav.overview": "Kacheln für alle Geräte, nach Raum gruppiert.",
  "surface.desc.nav.devices": "Die Geräteliste und alle Gerätedetailseiten.",
  "surface.desc.nav.favorites": "Ihre markierten Geräte und Kanäle.",
  "surface.desc.nav.alarm": "Scharfschaltung, Zonen, Sensoren und Sirenen.",
  "surface.desc.nav.security":
    "Rauch, Wasser, Sabotage und Stromversorgung mit ihrem Störungszustand.",
  "surface.desc.nav.inbox": "Anlernbereite Geräte und der Anlernmodus.",
  "surface.desc.nav.fleet":
    "Alle konfigurierten CCUs mit ihrem Verbindungszustand.",
  "surface.desc.nav.programs":
    "CCU-Programme ausführen, aktivieren und den letzten Lauf sehen.",
  "surface.desc.nav.sysvars":
    "CCU-Systemvariablen lesen und schreiben, inklusive Kanalzuordnung.",
  "surface.desc.nav.groups": "HmIP-Gruppen auf der CCU und ihre Mitglieder.",
  "surface.desc.nav.links":
    "Fleet-weite, schreibgeschützte Liste aller Direktverknüpfungen.",
  "surface.desc.nav.messages":
    "Batterie schwach, nicht erreichbar, Sabotage — mit Quittierung.",
  "surface.desc.nav.diagnostics":
    "Verbindungszustand, Drosselung, Circuit-Breaker und der RPC-Rekorder.",
  "surface.desc.nav.energy": "Verbrauch und Leistung aller Messgeräte.",
  "surface.desc.nav.diagrams":
    "Aufgezeichnete Messkurven für beliebige Datenpunkte.",
  "surface.desc.nav.signal": "RSSI je Gerät, die schwächsten Strecken zuerst.",
  "surface.desc.nav.audit":
    "Wer hat wann was geändert — Konfiguration und Geräteschreibzugriffe.",
  "surface.desc.nav.logs": "Der Live-Logstream des Daemons mit Filtern.",
  "surface.desc.nav.matter":
    "Homematic-Geräte an Apple Home, Google Home oder Alexa bridgen.",
  "surface.desc.nav.firmware":
    "Verfügbare Geräte-Firmware-Updates und ihr Rollout-Zustand.",
  "surface.desc.nav.backups":
    "CCU- und Daemon-Sicherungen erstellen, herunterladen, zurückspielen.",
  "surface.desc.nav.settings": "Alles in diesem Bereich.",
  "surface.desc.nav.about":
    "Version, Build, Add-on-Stempel und Lizenzangaben.",

  "surface.desc.settings.general":
    "Sprache, Loglevel und die Identität des Daemons.",
  "surface.desc.settings.system":
    "Neustart, Update und Laufzeitinformationen.",
  "surface.desc.settings.navviews": "Dieser Editor.",
  "surface.desc.settings.changes":
    "Konfigurationsfelder, die von der laufenden Boot-Konfiguration abweichen.",
  "surface.desc.settings.mqtt":
    "Broker-Verbindung, Topic-Aufbau und Home-Assistant-Discovery.",
  "surface.desc.settings.matter":
    "Matter-Bridge, Inbetriebnahme und gekoppelte Controller.",
  "surface.desc.settings.mcp":
    "Der Model-Context-Protocol-Server und seine Schreibwerkzeuge.",
  "surface.desc.settings.rest":
    "Listener, TLS und CORS für die Northbound-API.",
  "surface.desc.settings.discovery":
    "Wie sich dieser Daemon im Netzwerk bekannt macht.",
  "surface.desc.settings.ccus":
    "CCUs hinzufügen, bearbeiten und finden, mit denen dieser Daemon spricht.",
  "surface.desc.settings.callback": "XML-RPC- und BIN-RPC-Callback-Ports.",
  "surface.desc.settings.oidc":
    "Single Sign-on über einen externen Identitätsanbieter.",
  "surface.desc.settings.ccu_auth":
    "Zugangsdaten, mit denen sich dieser Daemon an der CCU anmeldet.",
  "surface.desc.settings.users": "Lokale Benutzerkonten und ihre Rollen.",
  "surface.desc.settings.groups":
    "Die Räume und Gewerke der CCU und die Kanäle, die dazugehören.",
  "surface.desc.settings.tokens": "Langlebige Token für Maschinen-Clients.",
  "surface.desc.settings.visibility":
    "Welche Datenpunkte auf den Northbound-Ebenen unterdrückt werden.",
  "surface.desc.settings.reliability":
    "Retry-, Drossel- und Circuit-Breaker-Einstellungen.",
  "surface.desc.settings.persistence":
    "Datenbankort, Aufbewahrung und Vacuum-Zeitplan.",

  "surface.desc.device.overview":
    "Live-Werte und Bedienelemente des gewählten Geräts.",
  "surface.desc.device.configure":
    "Der gesamte Konfigurationstab inklusive seiner Untertabs.",
  "surface.desc.device.configure.device-config":
    "MASTER- und VALUES-Paramsets mit Bearbeitungssitzung und Undo.",
  "surface.desc.device.configure.channels":
    "Die Kanalleiste, mit der ausgewählt wird, welchen Kanal der Editor zeigt.",
  "surface.desc.device.configure.links":
    "Verknüpfungen dieses Geräts anlegen und löschen.",
  "surface.desc.device.configure.schedule":
    "Wochenprogramme für Heizung und Schaltvorgänge.",
  "surface.desc.device.history":
    "Die aufgezeichnete Kurve eines beliebigen Parameters dieses Geräts.",

  "groups.central_label": "Zentrale",
  "groups.created": "Erstellt.",
  "groups.deleted": "Entfernt.",
  "groups.delete_function_confirm": "Gewerk entfernen?",
  "groups.delete_room_confirm": "Raum entfernen?",
  "groups.empty_functions": "Keine Gewerke konfiguriert.",
  "groups.empty_rooms": "Keine Räume konfiguriert.",
  "groups.function_placeholder": "Gewerkname…",
  "groups.functions_title": "Gewerke",
  "groups.rename": "Umbenennen",
  "groups.renamed": "Umbenannt.",
  "groups.room_placeholder": "Raumname…",
  "groups.rooms_title": "Räume",
  // --- RoomsFunctions table column labels ---
  "roomsfn.col.name": "Name",
  "roomsfn.col.count": "Kanäle",
  "roomsfn.col.actions": "Aktionen",
  "changes.revert": "Zurücknehmen",
  "changes.revert_confirm":
    "Eigenen Wert für „{field}“ verwerfen und auf den eingebauten Standard zurückfallen? Das lässt sich hier nicht rückgängig machen.",
  "changes.reverted": "Feld auf Standard zurückgesetzt",
  "changes.empty": "Keine geänderten Einstellungen — alles auf Standard.",
  "changes.n_entries": "{count} Einträge",
  "changes.manage_ccus": "Zu CCUs",
  "changes.not_revertible": "Hier nicht rücknehmbar",
  "changes.intro":
    "Von dir überschriebene Einstellungen. Einzeln auf Standard zurücksetzbar.",
  "settings.restart_later": "Später",
  "settings.reset_confirm":
    "Gespeicherte Überschreibung für diesen Bereich entfernen? Der Daemon fällt beim nächsten Neustart auf die eingebauten Standards zurück.",
  "settings.reset_done": "Bereich auf eingebaute Standards zurückgesetzt.",
  "restart.banner_text":
    "Konfigurationsänderungen werden erst nach einem Neustart des Daemons wirksam.",
  "restart.now": "Jetzt neu starten",
  "settings.json_parse_error": "Ungültiges JSON — Syntax prüfen.",
  "settings.duration_parse_error":
    "Ungültige Dauer. Go-Syntax: 60s, 5m, 250ms, 1h30m.",
  "settings.tristate.default": "Standard",
  "settings.tristate.on": "An",
  "settings.tristate.off": "Aus",
  // --- Rollen (gemeinsam für die Einstellungs-Tabs Benutzer und Token) ---
  "role.viewer": "Betrachter",
  "role.operator": "Bediener",
  "role.admin": "Administrator",
  // --- Benutzer-Verwaltung ---
  "users.empty": "Keine Benutzer konfiguriert.",
  "users.add": "Benutzer hinzufügen",
  "users.add_title": "Benutzer hinzufügen",
  "users.edit_title": "Benutzer bearbeiten",
  "users.password_leave_blank": "Leer lassen um beizubehalten",
  "users.degraded_note":
    "Der Live-Benutzerspeicher ist nicht verfügbar. Die angezeigten Benutzer stammen aus der Bootstrap-Liste und können hier nicht bearbeitet werden. Verwalten Sie Benutzer über die config.yaml.",
  "users.created": "Benutzer erstellt.",
  "users.deleted": "Benutzer entfernt.",
  "users.password_changed": "Passwort geändert.",
  "users.role_changed": "Rolle aktualisiert.",
  "users.last_admin_error": "Letzter Admin kann nicht entfernt werden.",
  "users.last_admin_demote_error":
    "Letzter Admin kann nicht herabgestuft werden.",
  "users.exists_error": "Ein Benutzer mit diesem Namen existiert bereits.",
  "users.new_password": "Neues Passwort",
  "users.password": "Passwort",
  "users.confirm_delete_title": "Benutzer entfernen?",
  "users.confirm_delete_body":
    'Benutzer "{subject}" wirklich entfernen? Diese Aktion lässt sich nicht rückgängig machen.',
  "users.col.subject": "Benutzername",
  "users.col.role": "Rolle",
  "users.col.created": "Erstellt",
  "users.col.last_seen": "Zuletzt gesehen",
  "users.col.actions": "Aktionen",
  // --- Token-Verwaltung ---
  "tokens.empty": "Keine API-Tokens vorhanden.",
  "tokens.create": "Token erstellen",
  "tokens.create_title": "API-Token erstellen",
  "tokens.revoke": "Widerrufen",
  "tokens.revoked": "Token widerrufen.",
  "tokens.reveal_title": "Token erstellt",
  "tokens.reveal_warning":
    "Dieser Token wird nicht erneut angezeigt. Jetzt kopieren.",
  "tokens.copied": "Kopiert!",
  "tokens.copy_failed":
    "Kopieren fehlgeschlagen — die Zwischenablage benötigt einen sicheren (HTTPS-)Kontext. Der Token ist markiert und kann manuell kopiert werden.",
  "tokens.confirm_revoke_title": "Token widerrufen?",
  "tokens.confirm_revoke_body":
    "Token {fingerprint} widerrufen? Jeder Client, der ihn verwendet, verliert sofort den Zugriff.",
  "tokens.col.subject": "Subject",
  "tokens.col.role": "Rolle",
  "tokens.col.fingerprint": "Fingerprint",
  "tokens.col.created": "Erstellt",
  "tokens.col.last_seen": "Zuletzt gesehen",
  "tokens.col.actions": "Aktionen",
  // --- Entdeckung ---
  "discovery.add": "Hinzufügen",
  "discovery.already_configured": "Bereits konfiguriert",
  "discovery.empty": "Keine CCUs im Netzwerk gefunden.",
  "discovery.found_hint":
    "Im Netzwerk per SSDP gefundene CCUs - auf Hinzufügen klicken, um das Formular vorzubefüllen.",
  "discovery.ignore": "Ignorieren",
  "discovery.ignore_confirm":
    '"{name}" ({serial}) ignorieren? Die CCU erscheint dann nicht mehr in der Gefundene-CCUs-Liste.',
  "discovery.ignored": '"{name}" ignoriert.',
  "discovery.refresh": "Aktualisieren",
  "discovery.title": "Gefundene CCUs",
  // --- CCU-Verwaltung ---
  "centrals.empty": "Keine CCUs konfiguriert.",
  "centrals.add": "CCU hinzufügen",
  "centrals.col.name": "Name",
  "centrals.col.host": "Host",
  "centrals.col.status": "Status",
  "centrals.col.actions": "Aktionen",
  "centrals.add_title": "CCU hinzufügen",
  "centrals.edit_title": "CCU bearbeiten",
  "centrals.created": "CCU hinzugefügt.",
  "centrals.updated": "CCU aktualisiert.",
  "centrals.updated_restart_required":
    "CCU-Einstellungen gespeichert. Ein Neustart des Diensts ist erforderlich, damit sie auf die laufende Verbindung angewendet werden.",
  "centrals.deleted": "CCU entfernt.",
  "centrals.enabled": "CCU aktiviert.",
  "centrals.disabled": "CCU deaktiviert.",
  "centrals.confirm_delete_title": "CCU entfernen?",
  "centrals.confirm_delete_body":
    'CCU "{name}" wirklich entfernen? Geräte dieser CCU werden nicht mehr erreichbar sein.',
  "centrals.field.name": "Name",
  "centrals.field.name_hint": "Nur Buchstaben, Ziffern, - und _ — der Name wird Teil der Callback-URL, an die die CCU Ereignisse sendet.",
  "centrals.field.host": "Host",
  "centrals.field.interfaces": "Interfaces",
  "centrals.field.port": "Port",
  "centrals.field.port_hint":
    "Port leer lassen, um den Standardwert zu verwenden. Nur überschreiben, wenn die CCU einen abweichenden Port nutzt.",
  "centrals.field.json_rpc_port": "JSON-RPC-Port",
  "centrals.field.json_rpc_port_hint":
    "CCU-Web-/ReGa-Port für JSON-RPC. Leer = Standard (80, mit TLS 443). Nur bei abweichendem CCU-HTTP-Port überschreiben.",
  "centrals.field.primary_interface": "Primäres Interface",
  "centrals.behavior.title": "Erweitertes Verhalten",
  "centrals.behavior.light_last_brightness":
    "Letzte Helligkeit beim Einschalten wiederherstellen",
  "centrals.behavior.use_group_channel_for_cover_state":
    "Rollladen-Position vom Gruppenkanal melden",
  "centrals.behavior.enable_sysvar_scan": "Systemvariablen scannen",
  "centrals.behavior.enable_program_scan": "Programme scannen",
  "centrals.behavior.include_internal_sysvars":
    "Interne Systemvariablen einschließen",
  "centrals.behavior.include_internal_programs":
    "Interne Programme einschließen",
  "centrals.behavior.enable_device_firmware_check":
    "Firmware-Update-Entitäten anzeigen",
  "centrals.behavior.delay_new_device_creation":
    "Neue Geräte erst über den Posteingang anlegen",
  "centrals.behavior.sysvar_scan_interval":
    "Scan-Intervall für Systemvariablen (Sekunden, 0 = Standard 30, Minimum 3)",
  "centrals.behavior.sysvar_markers": "Systemvariablen-Marker",
  "centrals.behavior.program_markers": "Programm-Marker",
  "centrals.behavior.markers_hint":
    "Marker sind Textkürzel, die Sie in der CCU-WebUI in die Beschreibung des Eintrags schreiben. Sie entscheiden nicht, was importiert wird — importiert wird alles, was die CCU anbietet. Sie entscheiden, wie ein Eintrag ankommt: Ein Eintrag, der zu einem hier angehakten Marker passt, kommt aktiviert an, alle anderen kommen deaktiviert an und werden einzeln eingeschaltet.",
  "centrals.behavior.marker.hahm":
    "Macht die Systemvariable schreibbar — sie kommt als Schalter, Auswahl, Zahl oder Textfeld an statt als reiner Sensor.",
  "centrals.behavior.marker.hx": "Freier Marker für eigene Filterung.",
  "centrals.behavior.marker.internal.sysvar":
    "Liefert zusätzlich die internen Variablen der CCU aus — jene, die sie für sich selbst führt, nicht die von Ihnen angelegten.",
  "centrals.behavior.marker.internal.program":
    "Liefert zusätzlich die internen Programme der CCU aus. Das wiegt schwerer, als es klingt: Die CCU kennzeichnet die meisten gewöhnlichen Benutzerprogramme als intern — ohne diesen Marker bleibt die Programmliste fast leer.",
  "centrals.field.username": "Benutzername",
  "centrals.field.password": "Passwort",
  "centrals.field.password_hint":
    "Wird in der SQLite-Datenbank des Daemons abgelegt (Dateirechte 0600). Backup-Archive redaktieren das Feld, außer du verwendest --include-secrets.",
  "centrals.field.password_hint_unchanged":
    "Es ist ein Passwort gespeichert. Leer lassen, um es zu behalten — ein neues eingeben, um es zu ersetzen.",
  "centrals.field.password_placeholder_env": "(wird aus Env-Variable gelesen)",
  "centrals.field.password_env": "Passwort-Umgebungsvariable (überschreibt)",
  "centrals.field.password_env_hint":
    "Optional. Name einer Env-Variable; wenn gesetzt, hat sie Vorrang vor dem Passwortfeld oben. Sinnvoll für Kubernetes / Vault / systemd-creds. Details siehe README → Secrets.",
  "centrals.field.tls_insecure": "TLS-Prüfung überspringen",
  "centrals.field.tls_insecure_warn":
    "Deaktiviert Zertifikatsketten- und Hostnamen-Prüfung. Nur bei CCUs mit selbst-signierten Zertifikaten in vertrauenswürdigen Netzen einsetzen.",
  "centrals.error.no_interface": "Mindestens eine Schnittstelle auswählen.",
  "centrals.error.invalid_name": "Der Name darf nur Buchstaben, Ziffern, - und _ enthalten.",
  "sysvars.title": "Systemvariablen",
  "sysvars.empty": "Keine Variablen.",
  "sysvars.col.name": "Name",
  "sysvars.col.type": "Typ",
  "sysvars.col.value": "Wert",
  "sysvars.col.actions": "Aktionen",
  "sysvars.create.title": "Neue Variable",
  "sysvars.create.name": "Name",
  "sysvars.create.type": "Typ",
  "sysvars.create.unit": "Einheit",
  "sysvars.create.values": "Werte (Semikolon-getrennt)",
  "sysvars.create.alarm_hint":
    "Legt eine binäre, quittierbare Alarmlinie auf der CCU an.",
  "sysvars.edit.title": "Bearbeiten",
  "sysvars.edit.name": "Name (umbenennen)",
  "sysvars.edit.description": "Beschreibung",
  "sysvars.edit.note":
    "Typ-Änderungen erfordern Löschen + Neuanlegen. Hier werden nur Metadaten aktualisiert.",
  "sysvars.edit.bound_required":
    "Eine numerische Systemvariable hat auf der CCU immer beide Grenzen — sie lassen sich ändern, aber nicht entfernen. Bitte für Minimum und Maximum einen Wert eintragen.",
  "sysvars.confirm_remove": 'Systemvariable "{name}" wirklich entfernen?',
  "sysvars.usage.warning":
    "Achtung: {count} Programm(e) verwenden diese Variable und sind vom Löschen betroffen:",
  "sysvars.usage.internal": "intern",
  "sysvars.removed": "{name} entfernt.",
  "sysvars.created": "Variable erstellt.",
  "sysvars.updated": "{name} aktualisiert.",
  "sysvars.saved": "{name} gespeichert.",
  "sysvars.count": "{count} Variablen",
  "sysvars.edit.tooltip": "Metadaten bearbeiten",
  "sysvars.remove.tooltip": "Variable entfernen",
  "sysvars.labels.title": "Wertelabels",
  "sysvars.labels.value0": "Bezeichnung für „falsch“",
  "sysvars.labels.value1": "Bezeichnung für „wahr“",
  "sysvars.labels.hint":
    "Für den Bediener sichtbarer Text je Zustand einer binären (BOOL/ALARM) Variable.",
  "sysvars.flags.visible": "Im CCU-WebUI sichtbar",
  "sysvars.flags.logged": "Werteänderungen protokollieren",
  "sysvars.channel.label": "Kanalzuordnung",
  "sysvars.channel.hint":
    "Variable an einen Gerätekanal binden (die CCU-„Kanalzuordnung“). Optional — ohne Zuordnung hängt sie am Hub.",
  "sysvars.channel.none": "Nicht zugeordnet",
  "sysvars.channel.clear": "Aufheben",
  "sysvars.channel.search": "Gerät suchen…",
  "sysvars.channel.no_devices": "Keine Geräte",
  "sysvars.channel.load_failed": "Kanäle konnten nicht geladen werden",
  "device.tab.control": "Bedienen",
  "device.tab.values": "Werte",
  "device.tab.master": "Konfiguration",
  "device.tab.links": "Verknüpfungen",
  "device.tab.schedule": "Zeitplan",
  "device.no_channels": "Dieses Gerät hat keine Kanäle.",
  "device.all_devices": "Alle Geräte",
  "device.offline": "offline",
  "device.update_available": "Update verfügbar",
  "device.firmware_update": "Firmware aktualisieren",
  "device.firmware_update.tooltip":
    "Firmware-Update auslösen ({current} → {available})",
  "device.firmware_triggered": "Firmware-Update angestoßen.",
  "device.confirm_remove":
    'Gerät "{name}" wirklich entfernen?\n\nDie Kopplung wird aus der CCU gelöst.',
  "device.confirm_firmware":
    'Firmware-Update für "{name}" jetzt anstoßen? Das Gerät bleibt während des Updates kurzzeitig nicht erreichbar.',
  "device.removed": "Gerät entfernt.",
  "device.renamed": "Gerät umbenannt.",
  "device.rename_include_channels": "Kanäle mitbenennen",
  "channel.rename": "Kanal umbenennen",
  "channel.renamed": "Kanal umbenannt.",
  "channel.rooms": "Räume",
  "channel.functions": "Gewerke",
  "channel.rooms_updated": "Kanal-Räume aktualisiert.",
  "channel.functions_updated": "Kanal-Gewerke aktualisiert.",
  "remote.key_grid_title": "Tastensimulation",
  "remote.key_n": "Taste {n}",
  "remote.press_short": "Kurz",
  "remote.press_long": "Lang",
  "remote.press_short_aria": "Taste {n} kurz drücken",
  "remote.press_long_aria": "Taste {n} lang drücken",
  "remote.press_short_title": "{title}: kurzer Tastendruck",
  "remote.press_long_title": "{title}: langer Tastendruck",
  "remote.press_failed": "Tastendruck fehlgeschlagen",
  "remote.press_just_now": "{kind} jetzt",
  "remote.press_ago_sec": "{kind} vor {n} Sek.",
  "remote.press_ago_min": "{kind} vor {n} Min.",
  "remote.press_ago_hour": "{kind} vor {n} Std.",
  "remote.press_ago_day": "{kind} vor {n} Tg.",
  "device.rooms_updated": "Räume aktualisiert.",
  "device.functions_updated": "Gewerke aktualisiert.",
  "device.rooms": "Räume",
  "device.functions": "Gewerke",
  "device.rooms.placeholder": "Wohnzimmer, Küche, …",
  "device.functions.placeholder": "Licht, Heizung, …",
  "roomfn.placeholder.room": "Raum suchen oder anlegen…",
  "roomfn.placeholder.function": "Gewerk suchen oder anlegen…",
  "roomfn.remove": "Entfernen",
  "roomfn.remove_named": "{name} entfernen",
  "roomfn.no_matches": "Keine Treffer",
  "roomfn.create": "+ „{name}“ anlegen",
  "roomfn.create.room": "+ Raum „{name}“ anlegen",
  "roomfn.create.function": "+ Gewerk „{name}“ anlegen",
  "roomfn.created.room": "Raum angelegt.",
  "roomfn.created.function": "Gewerk angelegt.",
  "device.rename": "Umbenennen",
  "device.remove": "Entfernen",
  "device.channel_n": "Kanal {n}",
  "device.error_label": "Fehler: {message}",
  "device.export_definition": "Definition exportieren",
  "device.export_definition_success": "Definition heruntergeladen.",
  "device.export_definition_error": "Export fehlgeschlagen.",
  "channel.loading_schema": "Lade Schema…",
  "channel.schema_failed": "Schema konnte nicht geladen werden",
  "channel.snapshot_downloaded": "Snapshot heruntergeladen.",
  "channel.session_lock_other":
    "Dieser Editor wird gerade von einer anderen Sitzung bearbeitet. Speichern kann scheitern, bis die Sperre abläuft.",
  "channel.take_over": "Bearbeitung übernehmen",
  "channel.take_over_failed": "Übernahme fehlgeschlagen",
  "channel.lock_lost": "Bearbeitungssperre verloren",
  "channel.lock_lost_detail":
    "Eine andere Sitzung hat die Bearbeitungssperre übernommen oder deine Sperre ist abgelaufen. Öffne diesen Editor erneut, bevor du speicherst, um gleichzeitige Änderungen nicht zu überschreiben.",
  "channel.tab.common": "Allgemein",
  "channel.tab.short": "Kurzer Tastendruck",
  "channel.tab.long": "Langer Tastendruck",
  "quick.on": "Ein",
  "quick.off": "Aus",
  "programs.toggle.tooltip": "Aktivierung umschalten",
  "programs.executed": 'Programm "{name}" ausgeführt.',
  "programs.not_executed":
    'Programm "{name}" nicht ausgeführt — Bedingung nicht erfüllt.',
  "programs.check_conditions": "Nur ausführen, wenn Bedingung erfüllt",
  "programs.toggle_done": 'Programm "{name}" {state}.',
  "programs.enabled": "aktiviert",
  "programs.disabled": "deaktiviert",
  "programs.active": "aktiv",
  "programs.col.name": "Programm",
  "programs.col.status": "Status",
  "programs.col.condition": "Bedingung",
  "programs.col.activity": "Aktivität",
  "programs.col.last_executed": "Zuletzt ausgeführt",
  "programs.col.actions": "Aktionen",
  "programs.never_executed": "nie",
  "programs.inactive": "inaktiv",
  "programs.count": "{count} Programme",
  "programs.confirm_run": 'Programm "{name}" ausführen?',
  "programs.confirm_delete":
    'Programm "{name}" löschen? Dies kann nicht rückgängig gemacht werden.',
  "programs.deleted": 'Programm "{name}" gelöscht.',
  "programs.delete.tooltip": "Programm von der CCU löschen",
  "programs.show_internal": "Systemprogramme anzeigen",
  "schedules.title": "Zeitprogramme",
  "schedules.subtitle": "Alle Geräte mit Wochenprogramm, über alle CCUs hinweg.",
  "schedules.empty": "Kein Gerät hat ein Wochenprogramm.",
  "schedules.empty.description":
    "Thermostate sowie Schalter und Rollläden mit Wochenprofil-Kanal erscheinen hier, sobald sie angelernt sind.",
  "schedules.no_matches": "Kein Gerät passt zur Suche.",
  "schedules.search": "Suche nach Name, Adresse oder Typ…",
  "schedules.kind.climate": "Thermostat",
  "schedules.kind.week_profile": "Wochenprofil",
  "schedules.editor_hidden":
    "Der Zeitplan-Editor ist in diesem Profil ausgeblendet (Einstellungen → Navigation & Ansichten). Die Übersicht bleibt, ihre Einträge verlinken nicht.",
  "surface.desc.nav.schedules":
    "Alle Geräte mit Wochenprogramm, mit Link zum jeweiligen Editor.",
  "links.title": "Direktverknüpfungen",
  "links.empty": "Keine Direktverknüpfungen.",
  "links.add": "Verknüpfung hinzufügen",
  "links.remove": "Entfernen",
  "links.removed": "Verknüpfung entfernt.",
  "links.sender": "Sender",
  "links.receiver": "Empfänger",
  "links.name": "Name",
  "links.search": "Suche nach Name, Sender, Empfänger…",
  "links.count": "{count} Verknüpfungen",
  "links.empty.description":
    "Direktverknüpfungen lassen zwei Kanäle ohne CCU-Programm miteinander kommunizieren. Es gibt noch keine.",
  "links.no_matches": "Keine Verknüpfung passt zur Suche.",
  "links.central": "CCU",
  "links.edit_on_device": "Am Gerät bearbeiten",
  "links.editor_hidden":
    "Der Verknüpfungs-Editor ist in diesem Profil ausgeblendet (Einstellungen → Navigation & Ansichten). Die Übersicht bleibt, ihre Einträge verlinken nicht.",
  "profile.test.short": "Test (kurzer Tastendruck)",
  "profile.test.long": "Test (langer Tastendruck)",
  "links.test.ok": "Verknüpfung am Gerät ausgelöst.",
  "links.test.error": "Verknüpfung konnte nicht ausgelöst werden.",
  "links.test.unsupported":
    "Dieses Interface unterstützt keinen Verknüpfungstest.",
  "links.test.confirm_title": "Verknüpfung am Gerät testen?",
  "links.test.confirm_body":
    "Dies löst den Empfänger physisch aus (ein Schalter klickt, ein Rollladen fährt), als hätte der Sender ausgelöst. Fortfahren?",
  "central.title": "Tastendruck an Zentrale",
  "central.subtitle":
    "Steuert, ob die CCU Tastendruck-Ereignisse (PRESS_SHORT/LONG) an OpenCCU-Loom weiterleitet.",
  "central.help.summary":
    "Warum ein Taster scheinbar nichts tut und was das Aktivieren kostet",
  "central.help.no_link":
    "Ohne aktivierte Weiterleitung senden viele HmIP-Taster ihre Tasterevents gar nicht an die CCU oder OpenCCU-Loom — das ist die häufigste Ursache dafür, dass ein Taster scheinbar nichts tut.",
  "central.help.duty_cycle":
    "Das Aktivieren legt eine interne Verknüpfung an und erhöht den Funk-DutyCycle sowie den Batterieverbrauch des Geräts.",
  "central.unsupported":
    "Diese Schnittstelle unterstützt kein Event-Routing zur Zentrale.",
  "central.eligible": "Tasten-Kanäle",
  "central.active": "Aktiv",
  "central.inactive": "Inaktiv",
  "central.active_count": "{count} aktiv",
  "central.enable": "Aktivieren",
  "central.disable": "Deaktivieren",
  "central.unsupported_badge": "nicht unterstützt",
  "central.report.enabled":
    "Aktiviert: {touched} Kanal/Kanäle, {skipped} übersprungen, {failed} fehlgeschlagen.",
  "central.report.disabled":
    "Deaktiviert: {touched} Kanal/Kanäle, {skipped} übersprungen, {failed} fehlgeschlagen.",
  "central.device_wide": "Ganzes Gerät",
  "central.per_channel": "Pro Kanal",
  "central.channel_label": "Kanal {number}",
  "central.confirm.enable_title": "Tasterevent-Weiterleitung aktivieren?",
  "central.confirm.enable_body":
    "Die CCU leitet die Tasterevents (PRESS_SHORT/PRESS_LONG) dieses Kanals an OpenCCU-Loom und an CCU-Programme weiter. Das erhöht den Funk-DutyCycle und den Batterieverbrauch des Geräts.",
  "central.confirm.disable_title": "Tasterevent-Weiterleitung deaktivieren?",
  "central.confirm.disable_body":
    "CCU-seitige Programme könnten diese Tasterevents nutzen. Nach dem Deaktivieren erhalten weder CCU-Programme noch OpenCCU-Loom Tasterevents dieses Kanals.",
  "central.action_failed":
    "Tasterevent-Weiterleitung konnte nicht geändert werden",
  "schedule.loading": "Lade Zeitplan…",
  "schedule.unsupported": "Dieses Gerät unterstützt keinen Zeitplan.",
  "schedule.unsupported_channel":
    "Dieser Kanal unterstützt keinen Klima-Zeitplan.",
  "schedule.profile_active": "Aktives Profil: {profile}",
  "schedule.base_temperature": "Grundtemperatur",
  "schedule.weekday_overview": "Wochenübersicht",
  "schedule.click_to_edit": "Tag anklicken, um zu bearbeiten",
  "schedule.astro": "Astro",
  "schedule.condition": "Bedingung",
  "schedule.duration": "Dauer",
  "schedule.ramp_time": "Rampe",
  "schedule.color": "Farbe",
  "schedule.color.hue_saturation": "Farbe (Farbton/Sättigung)",
  "schedule.color.temperature": "Farbtemperatur",
  "schedule.color.effect": "Effekt",
  "schedule.target_channels": "Zielkanäle",
  "schedule.level": "Stufe",
  "schedule.simple_title": "Schaltzeiten",
  "schedule.slots_count": "{count} / {max} Schaltpunkte",
  "schedule.add_slot": "+ Schaltzeit",
  "schedule.empty_slots":
    "Keine Schaltzeiten — auf '+' klicken um eine anzulegen.",
  "schedule.max_reached": "Maximum von {max} Schaltzeiten erreicht.",
  "schedule.weekday_select_one": "Slot {n}: Mindestens ein Wochentag wählen.",
  "schedule.invalid_time": "Slot {n}: Zeit {time} ist ungültig.",
  "schedule.saved_toast": "Zeitplan gespeichert.",
  "schedule.save_failed": "Speichern fehlgeschlagen",
  "schedule.cover.position": "Behang",
  "schedule.cover.slat": "Lamellen",
  "schedule.lock.mode": "Modus",
  "schedule.lock.action": "Aktion",
  "schedule.lock.permission": "Berechtigung",
  "schedule.lock.door_lock": "Türschloss",
  "schedule.lock.user_permission": "Berechtigung",
  "schedule.lock.action.lock_autorelock_end": "Abschließen + Ende",
  "schedule.lock.action.lock_autorelock_start": "Abschließen + Start",
  "schedule.lock.action.unlock_autorelock_end": "Aufschließen + Ende",
  "schedule.lock.action.autorelock_end": "Auto-Relock-Ende",
  "schedule.lock.granted": "Erlaubt",
  "schedule.lock.not_granted": "Verweigert",
  "schedule.astro.sunrise": "Sonnenaufgang",
  "schedule.astro.sunset": "Sonnenuntergang",
  "schedule.astro.offset": "Offset",
  "schedule.advanced": "Erweitert",
  "schedule.cond.fixed_time": "Feste Zeit",
  "schedule.cond.astro": "Astro",
  "schedule.cond.fixed_if_before_astro": "Feste Zeit, wenn vor Astro",
  "schedule.cond.astro_if_before_fixed": "Astro, wenn vor fester Zeit",
  "schedule.cond.fixed_if_after_astro": "Feste Zeit, wenn nach Astro",
  "schedule.cond.astro_if_after_fixed": "Astro, wenn nach fester Zeit",
  "schedule.cond.earliest_of_fixed_and_astro": "Frühester Termin",
  "schedule.cond.latest_of_fixed_and_astro": "Spätester Termin",
  "schedule.viz.aria": "Wochenplan-Visualisierung",
  "schedule.entry.level": "Stufe",
  "schedule.targets.all_default": "Alle (CCU-Standard)",
  "schedule.targets.selected": "{count} ausgewählt",
  "schedule.targets.all": "Alle",
  "schedule.targets.none": "Keine",
  "weekday.short.MONDAY": "Mo",
  "weekday.short.TUESDAY": "Di",
  "weekday.short.WEDNESDAY": "Mi",
  "weekday.short.THURSDAY": "Do",
  "weekday.short.FRIDAY": "Fr",
  "weekday.short.SATURDAY": "Sa",
  "weekday.short.SUNDAY": "So",
  "weekday.long.MONDAY": "Montag",
  "weekday.long.TUESDAY": "Dienstag",
  "weekday.long.WEDNESDAY": "Mittwoch",
  "weekday.long.THURSDAY": "Donnerstag",
  "weekday.long.FRIDAY": "Freitag",
  "weekday.long.SATURDAY": "Samstag",
  "weekday.long.SUNDAY": "Sonntag",
  "climate.base_label": "Grundtemperatur",
  "climate.add_period": "+ Periode",
  "climate.all_day": "Ganztägig auf Grundtemperatur {temp} °C",
  "climate.day_copied": "Tag kopiert ({count} Perioden).",
  "climate.day_pasted": "Eingefügt in {day}.",
  "climate.fill_all_done": "Montag auf alle Tage übertragen.",
  "climate.fill_all": "Mo → Alle",
  "climate.fill_all.tooltip": "Montag auf alle Tage übertragen",
  "climate.set_active": "Als aktiv setzen",
  "climate.set_active_failed": "Profil konnte nicht aktiviert werden",
  "climate.profile_active_badge": "aktiv",
  "channel.save_failed": "Speichern fehlgeschlagen",
  "channel.import_failed": "Import fehlgeschlagen",
  "channel.kanal": "Kanal {n}",
  "channel.action_triggered": "Aktion {name} ausgelöst.",
  "channel.action_failed": "Aktion {name} fehlgeschlagen",
  "channel.profile_staged":
    "Profil vorgemerkt — zum Anwenden Speichern drücken.",
  "channel.import_staged":
    "Import vorgemerkt — zum Anwenden Speichern drücken.",
  "channel.import_paramset_mismatch":
    "Paramset-Mismatch: Snapshot={snapshot}, aktuell={current}.",
  "channel.import_invalid_file": "Datei ist kein gültiger OpenCCU-Loom-Export.",
  "channel.import_cross_channel_confirm":
    "Snapshot stammt von {snapshot}. Trotzdem auf {current} anwenden?",
  "channel.lock_count": "{count} Parameter durch Profil gesperrt.",
  "channel.unlock_label": "Sperre aufheben",
  "channel.advanced_label":
    "Erweiterte Parameter anzeigen (Jump-Targets, Bedingungen)",
  "channel.expert_label":
    "Experten-Modus (alle Parameter, auch ohne Übersetzung)",
  "channel.no_params_in_group": "Keine Parameter in dieser Gruppe.",
  "channel.other": "Weitere",
  "channel.cross_validation_error":
    "Widersprüchliche Werte — bitte korrigieren, dann erneut speichern.",
  "channel.export": "Export",
  "channel.import": "Import",
  "channel.export.tooltip": "Aktuelle Werte als JSON sichern",
  "channel.import.tooltip": "Werte aus JSON-Datei laden",
  "channel.undo.tooltip": "Rückgängig (Ctrl+Z)",
  "channel.redo.tooltip": "Wiederholen (Ctrl+Y)",
  "channel.save_n": "Speichern ({count})",
  "channel.unsaved": "Ungespeicherte Änderungen",
  "channel.saved_short": "Gespeichert.",
  // --- Secured transmission (channel/SecureTransmission.svelte) ---
  "channel.flags.hidden.title": "Kanal ausblenden",
  "channel.flags.hidden.help":
    "Entfernt diesen Kanal aus den Bedienflächen (Entitätsliste, MQTT, Matter). Er bleibt hier sichtbar, damit du ihn wieder einblenden kannst.",
  "channel.flags.locked.title": "Bedienung sperren",
  "channel.flags.locked.help":
    "Blockiert Steuer-Schreibzugriffe auf diesen Kanal. Lesen und Konfiguration bleiben unberührt.",
  "channel.flags.saved_toast": "Kanal-Einstellungen gespeichert",
  "channel.flags.failed":
    "Kanal-Einstellungen konnten nicht gespeichert werden",
  "channel.secure_transmission.title": "Gesicherte Übertragung",
  "channel.secure_transmission.help":
    "Funktelegramme dieses Kanals signieren (AES). Erhöht die Sicherheit, steigert aber die Funklast des Kanals und – bei Batteriegeräten – den Batterieverbrauch.",
  "channel.secure_transmission.confirm_title":
    "Gesicherte Übertragung aktivieren?",
  "channel.secure_transmission.confirm_body":
    "Die gesicherte (AES-signierte) Übertragung fügt jedem Befehl eine Bestätigungsrunde hinzu. Das erhöht die Funklast dieses Kanals und – bei Batteriegeräten – den Batterieverbrauch. Trotzdem aktivieren?",
  "channel.secure_transmission.enable": "Aktivieren",
  "channel.secure_transmission.enabled_toast":
    "Gesicherte Übertragung aktiviert.",
  "channel.secure_transmission.disabled_toast":
    "Gesicherte Übertragung deaktiviert.",
  "channel.secure_transmission.failed":
    "Übertragungsmodus konnte nicht geändert werden.",
  // --- Motion-detector brightness helper (channel/brightness-helper.ts) ---
  "channel.brightness.apply": "Helligkeit {value} übernehmen",
  "channel.brightness.apply_tooltip":
    "Aktuelle Helligkeit des Bewegungsmelders ({value}) als diesen Schwellwert übernehmen.",
  // --- DST sub-group headers (channel/dst-groups.ts) ---
  "channel.dst.start_header": "Beginn der Sommerzeit",
  "channel.dst.end_header": "Ende der Sommerzeit",
  // --- Messwert-Verlauf (Geräte-Tab „Verlauf") ---
  "history.chart_title": "Messwert-Verlauf — {name}",
  "history.label_channel": "Kanal:",
  "history.label_parameter": "Parameter:",
  "history.channel_n": "Kanal {n}",
  "history.loading_parameters": "Lade Parameter…",
  "history.no_numeric": "Keine numerischen Parameter auf diesem Kanal.",
  "history.record_label": "Aufzeichnen",
  "history.record_saved": "Aufzeichnungs-Einstellung gespeichert.",
  "history.record_reset": "Auf Standard zurücksetzen",
  "history.record_reset_done": "Auf die Aufzeichnungsrichtlinie zurückgesetzt.",
  "history.record_error": "Aufzeichnung konnte nicht geändert werden: {error}",
  "history.reload": "Neu laden",
  "history.empty": "Keine aufgezeichneten Messwerte in diesem Zeitraum.",
  "history.disabled_title": "Verlaufsaufzeichnung ist deaktiviert",
  "history.disabled_hint":
    "Unter Einstellungen → Persistenz aktivieren, um diesen Wert aufzuzeichnen.",
  "history.enable_link": "Einstellungen öffnen",
  // --- Energie-Ansicht (GET /api/v1/energy) ---
  "energy.title": "Energie",
  "energy.subtitle":
    "Verbrauch und Einspeisung pro Gerät, über die Zeit aggregiert.",
  "energy.central": "Zentrale",
  "energy.group": "Gruppieren nach",
  "energy.group.hour": "Stunde",
  "energy.group.day": "Tag",
  "energy.group.month": "Monat",
  "energy.range": "Zeitraum",
  "energy.preset.24h": "24 Std.",
  "energy.preset.7d": "7 Tage",
  "energy.preset.30d": "30 Tage",
  "energy.preset.12mo": "12 Monate",
  "energy.no_centrals": "Noch keine CCU konfiguriert.",
  "energy.total_consumed": "Verbrauch gesamt",
  "energy.total_feed_in": "Einspeisung gesamt",
  "energy.chart_title": "Verbrauch über die Zeit",
  "energy.chart.all_devices": "Alle Geräte",
  "energy.breakdown_title": "Aufschlüsselung nach Gerät",
  "energy.col.device": "Gerät",
  "energy.col.consumed": "Verbrauch",
  "energy.col.feed_in": "Einspeisung",
  "energy.col.avg_power": "Ø Leistung",
  "energy.col.cost": "Kosten",
  "energy.col.peak_power": "Spitzenleistung",
  "energy.col.reset": "Reset",
  "energy.reset_note":
    "Bei mindestens einem Gerät gab es in diesem Zeitraum einen Zählerreset — der betroffene Bucket meldet den Zählerstand seit dem Reset, kein negatives Delta.",
  "energy.empty": "Keine Energiegeräte mit Daten in diesem Zeitraum.",
  "energy.disabled_title": "Verlaufsaufzeichnung ist deaktiviert",
  "energy.disabled_hint":
    "Unter Einstellungen → Persistenz aktivieren, um Energiedaten zu sehen.",
  "energy.enable_link": "Einstellungen öffnen",
  "links.add.create": "Anlegen",
  "links.add.creating": "Lege an…",
  "links.add.title2": "Neue Verknüpfung",
  "links.add.step1":
    "Schritt 1 — Wähle den Kanal dieses Geräts, der verknüpft werden soll.",
  "links.add.step2": "Schritt 2 — Rolle wählen und Partnerkanal auswählen.",
  "links.add.step3": "Schritt 3 — Prüfe die Zuordnung und bestätige.",
  "links.add.loading_channels": "Lade Kanäle…",
  "links.add.no_linkable": "Keine verknüpfbaren Kanäle vorhanden.",
  "links.add.role": "Rolle",
  "links.add.search_peers": "Suche nach Gerät, Modell oder Kanal…",
  "links.add.loading_peers": "Lade Partner…",
  "links.add.no_peer_matches": "Keine Treffer für die Suche.",
  "links.add.no_compatible": "Keine passenden Kanäle gefunden.",
  "links.add.back": "Zurück",
  "links.add.next": "Weiter",
  "links.add.name_optional": "Name (optional)",
  "links.add.desc_optional": "Beschreibung (optional)",
  "links.add.aria_progress": "Fortschritt",
  "links.config.back_to_list": "Zurück zu den Verknüpfungen",
  "links.config.receiver_section": "Empfänger-Konfiguration",
  "links.config.sender_section": "Sender-Konfiguration",
  "links.created": "Verknüpfung erstellt.",
  "links.wakeup_pending.title": "Gespeichert – wird beim Aufwachen übertragen",
  "links.wakeup_pending.body":
    "Dies ist ein batteriebetriebenes Gerät. Die Änderung ist vorgemerkt und wird erst beim nächsten Aufwachen des Geräts übertragen (z. B. per Tastendruck).",
  "links.removal_failed": "Entfernen fehlgeschlagen",
  "links.no_for_device": "Keine Direktverknüpfungen für dieses Gerät.",
  "links.configure": "Konfigurieren",
  "links.direction": "Richtung",
  "links.outgoing_label": "Ausgehend",
  "links.incoming_label": "Eingehend",
  "links.rename": "Umbenennen",
  "links.rename.title": "Verknüpfung umbenennen",
  "links.rename.name": "Name",
  "links.rename.description": "Beschreibung",
  "links.rename.name_placeholder": "Name der Verknüpfung",
  "links.rename.description_placeholder": "Optionale Beschreibung",
  "links.rename.saving": "Speichern…",
  "links.renamed": "Verknüpfung umbenannt.",
  "links.rename_failed": "Umbenennen fehlgeschlagen",
  "links.confirm_delete": "Verknüpfung {sender} → {receiver} wirklich löschen?",
  "links.links_label": "Verknüpfungen",
  "common.sort": "Sortieren:",
  "common.no_matches": "Keine Treffer.",
  "shortcut.help_open": "Diese Hilfe anzeigen",
  "shortcut.title": "Tastenkürzel",
  "shortcut.group.general": "Allgemein",
  "shortcut.group.editor": "Parameter-Editor",
  "shortcut.close_dialog": "Dialog schließen",
  "shortcut.undo": "Rückgängig",
  "shortcut.redo": "Wiederholen",
  "connection.reconnecting": "verbinde…",
  "connection.daemon_stopping": "Daemon beendet sich",
  "connection.live_on": "Live",
  "connection.live_off": "Live getrennt",
  "connection.tooltip.on":
    "Live-Verbindung aktiv — Änderungen erscheinen sofort.",
  "connection.tooltip.off":
    "Live-Verbindung getrennt — Werte aktualisieren sich nicht automatisch. Die Verbindung wird automatisch wiederhergestellt.",
  "connection.tooltip.connecting": "Live-Verbindung wird hergestellt…",
  "connection.tooltip.daemon_stopping":
    "Der Daemon hat sein Herunterfahren angekündigt. Das ist kein Netzwerkproblem — die Live-Aktualisierung kehrt zurück, sobald er wieder läuft.",
  "connection.events": "Ereignisse",
  "connection.last": "zuletzt",
  "session.unsaved": "Ungespeicherte Änderungen",
  "session.idle":
    "Inaktiv seit einer Weile. Speichern in {time} fällig — sonst gehen Änderungen beim Neuladen verloren.",
  "session.dismiss": "Schließen",
  "app.menu": "Menü",
  "app.close_menu": "Menü schließen",
  "app.skip_to_content": "Zum Inhalt springen",
  "app.switch_language": "Sprache wechseln",
  "page.title.default": "OpenCCU-Loom",
  "page.title.alarm": "Alarmanlage — OpenCCU-Loom",
  "page.title.security": "Sicherheit & Sicherheitstechnik — OpenCCU-Loom",
  "page.title.devices": "Geräte — OpenCCU-Loom",
  "page.title.overview": "Übersicht — OpenCCU-Loom",
  "page.title.diagnostics": "Diagnose — OpenCCU-Loom",
  "page.title.energy": "Energie — OpenCCU-Loom",
  "page.title.fleet": "CCUs — OpenCCU-Loom",
  "page.title.groups": "Heizungsgruppen — OpenCCU-Loom",
  "page.title.links": "Direktverknüpfungen — OpenCCU-Loom",
  "page.title.schedules": "Zeitprogramme — OpenCCU-Loom",
  "page.title.diagrams": "Diagramme — OpenCCU-Loom",
  "diagrams.title": "Diagramme",
  "diagrams.subtitle": "Benannte Mehr-Serien-Messwertdiagramme",
  "diagrams.new": "Neues Diagramm",
  "diagrams.edit": "Diagramm bearbeiten",
  "diagrams.empty": "Noch keine Diagramme.",
  "diagrams.empty.description":
    "Lege ein Diagramm an, um mehrere Datenpunkte gemeinsam über die Zeit darzustellen.",
  "diagrams.saved": "Diagramm gespeichert.",
  "diagrams.deleted": "Diagramm gelöscht.",
  "diagrams.field.name": "Name",
  "diagrams.field.visibility": "Sichtbarkeit",
  "diagrams.field.series": "Serien",
  "diagrams.visibility.private": "Privat",
  "diagrams.visibility.shared": "Geteilt",
  "diagrams.series.label": "Bezeichnung (optional)",
  "diagrams.series.add": "Serie hinzufügen",
  "diagrams.series.remove": "Entfernen",
  "diagrams.picker.series": "Serie",
  "diagrams.picker.device": "Gerät",
  "diagrams.picker.search": "Gerät suchen…",
  "diagrams.picker.no_devices": "Keine Geräte",
  "diagrams.picker.channel": "Kanal",
  "diagrams.picker.channel_none": "Kanal wählen…",
  "diagrams.picker.value": "Wert",
  "diagrams.picker.param_none": "Wert wählen…",
  "diagrams.picker.label": "Bezeichnung (optional)",
  "diagrams.picker.channels_failed": "Kanäle konnten nicht geladen werden",
  "diagrams.picker.params_failed": "Werte konnten nicht geladen werden",
  "diagrams.delete.confirm_title": 'Diagramm "{name}" löschen?',
  "diagrams.error.name_required": "Ein Name ist erforderlich.",
  "diagrams.error.series_required":
    "Mindestens eine Serie mit Zentrale hinzufügen.",
  "diagrams.error.save": "Diagramm konnte nicht gespeichert werden.",
  "diagrams.error.delete": "Diagramm konnte nicht gelöscht werden.",
  "diagrams.chart.empty": "Keine aufgezeichneten Messwerte in diesem Zeitraum.",
  "diagrams.chart.aria": "Mehr-Serien-Messwertdiagramm",
  "diagrams.chart.history_off": "Verlauf aus",
  "diagrams.chart.series_error": "nicht verfügbar",
  "diagrams.chart.no_samples": "keine Werte",
  "diagrams.history_required": "Verlaufsaufzeichnung ist aus.",
  "diagrams.history_required.description":
    "Diagramme stellen aufgezeichnete Messwerte dar. Aktiviere die Verlaufsaufzeichnung in den Einstellungen, um sie zu nutzen.",
  "page.title.logs": "Protokoll — OpenCCU-Loom",
  "page.title.settings": "Einstellungen — OpenCCU-Loom",
  "page.title.about": "Info — OpenCCU-Loom",
  "profile.header": "Profil",
  "profile.detected": "aktives Profil erkannt",
  "profile.placeholder": "Profil auswählen",
  "profile.apply": "Übernehmen",
  "profile.preview_label": "Vorschau:",
  "profile.preview.matching": "passend",
  "profile.preview.will_change": "wird geändert",
  "profile.preview.conflict": "Konflikt",
  "profile.preview.hide": "Details ausblenden",
  "profile.preview.show": "Details anzeigen",
  "profile.col.parameter": "Parameter",
  "profile.col.current": "Aktuell",
  "profile.col.next": "Neu",
  "profile.col.status": "Status",
  "subset.active": "aktiv",
  "subset.placeholder": "Auswahl…",
  "parameter.help": "Hilfe",
  "parameter.profile_badge": "Profil",
  "parameter.last_value": "Letzter Wert",
  "parameter.modified": "geändert",
  "parameter.read_only": "nur lesen",
  "parameter.not_triggerable": "nicht auslösbar",
  "parameter.unknown_type": "Unbekannter Typ: {type}",
  "parameter.execute": "Ausführen",
  "parameter.custom": "Benutzerdefiniert",
  "parameter.determine": "Bestimmen",
  "parameter.determine.tooltip": "Aktuellen Wert vom Gerät bestimmen",
  "parameter.determine.done": "{name} vom Gerät bestimmt",
  "parameter.determine.failed": "Bestimmen fehlgeschlagen",
  "parameter.determine.unsupported":
    "Dieses Gerät unterstützt das Bestimmen dieses Parameters nicht",
  "parameter.threshold.upper": "oberer Grenzwert",
  "parameter.threshold.lower": "unterer Grenzwert",
  // --- Zeitpaar-Presets (channel/time-pairs.ts) — nur die
  // wortbasierten Presets brauchen einen Key; Presets mit Zahl+Einheit
  // ("100 ms", "1 s", …) sind sprachunabhängig identisch und tragen den
  // literalen String als Key, den `t()` unverändert als Fallback liefert.
  "parameter.time_preset.not_active": "Nicht aktiv",
  "parameter.time_preset.1_second": "1 Sekunde",
  "parameter.time_preset.2_seconds": "2 Sekunden",
  "parameter.time_preset.3_seconds": "3 Sekunden",
  "parameter.time_preset.30_seconds": "30 Sekunden",
  "parameter.time_preset.1_minute": "1 Minute",
  "parameter.time_preset.2_minutes": "2 Minuten",
  "parameter.time_preset.4_minutes": "4 Minuten",
  "parameter.time_preset.15_minutes": "15 Minuten",
  // --- App-Chrome / Sidebar ---
  "common.ok": "OK",
  "app.theme.toggle": "Theme wechseln",
  "sidebar.cluster.overview": "Übersicht",
  "sidebar.cluster.automation": "Automatisierung",
  "sidebar.cluster.diagnose": "Status & Diagnose",
  "sidebar.cluster.system": "System",
  "sidebar.install_mode_active": "Anlernmodus aktiv",
  "sidebar.pending_messages": "{count} offene Meldung(en)",
  "diagnostics.all_ccus": "alle",
  // --- DeviceList ---
  "device.list.select_aria": "Gerät auswählen",
  "device.list.reachable": "Erreichbar",
  "device.list.unreachable": "Nicht erreichbar",
  "device.list.firmware_available": "Firmware-Update verfügbar",
  "device.list.channels": "Kanäle",
  // --- DeviceDetail Top-/Sub-Tabs ---
  "device.toptab.overview": "Übersicht",
  "device.toptab.configure": "Konfigurieren",
  "device.toptab.history": "Verlauf",
  "device.subtab.device_config": "Gerätekonfiguration",
  "device.subtab.maintenance_config": "Wartungskonfiguration",
  "device.subtab.channels": "Kanäle",
  "device.subtab.links": "Verknüpfungen",
  "device.subtab.schedule": "Zeitplan",
  "device.virtual": "Virtuell",
  "device.no_device_config":
    "Dieses Gerät hat keine Geräte-Konfigurationsebene.",
  "device.week_profile_channel.title": "Zeitplan-Kanal",
  "device.week_profile_channel.body":
    "Dieser Kanal hält nur den Geräte-Zeitplan. Öffne den Zeitplan-Editor zum Bearbeiten.",
  "device.confirm_remove_title": "Gerät entfernen?",
  "device.confirm_remove_body":
    'Gerät "{name}" entfernen? Die CCU-Kopplung wird aufgehoben und kann nicht rückgängig gemacht werden.',
  "device.delete.mode_label": "Art der Entfernung",
  "device.delete.mode_unpair": "Nur abmelden",
  "device.delete.mode_unpair_hint":
    "Die CCU-Kopplung wird aufgehoben (Standard).",
  "device.delete.mode_reset": "Ab Werk zurücksetzen",
  "device.delete.mode_reset_hint":
    "Das Gerät wird beim Entfernen zusätzlich auf Werkseinstellungen zurückgesetzt.",
  "device.delete.force": "Löschen erzwingen (Gerät unerreichbar)",
  "device.delete.force_hint":
    "Das Gerät auch dann entfernen, wenn es nicht mehr auf die CCU reagiert.",
  "device.delete.checking": "Abhängigkeiten werden geprüft…",
  "device.delete.warning_title": "Dieses Gerät wird noch verwendet",
  "device.delete.warning_links":
    "{count} Direktverknüpfung(en) verweisen auf dieses Gerät und funktionieren danach nicht mehr.",
  "device.delete.warning_programs":
    "{count} Programm(e) verweisen auf dieses Gerät.",
  "device.confirm_firmware_body":
    'Firmware-Update für "{name}" starten? Das Gerät ist während des Updates kurz nicht erreichbar.',
  "device.restore_config": "Konfiguration wiederherstellen",
  "device.restore_config.tooltip":
    "Die gespeicherte Konfiguration erneut an das Gerät übertragen (nach einem Werksreset)",
  "device.confirm_restore_config_body":
    'Die gespeicherte Konfiguration (alle Kanaleinstellungen und Direktverknüpfungen) erneut an "{name}" übertragen? Nach einem Werksreset verwenden — die Übertragung läuft über Funk und kann etwas dauern.',
  "device.restore_config_triggered": "Konfigurationsübertragung gestartet.",
  "device.communication_test": "Test",
  "device.communication_test.tooltip":
    "Ein Funk-Testtelegramm senden und prüfen, ob das Gerät antwortet",
  "device.communication_test_running": "Test läuft…",
  "device.communication_test_passed": "Kommunikation OK",
  "device.communication_test_failed": "Keine Antwort",
  "device.team.title": "Team",
  "device.team.reset": "Standard-Team",
  "device.team.changed": "Team-Zuordnung aktualisiert.",
  "device.team.none": "Keine weiteren Teams für diesen Gerätetyp.",
  "device.status.paramset_pick": "Quelle",
  // --- Wartungs-Grid ---
  "device.maintenance.title": "Wartung",
  "device.maintenance.reachable": "Erreichbar",
  "device.maintenance.rssi_device": "RSSI (Gerät)",
  "device.maintenance.rssi_peer": "RSSI (Peer)",
  "device.maintenance.low_bat": "Batterie schwach",
  "device.maintenance.battery": "Batterie",
  "device.maintenance.bat_low": "Schwach",
  "device.maintenance.status_ok": "OK",
  "device.maintenance.blocked": "Blockiert",
  "device.maintenance.operating_voltage": "Betriebsspannung",
  "device.maintenance.duty_cycle": "Duty-Cycle blockiert",
  "device.maintenance.duty_cycle_level": "Duty-Cycle",
  "device.maintenance.carrier_sense_level": "Carrier-Sense",
  "device.maintenance.config_pending": "Konfiguration ausstehend",
  "device.maintenance.update_pending": "Update ausstehend",
  "device.config_pending": "Geplant",
  // --- Friendly API errors ---
  "api.error.upstream_unavailable":
    "CCU vorübergehend nicht erreichbar. In wenigen Sekunden erneut versuchen.",
  "api.error.unauthorized": "Sitzung abgelaufen. Bitte erneut anmelden.",
  "auth.error.invalid_credentials": "Ungültige Anmeldedaten.",
  "api.error.forbidden": "Für diese Aktion fehlt die Berechtigung.",
  "api.error.not_found": "Ressource nicht gefunden.",
  "api.error.rate_limited": "Zu viele Anfragen — Geschwindigkeit gedrosselt.",
  "api.error.server": "Server-Fehler ({status}).",
  "api.error.request": "Anfrage abgelehnt ({status}).",
  "api.error.locked":
    "Gesperrt — dieser Kanal ist gegen Steuerbefehle gesperrt. Die Sperre lässt sich in den Kanal-Flags aufheben.",
  "api.error.locked_reason": "Gesperrt ({status}).",
  "api.error.edit_lock_lapsed":
    "Deine Editiersitzung ist abgelaufen — öffne den Parameter-Editor erneut, um eine neue Sperre zu erhalten.",
  // --- Matter-Bridge ---
  "nav.matter": "Matter",
  "sidebar.cluster.bridges": "Bridges",
  "matter.tab.expose": "Verfügbar machen",
  "matter.tab.fabrics": "Fabrics",
  "matter.tab.pair": "Koppeln",
  "matter.tab.diagnostics": "Diagnose",
  "matter.diag.title": "Bridge-Diagnose",
  "matter.diag.discovery": "Auffindbarkeit",
  "matter.diag.discovery_ok": "Die Bridge kündigt sich korrekt an — Controller können sie finden.",
  "matter.diag.not_advertising": "Die mDNS-Ankündigung ist abgeschaltet, kein Controller kann diese Bridge finden.",
  "matter.diag.port": "Port",
  "matter.diag.severity.error": "Blockierend",
  "matter.diag.severity.warning": "Warnung",
  "matter.diag.sessions": "Verbundene Controller",
  "matter.diag.sessions_hint": "Wie lange jeder Controller schon still ist. Ein Controller, der verschwindet ohne sich abzumelden, lässt seine Sitzung offen und sendet einfach nichts mehr.",
  "matter.diag.no_sessions": "Zurzeit ist kein Controller verbunden.",
  "matter.diag.sessions_occupancy": "Sitzungs-IDs: {live} aktiv · {reserved} für laufende Handshakes reserviert · {free} von {capacity} frei",
  "matter.diag.col_session": "Sitzung",
  "matter.diag.col_fabric": "Fabric",
  "matter.diag.col_peer_idle": "Controller still seit",
  "matter.diag.col_subscriptions": "Abonnements",
  "matter.diag.pase": "Kopplung",
  "matter.diag.no_subscriptions": "verbunden, empfängt aber nichts",
  "matter.diag.age_seconds": "{n}s",
  "matter.diag.age_minutes": "{n}min",
  "matter.diag.age_hours": "{n}h",
  "matter.diag.events": "Letzte Ereignisse",
  "matter.diag.events_hint": "Was passiert ist — im Unterschied zu dem, was gerade gilt. Nur im Speicher, geht beim Neustart verloren.",
  "matter.diag.events_empty": "Seit dem Start der Bridge nichts aufgezeichnet.",
  "matter.diag.kind_pairing": "Kopplung",
  "matter.diag.kind_session": "Sitzung",
  "matter.diag.kind_discovery": "Discovery",
  "matter.diag.compatibility": "Ökosystem-Kompatibilität",
  "matter.diag.compat_ok": "Keine bekannten Unverträglichkeiten für die gekoppelten Ökosysteme.",
  "matter.diag.ecosystem.apple": "Apple",
  "matter.diag.ecosystem.google": "Google",
  "matter.diag.ecosystem.amazon": "Alexa",
  "matter.diag.ecosystem.smartthings": "SmartThings",
  "matter.diag.ecosystem.aqara": "Aqara",
  "matter.diag.ecosystem.home_assistant": "Home Assistant",
  "matter.diag.ecosystem.unknown": "Unbekannter Controller",
  "matter.diag.endpoints": "Endpunkte",
  "matter.diag.endpoints_hint": "Was ein Controller sieht. Die Endpunkt-Nummern stammen aus gespeicherter Identität — sie bleiben über Neustarts gleich, lassen sich aber nicht aus der Geräteliste ableiten.",
  "matter.diag.no_endpoints": "Es ist noch kein Gerät für Matter freigegeben.",
  "matter.diag.reachable": "Erreichbar",
  "matter.diag.unreachable": "Nicht erreichbar",
  "matter.status.enabled": "Matter-Bridge aktiv",
  "matter.status.disabled":
    "Matter-Bridge ist nicht aktiviert. Setze matter.enabled = true in config.yaml.",
  "matter.status.listening": "empfangsbereit",
  "matter.status.not_listening": "nicht empfangsbereit",
  "matter.status.endpoints": "{count} Endpunkte",
  "matter.status.fabrics": "{count} Fabrics",
  "matter.status.advertising": "wird beworben",
  "matter.expose.empty": "Keine exponierbaren Datenpunkte gefunden.",
  "matter.expose.filter_kind": "Art",
  "matter.expose.filter_class": "Klasse",
  "matter.expose.filter_class_all": "Alle Klassen",
  "matter.expose.filter_class_unmapped": "(ohne Klasse)",
  "matter.expose.select_all": "Alle auswählen",
  "matter.expose.search_placeholder": "Name, Adresse, Klasse suchen…",
  "matter.expose.col_channel": "Kanal",
  "matter.expose.col_parameter": "Parameter",
  "matter.expose.kind.custom": "Custom",
  "matter.expose.kind.generic": "Generisch",
  "matter.expose.kind.calculated": "Berechnet",
  "matter.expose.kind.combined": "Kombiniert",
  "matter.expose.kind.measurement": "Messung",
  "matter.expose.unmappable_hint": "Kein passender Matter-Endpunkt verfügbar.",
  "matter.expose.partially_mappable_hint":
    "Teilweise abbildbar — bestimmte Cluster bleiben MQTT-only.",
  "matter.expose.conflict_hint":
    "Bereits über einen anderen Datenpunkt auf diesem Kanal exponiert.",
  "matter.expose.conflict_hint_custom_active":
    "Bereits über Custom DP `{profile}` exponiert — die Generic-DP zusätzlich zu bridgen kann zu doppelten Matter-Entities führen.",
  "matter.expose.conflict_hint_generic_active":
    "Kanal hat zusätzlich Generic DPs exponiert — Apple Home kann doppelte Entities anzeigen.",
  "matter.expose.bulk_expose": "Auswahl verfügbar machen",
  "matter.expose.bulk_hide": "Auswahl verbergen",
  "matter.expose.save": "Änderungen speichern",
  "matter.expose.discard": "Verwerfen",
  "matter.expose.saved_toast": "{count} Änderung(en) übernommen.",
  "matter.expose.legend": "Legende",
  "matter.expose.group_count": "{count} Datenpunkte",
  "matter.expose.state_exposed": "Verfügbar gemacht",
  "matter.expose.state_partial": "Teilweise abbildbar",
  "matter.expose.state_available": "Verfügbar (nicht exponiert)",
  "matter.expose.state_unmappable": "Nicht abbildbar",
  "matter.expose.unmappable_checkbox_title":
    "Nicht als Matter-Endpunkt abbildbar",
  "matter.pair.already_paired": "Controller mit Zugriff auf diese Bridge: {count}. Ein weiteres Fenster fügt einen zusätzlichen hinzu; die bestehenden Kopplungen bleiben unberührt.",
  "matter.pair.add_controller": "Weiteren Controller hinzufügen",
  "matter.pair.copy_manual_code": "Manuellen Koppelcode kopieren",
  "matter.pair.copy_qr_payload": "QR-Nutzlast kopieren",
  "matter.pair.copied": "In die Zwischenablage kopiert.",
  "matter.pair.copy_failed": "Der Browser hat den Zugriff auf die Zwischenablage verweigert — Code bitte von Hand übertragen.",
  "matter.pair.window_open": "Koppelfenster offen",
  "matter.pair.window_open_duration": "Koppelfenster öffnen",
  "matter.pair.qr_caption": "QR-Code mit Matter-Controller-App scannen",
  "matter.pair.manual_code": "Manueller Code",
  "matter.pair.success": "Controller erfolgreich gekoppelt.",
  "matter.pair.close_window": "Koppelfenster schließen",
  "matter.pair.loading": "Koppelstatus wird geladen…",
  "matter.pair.load_error": "Koppelstatus konnte nicht geladen werden.",
  "matter.pair.minutes": "Min",
  "matter.commissioning.closed": "Kopplungsfenster vom Betreiber geschlossen",
  "matter.maint.title": "Wartung",
  "matter.maint.force_sync": "Topologie neu aufbauen",
  "matter.maint.force_sync_hint": "Baut die veröffentlichten Endpoints aus den aktuellen Geräten neu auf. Kopplungen bleiben bestehen.",
  "matter.maint.force_sync_done": "Topologie neu aufgebaut.",
  "matter.maint.reset": "Alle Kopplungen entfernen",
  "matter.maint.reset_hint": "Versetzt die Bridge in den ungekoppelten Zustand. Jeder Controller muss sie neu hinzufügen.",
  "matter.maint.reset_confirm": "Alle Kopplungen entfernen?",
  "matter.maint.reset_confirm_body": "Jeder gekoppelte Controller verliert diese Bridge und muss sie neu hinzufügen. Das lässt sich nicht rückgängig machen.",
  "matter.maint.reset_confirm_label": "Alle entfernen",
  "matter.maint.reset_done": "Alle Kopplungen entfernt.",
  "matter.fabric.unpair_confirm": "Dieses Fabric entfernen?",
  "matter.fabric.unpaired": "Fabric entfernt.",
  "matter.fabric.share_bridge_hint": "Einen weiteren Controller hinzufügen öffnet ein Koppelfenster mit QR-Code, Countdown und der Möglichkeit, es wieder zu schließen — all das liegt im Koppel-Tab.",
  "matter.fabric.share_bridge_go": "Zum Koppel-Tab",
  "matter.fabric.share_bridge": "Bridge mit weiterem Controller teilen",
  "matter.fabric.label_unknown": "(kein Label)",
  "sensor_actor.toggle_failed": "{name} konnte nicht umgeschaltet werden",
  "sensor_actor.action_failed": "Aktion {name} fehlgeschlagen",
  "sensor_actor.numeric_invalid": "Ungültiger Wert für {name}",
  "sensor_actor.numeric_invalid_detail": "Bitte erst eine Zahl eingeben.",
  "sensor_actor.send": "Senden",
  "sensor_actor.cancel": "Abbrechen",
  "sensor_actor.no_primary":
    "Noch kein Hauptwert vorhanden — warte auf erstes CCU-Update.",
  "sensor_actor.loading": "Lädt {address}…",
  "sensor_actor.load_failed": "Konnte Kanal {address} nicht laden.",
  "sensor_actor.event_last": "zuletzt {age}",
  "sensor_actor.event_idle": "noch nicht ausgelöst",
  "sensor_actor.age_sec": "vor {n} s",
  "sensor_actor.age_min": "vor {n} min",
  "sensor_actor.age_hour": "vor {n} h",
  "sensor_actor.age_day": "vor {n} d",
  // --- Log viewer ---
  "nav.logs": "Protokoll",
  "logs.title": "Protokoll-Viewer",
  "logs.subtitle": "Strukturierte Daemon-Protokolle mit Live-Streaming.",
  "logs.default_level": "Standard-Level",
  "logs.view.aggregated": "Aggregiert",
  "logs.view.detail": "Detail",
  "logs.filter_placeholder": "Nach Meldung, Komponente filtern…",
  "logs.live": "Live",
  "logs.paused": "Pausiert",
  "logs.to_live": "▼ {count} neu · Zu Live",
  "logs.download": "Herunterladen",
  "logs.download_last": "Letzte {count}",
  "logs.empty": "Noch keine Protokolleinträge vorhanden.",
  "logs.forbidden": "Admin-Zugriff erforderlich, um Protokolle anzuzeigen.",
  "logs.repeated": "×{count}",
  "logs.connection.live": "verbunden",
  "logs.connection.reconnecting": "verbinde erneut…",
  "logs.level_saved": "Standard-Level gespeichert.",
  "app.not_found": "Nicht gefunden",
  "app.unknown_path": "Unbekannter Pfad",
  "app.route_load_failed":
    "Diese Ansicht konnte nicht geladen werden. Vermutlich wurde die Anwendung zwischenzeitlich aktualisiert — lade die Seite neu.",
  "audit.filter.global": "— (global)",
  "blind.label.position": "Position",
  "blind.label.slats": "Lamellen",
  "blind.pct_open": "geöffnet",
  "cdp.climate.absence": "Abwesenheit",
  "cdp.climate.absence_active": "Abwesenheit · aktiv",
  "cdp.climate.activate": "Aktivieren",
  "cdp.climate.actual_temp": "Ist-Temperatur",
  "cdp.climate.away_24h": "24 h abwesend",
  "cdp.climate.away_duration": "Dauer (h)",
  "cdp.climate.away_temperature": "Temperatur (°C)",
  "cdp.climate.boost": "Boost",
  "cdp.climate.frost": "Frostschutz",
  "cdp.climate.heat_off": "Aus",
  "cdp.climate.heat_on": "An",
  "cdp.climate.humidity": "Luftfeuchte",
  "cdp.climate.mode_auto": "Auto",
  "cdp.climate.mode_away": "Abwesend",
  "cdp.climate.mode_boost": "Boost",
  "cdp.climate.mode_manual": "Manuell",
  "cdp.climate.present": "Anwesend",
  "cdp.climate.profile": "Profil",
  "cdp.climate.secondary_off": "Aus",
  "cdp.climate.secondary_on": "An",
  "cdp.climate.week_program": "Wochenprogramm {n}",
  "cdp.cover.close": "Schließen",
  "cdp.cover.open": "Öffnen",
  "cdp.cover.position": "Position",
  "cdp.cover.secondary_open": "{pct} % geöffnet",
  "cdp.cover.secondary_slats": "Lamellen {pct} %",
  "cdp.cover.slats": "Lamellen",
  "cdp.cover.state_closed": "Geschlossen",
  "cdp.cover.state_open": "Offen",
  "cdp.cover.state_unknown": "Unbekannt",
  "cdp.cover.state_ventilating": "Lüftet",
  // Generische ENUM-Wert-Tokens (Sensor/Aktor-Anzeige). Lookup als
  // `enum.<TOKEN>`; unbekannte Tokens fallen auf Title-Case zurück.
  "enum.CLOSED": "Geschlossen",
  "enum.OPEN": "Offen",
  "enum.TILTED": "Gekippt",
  "enum.UNKNOWN": "Unbekannt",
  "enum.STABLE": "Stabil",
  "enum.FALLING": "Fallend",
  "enum.RISING": "Steigend",
  "enum.UP": "Auf",
  "enum.DOWN": "Ab",
  "enum.NONE": "Keine",
  "enum.ON": "An",
  "enum.OFF": "Aus",
  "enum.DRY": "Trocken",
  "enum.WET": "Nass",
  // Generische Datenpunkt-Labels (Sensor/Aktor-Kacheln). Lookup als
  // `datapoint.<NAME>`; nur kanal-agnostische Namen.
  "datapoint.STATE": "Status",
  "datapoint.LEVEL": "Pegel",
  "datapoint.DIRECTION": "Richtung",
  "datapoint.ERROR": "Fehler",
  "datapoint.WORKING": "Aktiv",
  "cdp.cover.stop": "Halt",
  "cdp.cover.ventilate": "Lüften",
  "cdp.light.brightness": "Helligkeit",
  "cdp.light.color": "Farbe",
  "cdp.light.color_temp": "Farbtemperatur",
  "cdp.light.effect": "Effekt",
  "cdp.light.hue": "Farbton",
  "cdp.light.saturation": "Sättigung",
  "cdp.light.white": "Weiß",
  "cdp.panel.general": "Allgemein",
  "cdp.panel.group": "Gruppe {n}",
  "cdp.panel.loading": "Lädt {addr}/cdps · seit {n}s…",
  "cdp.panel.no_controls": "Keine Bedienelemente für dieses Gerät.",
  "cdp.panel.server_unresponsive":
    "Server antwortet nicht. Prüfe ob der Daemon läuft (Browser-Network-Tab: <code>/api/v1/devices/{addr}/cdps</code>).",
  "cdp.retry": "Erneut versuchen",
  "cdp.siren.acoustic": "Akustik",
  "cdp.siren.duration": "Dauer",
  "cdp.siren.off": "Aus",
  "cdp.siren.optical": "Optik",
  "cdp.siren.state_acoustic": "Akustisch",
  "cdp.siren.state_active": "Alarm aktiv",
  "cdp.siren.state_optical": "Optisch",
  "cdp.siren.state_quiet": "Ruhe",
  "cdp.siren.test": "Test",
  "cdp.siren.volume": "Lautstärke",
  "cdp.switch.on_for": "An für…",
  "cdp.group_n": "Gruppe {n}",
  "cdp.tile.no_state": "Noch kein Zustand empfangen",
  "cdp.status.age": " · zuletzt vor {ago}",
  "cdp.status.from_cache": "Aus Cache wiederhergestellt{age}",
  "cdp.status.no_datapoints": "Keine Datenpunkte beobachtet.",
  "cdp.status.stale": "Verbindung verloren{age}",
  "cdp.textdisplay.advanced": "Erweitert",
  "cdp.textdisplay.color_label": "Farbe",
  "cdp.textdisplay.color_placeholder": "z. B. WHITE",
  "cdp.textdisplay.icon_label": "Icon",
  "cdp.textdisplay.icon_placeholder": "z.B. 0",
  "cdp.textdisplay.less": "Weniger",
  "cdp.textdisplay.row": "Zeile {row}",
  "cdp.textdisplay.row_label": "Zeile",
  "cdp.textdisplay.sending": "Sendet…",
  "cdp.textdisplay.text_placeholder": "Text…",
  "cdp.textdisplay.write": "Schreiben",
  "cdp.valve.close": "Zu",
  "cdp.valve.open": "Auf",
  "cdp.valve.open_for": "Öffnen für…",
  "cdp.valve.opening": "Öffnung",
  "cdp.valve.secondary_open": "{pct} % offen",
  "cdp.valve.state_closed": "Geschlossen",
  "cdp.valve.state_open": "Geöffnet",
  "climate.mode.auto": "Auto",
  "climate.mode.away": "Abwesend",
  "climate.mode.boost": "Boost",
  "climate.mode.manual": "Manuell",
  "climate.preset.boost": "Boost",
  "climate.preset.comfort": "Komfort",
  "climate.preset.frost": "Frostschutz",
  "climate.preset.lowering": "Absenken",
  "climate.stat.current_temp": "Ist-Temperatur",
  "climate.stat.heat_cool": "Heiz./Kühl.",
  "climate.stat.humidity": "Luftfeuchte",
  "climate.stat.valve": "Ventil",
  "climate.stat.window": "Fenster",
  "common.all_ccus": "Alle CCUs",
  "common.max": "Max",
  "common.min": "Min",
  "common.select_placeholder": "— wählen —",
  "control.active": "Aktiv",
  "control.alarm_active": "Alarm aktiv",
  "control.brightness": "Helligkeit",
  "control.color": "Farbe",
  "control.color_temp": "Farbtemperatur",
  "control.current": "Strom",
  "control.effect": "Effekt",
  "control.energy": "Energie",
  "control.frequency": "Frequenz",
  "control.hue": "Farbton",
  "control.idle": "Ruhe",
  "control.locked": "Verriegelt",
  "control.number.decrement": "Verringern",
  "control.number.increment": "Erhöhen",
  "control.power": "Leistung",
  "control.status_unknown": "Status unbekannt",
  "control.test": "Test",
  "control.unlocked": "Entriegelt",
  "control.voltage": "Spannung",
  "cover.close": "Schließen",
  "cover.open": "Öffnen",
  "cover.stop": "Stopp",
  "device.aria.configure_sub_tabs": "Konfigurations-Unter-Tabs",
  "device.aria.top_tabs": "Haupt-Tabs",
  "devicelist.all": "Alle",
  "devicelist.all_areas": "Alle Bereiche",
  "devicelist.all_rooms": "Alle Räume",
  "devicelist.apply": "Übernehmen",
  "devicelist.availability": "Verfügbarkeit",
  "devicelist.available": "Verfügbar",
  "devicelist.bulk_firmware_body":
    "Firmware-Update für {count} Gerät(e) anstoßen?",
  "devicelist.bulk_firmware_confirm": "Update starten",
  "devicelist.bulk_firmware_label": "Firmware-Update",
  "devicelist.bulk_no_updates":
    "Keine selektierten Geräte haben ein Firmware-Update verfügbar.",
  "devicelist.bulk_result": "{ok} OK, {fail} fehlgeschlagen.",
  "devicelist.ccu_refresh": "Von CCU neu einlesen",
  "devicelist.ccu_refresh_title":
    "Geräteliste und Namen neu von der CCU einlesen",
  "devicelist.clear_selection": "Auswahl leeren",
  "devicelist.col.address": "Adresse",
  "devicelist.col.model": "Modell",
  "devicelist.col.name": "Name",
  "devicelist.col.rooms": "Räume",
  "devicelist.col.status": "Status",
  "devicelist.count": "{filtered} / {total} Geräte",
  "devicelist.group_by_interface": "Nach Interface gruppieren",
  "devicelist.last_updated": "Zuletzt aktualisiert {time}",
  "devicelist.load_error": "Fehler beim Laden: {error}",
  "devicelist.area": "Bereich",
  "devicelist.room": "Raum",
  "devicelist.room_aria": "Raum für Auswahl",
  "devicelist.room_placeholder": "Raum (leer = entfernen)",
  "devicelist.search_placeholder": "Suche (Adresse, Name, Modell)",
  "devicelist.select_filtered": "Alle filtern",
  "devicelist.selected": "{count} Gerät(e) ausgewählt",
  "devicelist.set_room": "Raum setzen",
  "devicelist.unavailable": "Nicht verfügbar",
  "devicelist.update_available": "Update verfügbar",
  "devicelist.view_mode": "Ansicht",
  "devicelist.view_grid": "Rasteransicht",
  "devicelist.view_list": "Tabellenansicht",
  "garage.cmd.close": "Schließen",
  "garage.cmd.open": "Öffnen",
  "garage.cmd.stop": "Halt",
  "garage.cmd.vent": "Lüften",
  "garage.state.closed": "Geschlossen",
  "garage.state.open": "Offen",
  "garage.state.unknown": "Unbekannt",
  "garage.state.ventilating": "Lüftet",
  "inbox.install_mode": "Anlernmodus",
  "inbox.install_mode_active_title":
    "Anlernmodus aktiv — klicken um zu beenden",
  "inbox.install_mode_badge": "aktiv",
  "inbox.install_mode_pairing": "Anlernen · {seconds} s",
  "inbox.install_mode_running": "Anlernmodus läuft",
  "inbox.install_mode_seconds_left": "Sekunden verbleibend",
  "inbox.install_mode_start_title":
    "Anlernmodus starten (60 s) um neue Geräte zu koppeln",
  "inbox.install_mode_select_interface":
    "Bitte eine Schnittstelle zum Anlernen wählen.",
  "inbox.install_mode_banner_iface_on":
    "Anlernmodus aktiv auf {iface} ({seconds} s).",
  "inbox.install_mode_banner_iface_off": "Anlernmodus beendet auf {iface}.",
  "inbox.pair_serial_label": "Per Seriennummer anlernen:",
  "inbox.pair_serial_placeholder": "Geräteadresse / Seriennummer",
  "inbox.pair_serial_submit": "Gerät anlernen",
  "inbox.install_mode_local_label":
    "HmIP-Gerät offline anlernen (SGTIN + Key):",
  "inbox.install_mode_local_sgtin_label": "SGTIN",
  "inbox.install_mode_local_sgtin_placeholder": "SGTIN, z. B. 3014-F711-A000-…",
  "inbox.install_mode_local_key_label": "Geräteschlüssel",
  "inbox.install_mode_local_key_placeholder": "Geräteschlüssel vom Etikett",
  "inbox.install_mode_local_submit": "Lokales Anlernen starten",
  "inbox.install_mode_local_started":
    "Lokales Anlernen gestartet — Anlerntaste am Gerät drücken.",
  "inbox.install_mode_local_hint":
    "Funktioniert ohne Internetzugang: Nur das Gerät mit passender SGTIN und Key kann sich anmelden.",
  "inbox.search_wired": "Draht-Bus durchsuchen",
  "inbox.search_wired_title":
    "Den BidCos-Wired-Bus nach neu angeschlossenen Geräten durchsuchen",
  "inbox.search_wired_hint":
    "Durchsucht den Draht-Bus; gefundene Geräte erscheinen im Posteingang.",
  "inbox.search_wired_running": "Suche läuft…",
  "inbox.search_wired_done": "{count} Gerät(e) gefunden — siehe Posteingang.",
  "inbox.replace.button": "Gerät tauschen",
  "inbox.replace.title": "Vorhandenes Gerät tauschen",
  "inbox.replace.intro":
    "Wähle das angelernte Gerät, das {address} ersetzt. Verknüpfungen, Teams und Programme wandern auf das neue Gerät; das alte Gerät wird abgelernt.",
  "inbox.replace.empty": "Keine tauschbaren Geräte",
  "inbox.replace.empty_description":
    "Die CCU hat kein kompatibles angelerntes Gerät gefunden, das dieses ersetzen kann.",
  "inbox.replace.same_type": "Gleicher Typ",
  "inbox.replace.compatible_type": "Kompatibel",
  "inbox.replace.confirm_title": "Gerät tauschen?",
  "inbox.replace.confirm_text":
    "„{new}“ ersetzt „{old}“. Das alte Gerät wird abgelernt und aus dem System entfernt. Das kann nicht rückgängig gemacht werden.",
  "inbox.replace.confirm_label": "Tauschen",
  "inbox.replace.success": "Gerät getauscht.",
  "inbox.pair_serial_started": "Anlernfenster für {addr} geöffnet.",
  "light.brightness": "Helligkeit",
  "light.color_temp": "Farbtemperatur",
  "light.effect": "Effekt",
  "light.hue": "Farbton",
  "light.mode.color": "Farbe",
  "light.mode.white": "Weiß",
  "light.saturation": "Sättigung",
  "lock.lock": "Verriegeln",
  "lock.locked": "Zu",
  "lock.open_door": "Tür öffnen",
  "lock.unlock": "Entriegeln",
  "lock.unlocked": "Auf",
  "login.password": "Passwort",
  "login.sso": "Single Sign-On (OIDC)",
  "login.ccu_hint": "Sie können sich mit Ihrem CCU-Konto anmelden.",
  "login.submit": "Anmelden",
  "login.submitting": "Anmelden…",
  "login.username": "Benutzername",
  "setup.step.progress": "Schritt {current} von {total}",
  "setup.step1.title": "Administratorkonto",
  "setup.step2.title": "Sprache & Darstellung",
  "setup.step3.title": "CCU verbinden",
  "setup.step4.title": "MQTT-Broker",
  "setup.username": "Benutzername",
  "setup.password": "Passwort",
  "setup.confirm": "Passwort bestätigen",
  "setup.password.too_short":
    "Das Passwort muss mindestens 8 Zeichen lang sein.",
  "setup.password.mismatch": "Die Passwörter stimmen nicht überein.",
  "setup.locale.label": "Sprache",
  "setup.theme.label": "Darstellung",
  "setup.theme.system": "System folgen",
  "setup.theme.light": "Hell",
  "setup.theme.dark": "Dunkel",
  "setup.ccu.enable": "Jetzt eine CCU verbinden",
  "setup.ccu.name": "Name",
  "setup.ccu.host": "Host",
  "setup.ccu.interfaces": "Schnittstellen",
  "setup.ccu.interfaces_hint": "Wählen Sie die Funkschnittstellen dieser CCU.",
  "setup.mqtt.enable": "MQTT aktivieren",
  "setup.mqtt.broker": "Broker-URL",
  "setup.back": "Zurück",
  "setup.next": "Weiter",
  "setup.finish": "Einrichtung abschließen",
  "setup.finishing": "Wird abgeschlossen…",
  "setup.done.title": "Einrichtung abgeschlossen",
  "setup.done.detail": "Melden Sie sich mit Ihrem neuen Administratorkonto an.",
  "setup.error.title": "Einrichtung fehlgeschlagen",
  "logs.groups": "Gruppen",
  "matter.expose.col_select": "Auswahl",
  "matter.expose.col_state": "Status",
  "matter.expose.drawer_aria": "Expositions-Detail",
  "matter.expose.drawer_clusters": "Cluster",
  "matter.expose.drawer_device_type": "Matter-Gerätetyp",
  "matter.expose.drawer_source": "Quelle",
  "matter.expose.drawer_state": "Status",
  "matter.expose.friendly_name": "Anzeigename",
  "matter.expose.select_row": "Zeile auswählen",
  "matter.fabrics.col_fabric": "Fabric #",
  "matter.fabrics.col_label": "Label",
  "matter.fabrics.col_node_id": "Node-ID",
  "matter.fabrics.col_vendor": "Hersteller",
  "matter.fabrics.empty": "Noch keine Fabrics gekoppelt.",
  "matter.fabrics.node_id_rounded": "gerundet",
  "matter.fabrics.node_id_rounded_hint":
    "Diese Node-ID überschreitet die Genauigkeit, die diese Liste überträgt — die letzten Stellen können von denen abweichen, die der Controller anzeigt.",
  "matter.pair.qr_payload": "QR-Payload",
  "select.placeholder": "Auswählen…",
  "sysvars.create.values_placeholder": "aus;an;blink",
  "ui.breadcrumb": "Brotkrümelnavigation",
  "ui.dismiss": "Schließen",
  "ui.events_since_connect": "Events seit Verbindungsaufbau",
  "diagnostics.recording_type.debug_log": "Debug-Log",
  "diagnostics.col.interface": "Interface",
  "diagnostics.col.type": "Typ",
  "diagnostics.col.status": "Status",
  "diagnostics.col.duty_cycle": "Duty-Cycle",
  "diagnostics.col.carrier_sense": "Carrier-Sense",
  "diagnostics.utilisation_unknown": "Für dieses Interface nicht gemeldet",
  "diagnostics.col.host": "Host / Zentrale",
  "diagnostics.col.action": "Aktion",
  "diagnostics.col.client": "Client",
  "diagnostics.col.score": "Score",
  "diagnostics.reliability.title": "Zuverlässigkeit",
  "diagnostics.reliability.help":
    "Circuit-Breaker- und Verbindungsstatus je (Zentrale, Interface)-Paar.",
  "diagnostics.reliability.col.central": "Zentrale",
  "diagnostics.reliability.col.interface": "Interface",
  "diagnostics.reliability.col.circuit": "Circuit",
  "diagnostics.reliability.col.state": "Status",
  "diagnostics.reliability.col.requests":
    "Requests (gesamt / ausgeführt / ausstehend)",
  "diagnostics.reliability.col.last_failure": "Letzter Fehler",
  "diagnostics.reliability.col.last_callback": "Letzter Callback",
  "diagnostics.reliability.circuit.closed": "Geschlossen",
  "diagnostics.reliability.circuit.open": "Offen",
  "diagnostics.reliability.circuit.half_open": "Halb offen",
  "diagnostics.reliability.empty": "Noch keine meldenden Interface-Clients.",
  "diagnostics.values_cache.title": "Werte-Cache",
  "diagnostics.values_cache.help":
    "Zeilenanzahl, Größe und kumulative Zähler des persistenten VALUES-Caches seit Prozessstart.",
  "diagnostics.values_cache.rows": "Zeilen",
  "diagnostics.values_cache.bytes": "Value-JSON-Bytes",
  "diagnostics.values_cache.restored": "Wiederhergestellt",
  "diagnostics.values_cache.cast_failures": "Cast-Fehler",
  "diagnostics.values_cache.gc_deleted": "GC-gelöscht",
  "diagnostics.values_cache.flush_batches": "Flush-Batches",
  "diagnostics.values_cache.flushed_entries": "Geflushte Einträge",
  "diagnostics.values_cache.reset": "Cache zurücksetzen",
  "diagnostics.values_cache.reset_confirm_title": "VALUES-Cache zurücksetzen?",
  "diagnostics.values_cache.reset_confirm_body":
    "Jeder zwischengespeicherte Wire-Wert wird gelöscht. Datenpunkte zeigen source=unobserved, bis Live-Events sie neu befüllen.",
  "diagnostics.values_cache.reset_success": "Werte-Cache zurückgesetzt.",
  "schedule.aria.weekdays": "Wochentage",
  "schedule.duration_placeholder": "z.B. 10s, 5min",
  "schedule.ramp_placeholder": "z.B. 500ms, 2s",
  "ccu_position.title": "Astro-Position",
  "ccu_position.latitude": "Breitengrad",
  "ccu_position.longitude": "Längengrad",
  "ccu_position.help":
    "Bezugspunkt für die Sonnenauf- und -untergangszeiten der CCU. Falsche Koordinaten verschieben jede Astro-Schaltzeit, ohne dass ein Fehler auftritt.",
  "ccu_position.unknown": "Position noch nicht bekannt.",
  "ccu_position.confirm_title": "Astro-Position ändern?",
  "ccu_position.confirm_body":
    "Das ändert die Sonnenauf- und -untergangszeiten, die {central} berechnet — für die eigenen Programme ebenso wie für die hier bearbeiteten Wochenprofile.",
  "ccu_position.saved": "Astro-Position von {central} gespeichert.",
  "ccu_host.poweroff.action": "Herunterfahren",
  "ccu_host.poweroff.confirm_title": "CCU herunterfahren?",
  "ccu_host.poweroff.confirm_body":
    "{central} wird ausgeschaltet. Aus der Ferne lässt sie sich nicht wieder starten — das geht nur am Gerät.",
  "ccu_host.poweroff.triggered": "Herunterfahren von {central} ausgelöst.",
  "ccu_host.safe_mode.action": "Abgesicherter Modus",
  "ccu_host.safe_mode.confirm_title": "In den abgesicherten Modus neu starten?",
  "ccu_host.safe_mode.confirm_body":
    "{central} startet ohne Logikschicht neu. Programme und Systemvariablen laufen erst wieder, wenn der abgesicherte Modus verlassen wird.",
  "ccu_host.safe_mode.triggered":
    "{central} startet in den abgesicherten Modus.",
  "ccu_host.recovery_mode.action": "Recovery",
  "ccu_host.recovery_mode.confirm_title": "In das Recovery-System neu starten?",
  "ccu_host.recovery_mode.confirm_body":
    "{central} startet in das Recovery-System und bleibt außer Betrieb, bis es dort wieder verlassen wird. Die Recovery-Oberfläche ist unter der Adresse der CCU erreichbar.",
  "ccu_host.recovery_mode.triggered":
    "{central} startet in das Recovery-System.",
  "ccu_maintenance.title": "CCU-Wartung",
  "ccu_maintenance.subtitle":
    "Aktionen auf Host-Ebene für jede verbundene CCU. Ein Neustart startet die CCU neu und unterbricht kurz ihre Verbindung.",
  "ccu_maintenance.empty": "Noch keine CCU konfiguriert.",
  "ccu_maintenance.online": "Verbunden",
  "ccu_maintenance.offline": "Getrennt",
  "ccu_maintenance.reboot": "CCU neu starten",
  "ccu_maintenance.rebooting": "Wird neu gestartet…",
  "ccu_maintenance.confirm_title": "CCU neu starten?",
  "ccu_maintenance.confirm_body":
    "{central} wird jetzt neu gestartet. Die Verbindung zu dieser CCU bricht ab, bis sie wieder online ist. Fortfahren?",
  "ccu_maintenance.triggered":
    "Neustart für {central} angestoßen — sie ist in Kürze wieder erreichbar.",
  "ccu_maintenance.admin_only":
    "Nur Administratoren können eine CCU neu starten.",
  "ccu_update.admin_only":
    "Nur Administratoren können CCU-Updates installieren.",
  "ccu_update.available": "Update verfügbar",
  "ccu_update.confirm_body":
    "{central} lädt und installiert sein Firmware-Update und startet neu — die Verbindung bricht kurz ab. Fortfahren?",
  "ccu_update.backup_first": "Vorher sichern",
  "ccu_update.backup_first.help":
    "Vor dem Update ein vollständiges CCU-Backup anlegen. Das Update startet erst, wenn das Backup gespeichert ist, und gar nicht, wenn es fehlschlägt. Das kann mehrere Minuten dauern.",
  "ccu_update.backing_up": "Sichere…",
  "ccu_update.confirm_body_with_backup":
    "Zuerst wird ein vollständiges Backup von {central} angelegt; das Update startet nur, wenn das gelingt. Das kann mehrere Minuten dauern — lass die Seite offen.",
  "ccu_update.confirm_title": "CCU-Update installieren?",
  "ccu_update.empty": "Noch keine CCU-Update-Informationen verfügbar.",
  "ccu_update.in_progress": "Wird installiert…",
  "ccu_update.install": "Update installieren",
  "ccu_update.installing": "Wird gestartet…",
  "ccu_update.not_observed": "Update-Status noch nicht abgerufen.",
  "ccu_update.subtitle":
    "Stößt das Firmware-Update der CCU an. Die CCU startet während der Installation neu.",
  "ccu_update.title": "CCU-System-Update",
  "ccu_update.triggered":
    "CCU-Update für {central} angestoßen — sie startet neu.",
  "firmware_download.title": "Firmware auf eine CCU laden",
  "firmware_download.subtitle":
    "Die CCU lädt ein Firmware-Abbild von der URL auf die Zentrale, damit es für die Installation bereitsteht.",
  "firmware_download.url_label": "URL des Firmware-Abbilds",
  "firmware_download.url_placeholder": "https://…",
  "firmware_download.download": "Herunterladen",
  "firmware_download.downloading": "Wird geladen…",
  "firmware_download.triggered": "Firmware-Download angestoßen.",
  "addon_update.title": "Add-on-Update",
  "addon_update.subtitle":
    "Nach Updates für das CCU-Add-on suchen und sie installieren. Der Daemon startet während der Installation neu.",
  "addon_update.check": "Nach Updates suchen",
  "addon_update.checking": "Suche läuft…",
  "addon_update.available": "Update verfügbar",
  "addon_update.up_to_date": "Aktuell",
  "addon_update.install": "Update installieren",
  "addon_update.install_starting": "Wird gestartet…",
  "addon_update.installing_notice":
    "Update wird installiert — der Daemon startet neu. Diese Seite verbindet sich automatisch neu, sobald er wieder da ist.",
  "addon_update.confirm_title": "Add-on-Update installieren?",
  "addon_update.confirm_body":
    "Der Daemon startet zum Abschluss der Installation neu — die Verbindung bricht kurz ab und verbindet sich von selbst neu. Fortfahren?",
  "addon_update.release_notes": "Release-Notes",
  "addon_update.never_checked": "Noch nie geprüft",
  "addon_update.field.current_version": "Installierte Version",
  "addon_update.field.latest_version": "Neueste Version",
  "addon_update.field.last_check": "Zuletzt geprüft",
  "addon_update.toast.check_failed": "Update-Suche fehlgeschlagen",
  "addon_update.toast.install_trigger_failed":
    "Update konnte nicht gestartet werden",
  "addon_update.toast.failed": "Add-on-Update fehlgeschlagen",
  "addon_update.toast.installed": "Add-on auf {version} aktualisiert",
  // --- Spaltenbezeichnungen für migrierte DataTable-Views ---
  "messages.col.name": "Meldung",
  "messages.col.device": "Gerät",
  "messages.col.time": "Zeit",
  "messages.col.last_timestamp": "Zuletzt geändert",
  "messages.col.type": "Typ",
  "messages.col.actions": "Aktionen",
  "inbox.col.address": "Adresse",
  "inbox.col.model": "Modell",
  "inbox.col.serial": "Seriennr.",
  "inbox.col.first_seen": "Erstmals gesehen",
  "inbox.col.actions": "Aktionen",
  "audit.col.time": "Zeit",
  "audit.col.action": "Aktion",
  "audit.col.user": "Benutzer",
  "audit.col.target": "Ziel",
  "audit.col.changes": "Änderungen",
  // --- CCU-Flotte (schreibgeschützte CCU-übergreifende Übersicht) ---
  "fleet.title": "CCUs",
  "fleet.subtitle":
    "Alle konfigurierten CCUs mit Status, Schnittstellen und Geräteanzahl auf einen Blick.",
  "fleet.empty": "Noch keine CCUs konfiguriert.",
  "fleet.empty.description":
    "Registriere eine CCU in den Einstellungen, um Verbindung und Geräte hier zu überwachen.",
  "fleet.load_error": "CCU-Flotte konnte nicht geladen werden: {error}",
  "fleet.status.online": "Online",
  "fleet.status.offline": "Offline",
  "fleet.field.host": "Host",
  "fleet.field.model": "Modell",
  "fleet.field.version": "Firmware-Version",
  "fleet.field.serial": "Seriennummer",
  "fleet.field.devices": "Geräte",
  "fleet.field.interfaces": "Schnittstellen",
  "fleet.field.ccu_interfaces": "Von der CCU gemeldete Schnittstellen",
  "fleet.field.ccu_interfaces.unmanaged":
    "Die CCU bietet diese Schnittstelle an, dieser Daemon ist dafür aber nicht konfiguriert.",
  "fleet.field.ccu_security": "CCU-Sicherheit",
  "fleet.field.auth_enabled.on": "Authentifizierung erforderlich",
  "fleet.field.auth_enabled.off": "Keine Authentifizierung",
  "fleet.field.auth_enabled.hint":
    "Ob die CCU selbst eine Authentifizierung verlangt. Wird auch als „keine Authentifizierung“ angezeigt, wenn die CCU-Firmware die Abfrage nicht beantwortet.",
  "fleet.field.https_redirect.on": "HTTPS-Weiterleitung an",
  "fleet.field.https_redirect.off": "HTTPS-Weiterleitung aus",
  "fleet.field.https_redirect.hint":
    "Ob die CCU einfaches HTTP auf HTTPS weiterleitet. Wird auch als „aus“ angezeigt, wenn die CCU-Firmware die Abfrage nicht beantwortet.",
  "fleet.open_webui": "CCU-WebUI öffnen",
  // --- Heizungsgruppen (schreibgeschützt, GR01) ---
  "groups.title": "Heizungsgruppen",
  "groups.count": "{count} Gruppen",
  "groups.empty": "Noch keine Heizungsgruppen konfiguriert.",
  "groups.empty.description":
    "Heizungsgruppen werden auf der CCU selbst angelegt und bearbeitet; diese Ansicht spiegelt nur den aktuellen Bestand.",
  "groups.field.id": "ID",
  "groups.type": "Typ",
  "groups.new": "Neue Gruppe",
  "groups.select_ccu_first": "Zuerst eine CCU auswählen.",
  "groups.delete.title": "Gruppe löschen?",
  "groups.delete.body":
    "Die Heizungsgruppe „{name}“ löschen? Die Mitglieder-Verdrahtung auf der CCU wird entfernt. Das kann nicht rückgängig gemacht werden.",
  "groups.delete.done": "Gruppe gelöscht.",
  "groups.editor.create_title": "Neue Heizungsgruppe",
  "groups.editor.edit_title": "Heizungsgruppe bearbeiten",
  "groups.editor.created": "Gruppe angelegt.",
  "groups.editor.updated": "Gruppe aktualisiert.",
  "groups.editor.name": "Name",
  "groups.editor.members": "Mitglieder",
  "groups.editor.no_members": "Keine zuweisbaren Geräte für diesen Typ.",
  "groups.editor.no_types": "Keine Gruppentypen verfügbar.",
  "groups.editor.search_placeholder":
    "Nach Name, Raum, Typ oder Seriennummer suchen…",
  "groups.editor.selection_summary": "{channels} Kanäle · {devices} Geräte",
  "groups.editor.only_selected": "Nur ausgewählte",
  "groups.editor.select_visible": "Sichtbare wählen",
  "groups.editor.no_matches": "Keine Treffer — Suche oder Filter anpassen.",
  "groups.editor.selected": "Ausgewählt",
  "groups.editor.clear_all": "Alle abwählen",
  "groups.editor.no_selection":
    "Noch nichts ausgewählt — tippe auf ein Gerät oder einen Kanal.",
  "groups.editor.channel_fallback": "Kanal {no}",
  "groups.editor.not_selectable": "nicht auswählbar",
  "groups.editor.config_pending": "Konfig. ausstehend",
  "groups.field.group_device_name": "Virtuelles Gerät",
  "groups.operate_only_via_group": "Nur Gruppenbedienung",
  "groups.operate_only_via_group.help":
    "Wenn aktiv, können die Geräte der Gruppe nur gemeinsam über die Gruppe bedient werden, nicht einzeln.",
  "groups.members": "{count} Mitglieder",
  "groups.members.empty": "Keine Mitglieder",
  // --- Areas (Bereiche — Raumgruppen oberhalb der CCU-Räume) ---
  "areas.title": "Bereiche",
  "areas.hint":
    "Fasse CCU-Räume zu einem größeren Bereich zusammen — eine Etage, einen Schuppen, eine Terrassenüberdachung. Nicht zu verwechseln mit Alarmzonen.",
  "areas.empty": "Keine Bereiche konfiguriert.",
  "areas.col.rooms_count": "Räume",
  "areas.placeholder": "Bereichsname…",
  "areas.assign_rooms": "Räume zuweisen",
  "areas.delete_confirm": "Bereich entfernen?",
  "areas.delete_confirm.body":
    "Die Raumzuordnungen werden aufgehoben; die Räume selbst bleiben unverändert.",
  "areas.rooms_dialog.title": "Räume zuweisen — {name}",
  "areas.rooms_dialog.hint":
    "Ein Häkchen verschiebt den Raum aus seinem aktuellen Bereich hierher — ein Raum kann immer nur einem Bereich angehören.",
  "areas.rooms_dialog.search_placeholder": "Räume suchen…",
  "areas.rooms_dialog.empty":
    "Noch keine Räume bekannt — weise zunächst einem Gerät einen Raum zu.",
  "areas.rooms_dialog.current_area": "aktuell: {name}",
  "areas.toast.rooms_saved": "Räume zugewiesen.",
  // --- Sicherheit & Sicherheitstechnik (notes/concepts/security-safety-concept.md
  //     §7.8). Klassifikator-gesteuerte Gefahren-/Störungsklassen, ein
  //     Störungs-Ledger und das klassifizierte Datenpunkt-Inventar. Läuft
  //     unabhängig von der Alarmanlage oben ("alarm.*") — eine reine
  //     Rauch-/Wasser-/Gas-Installation bekommt weiterhin Klassen und
  //     Störungen, nur Zonen bleibt leer. ---
  "security.title": "Sicherheit & Sicherheitstechnik",
  "security.subtitle":
    "Rauch, Wasser, Gas, Sabotage und weitere Gefahrenklassen — funktioniert mit oder ohne Alarmanlage.",
  "security.tab.overview": "Übersicht",
  "security.tab.sources": "Quellen",
  "security.tab.faults": "Störungen",
  "security.intro.overview":
    "Der zusammengefasste Schweregrad, eine Kachel pro Gefahrenklasse, die letzte Meldung und die Anzahl offener Störungen.",
  "security.intro.sources":
    "Jeder klassifizierte Datenpunkt, den die Domäne kennt — filtern und eine falsche Klassifizierung korrigieren.",
  "security.intro.faults":
    "Offene Störungen, älteste zuerst. Quittieren vermerkt nur, dass du es gesehen hast — es behebt die Störung nicht.",
  // Gefahren-/Störungsklassen, in Eskalationsreihenfolge.
  "security.class.smoke": "Rauch erkannt",
  "security.class.water": "Wasser erkannt",
  "security.class.gas": "Gas erkannt",
  "security.class.co": "Kohlenmonoxid erkannt",
  "security.class.tamper": "Sabotage erkannt",
  "security.class.battery": "Batterie schwach",
  "security.class.technical": "Technische Störung",
  "security.class.intrusion": "Öffnung oder Bewegung erkannt",
  "security.class.panic": "Panikruf ausgelöst",
  // Zusammengefasster Schweregrad.
  "security.severity.ok": "OK",
  "security.severity.info": "Info",
  "security.severity.warning": "Warnung",
  "security.severity.alarm": "Alarm",
  "security.severity.critical": "Kritisch",
  // Gründe offener Störungen.
  "security.fault_reason.unreachable": "Nicht erreichbar",
  "security.fault_reason.blocked": "Blockiert",
  "security.fault_reason.device_error": "Gerätefehler",
  "security.fault_reason.central_lost": "CCU-Verbindung verloren",
  "security.fault_reason.duty_cycle": "Duty-Cycle-Grenze",
  "security.fault_reason.low_battery": "Schwache Batterie",
  "security.fault_reason.tamper": "Sabotage",
  // Übersicht.
  "security.overview.empty": "Noch nichts klassifiziert",
  "security.overview.empty.description":
    "Sobald ein Gerät mit Rauch-, Wasser-, Gas-, Sabotage- oder einer anderen Sicherheitsrolle online geht, erscheint es hier automatisch.",
  "security.overview.engine_healthy": "Alarmanlage gesund",
  "security.overview.engine_unhealthy": "Alarmanlage gestört",
  "security.overview.classes_title": "Gefahren- & Störungsklassen",
  "security.overview.no_classes": "Noch keine Quellen klassifiziert",
  "security.overview.no_classes.description":
    "Quellen erscheinen hier, sobald der Klassifikator einen Rauch-, Wasser-, Gas-, Sabotage- oder anderen sicherheitsrelevanten Datenpunkt findet.",
  "security.overview.class_active": "{count} aktiv",
  "security.overview.class_reporting": "Meldet: {count}",
  "security.overview.class_inactive": "Keine aktiven Quellen",
  "security.overview.class_known": "{count} bekannt",
  "security.overview.class_since": "seit {time}",
  "security.overview.sources_more": "+{count} weitere",
  "security.overview.zones_title": "Zonen",
  "security.overview.zone_state_unknown": "Unbekannt",
  "security.overview.zones_empty": "Keine Alarmanlage eingerichtet",
  "security.overview.zones_empty.description":
    "Diese Domäne funktioniert unabhängig von der Alarmanlage — das ist ein Feature, kein Fehler. Richte Zonen im Alarm-Panel ein, damit sie auch hier erscheinen.",
  "security.overview.zones_open_alarm": "Alarm-Panel öffnen",
  "security.overview.faults_count": "{count} offen",
  "security.overview.faults_none": "Keine offenen Störungen",
  "security.overview.last_alarm_title": "Letzte Alarmmeldung",
  "security.overview.last_fault_title": "Letzte Störungsmeldung",
  "security.overview.no_report": "Noch keine Meldung.",
  // Quellen-Inventar.
  "security.sources.filter.class": "Klasse",
  "security.sources.filter.central": "CCU",
  "security.sources.filter.zone": "Zone",
  "security.sources.filter.relevant": "Nur relevante",
  "security.sources.filter.active": "Nur aktive",
  "security.sources.filter.all": "Alle",
  "security.sources.search": "Suchen…",
  "security.sources.empty": "Noch keine klassifizierten Quellen",
  "security.sources.empty.description":
    "Quellen erscheinen hier, sobald der Klassifikator einen Rauch-, Wasser-, Gas-, Sabotage- oder anderen sicherheitsrelevanten Datenpunkt findet.",
  "security.sources.col.source": "Quelle",
  "security.sources.col.class": "Klasse",
  "security.sources.col.central": "CCU",
  "security.sources.col.zone": "Zone",
  "security.sources.col.relevant": "Relevant",
  "security.sources.col.active": "Aktiv",
  "security.sources.col.override": "Override",
  "security.sources.badge.overridden": "Überschrieben",
  "security.sources.badge.relevant": "Relevant",
  "security.sources.badge.not_relevant": "Nicht relevant",
  "security.sources.badge.active": "Aktiv",
  "security.sources.badge.inactive": "Inaktiv",
  "security.sources.intro.title": "Was diese Liste zeigt",
  "security.sources.intro.body":
    "Jeden Datenpunkt, den der Daemon als sicherheitsrelevant eingestuft hat \u2014 Rauch, Wasser, Sabotage, schwache Batterie, unerreichbar und die \u00fcbrigen. \u201eRelevant\u201c hei\u00dft: z\u00e4hlt in die Klassenkacheln und die St\u00f6rungsliste. Alles andere steht hier, damit Sie es finden \u2014 nicht, weil es \u00fcberwacht wird.",
  "security.sources.intro.when":
    "Diese Seite brauchen Sie nur, wenn die Einstufung danebenliegt: ein Melder in der falschen Klasse oder ein Datenpunkt, der gar nichts ausl\u00f6sen sollte. Eine \u00c4nderung wirkt auf das, was die Aggregate melden \u2014 nicht auf die Alarmanlage, die pro Bereich getrennt konfiguriert wird.",
  "security.sources.intro.docs": "Zum Handbuch Sicherheit und Alarmanlage",
  "security.sources.override.help":
    "Klasse leer lassen hei\u00dft: Einstufung des Klassifikators behalten. Wird der Einschluss abgeschaltet, bleibt der Datenpunkt gelistet, z\u00e4hlt aber in kein Aggregat mehr. Die Notiz ist f\u00fcr Sie \u2014 sie h\u00e4lt fest, warum, f\u00fcr die n\u00e4chste Person.",
  "security.sources.override.keep": "Klassifikator-Urteil beibehalten",
  "security.sources.override.included": "Einbezogen",
  "security.sources.override.note_placeholder": "Notiz (optional)",
  "security.sources.override.save": "Override speichern",
  "security.sources.override.reset": "Override entfernen",
  "security.sources.override.reset_title":
    "Zurück zum Urteil des Klassifikators — die Rückgängig-Funktion für einen falschen Override.",
  "security.sources.toast.saved": "Override gespeichert",
  "security.sources.toast.save_failed": "Speichern des Override fehlgeschlagen",
  "security.sources.toast.reset": "Override entfernt",
  "security.sources.toast.reset_failed":
    "Entfernen des Override fehlgeschlagen",
  // Störungen.
  "security.faults.hint":
    "Das Quittieren einer Störung vermerkt nur, dass du sie gesehen hast — die zugrunde liegende Ursache bleibt bestehen, bis sie sich von selbst behebt.",
  "security.faults.empty": "Keine offenen Störungen",
  "security.faults.empty.description":
    "Jede klassifizierte Quelle ist derzeit gesund.",
  "security.faults.col.class": "Klasse",
  "security.faults.col.reason": "Grund",
  "security.faults.col.source": "Quelle",
  "security.faults.col.standing": "Offen seit",
  "security.faults.col.status": "Status",
  "security.faults.col.actions": "Aktionen",
  "security.faults.status.acknowledged": "Quittiert {time}",
  "security.faults.status.acknowledged_by": "Quittiert {time} von {who}",
  "security.faults.status.open": "Noch nicht quittiert",
  "security.faults.acknowledge_confirm.title": "Diese Störung quittieren?",
  "security.faults.acknowledge_confirm.body":
    "Das vermerkt nur, dass du es gesehen hast — die Ursache {reason} bei {source} bleibt bestehen, bis sie sich von selbst behebt.",
  "security.faults.toast.acknowledged":
    "Störung quittiert — Ursache besteht weiterhin",
  "security.faults.toast.acknowledge_failed": "Quittieren fehlgeschlagen",
  "security.faults.duration.days_hours": "{days}d {hours}h",
  "security.faults.duration.hours_minutes": "{hours}h {minutes}m",
  "security.faults.duration.minutes": "{minutes}m",
};

const catalogs: Record<string, Catalog> = { en: EN, de: DE };

function format(s: string, vars?: Record<string, string | number>): string {
  if (!vars) return s;
  return s.replace(/\{(\w+)\}/g, (_, key) => {
    const v = vars[key];
    return v == null ? `{${key}}` : String(v);
  });
}

// Translate a key. Reads `prefs.locale` reactively so any component
// that calls `t()` inside a $derived expression re-renders when the
// language toggles. Falls back through: active locale → English →
// raw key.
export function t(key: string, vars?: Record<string, string | number>): string {
  const loc = prefs.locale;
  const cat = catalogs[loc] ?? EN;
  const value = cat[key] ?? EN[key] ?? key;
  return format(value, vars);
}

/**
 * The keys a single catalogue defines, without the English fallback.
 *
 * `t()` cannot answer "does this locale have the key?" — its fallback
 * chain returns the English string for a missing German entry, so an
 * assertion phrased against `t()` passes on a catalogue gap. This is the
 * only surface that sees one catalogue on its own.
 */
export function catalogKeys(locale: string): readonly string[] {
  return Object.keys(catalogs[locale] ?? {});
}
