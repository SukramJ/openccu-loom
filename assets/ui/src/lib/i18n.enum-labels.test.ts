// @vitest-environment happy-dom
import { describe, it, expect, afterEach } from "vitest";
import { t } from "$lib/i18n";
import { prefs } from "$lib/stores/preferences.svelte";

// Several views render a daemon-owned enum straight into a badge or a table
// cell. t() falls back to the raw key on a miss, so a missing entry does not
// throw and does not fail a component test whose t() is mocked to echo its
// argument — it just puts a dotted key or an English token in front of the
// operator, in both locales. These tables carry the vocabularies the daemon
// can actually emit, so a new engine event / audit action / capture state
// fails here instead of on screen.
//
// Keep each list in step with its Go source when that source gains a value:
//   alarm journal classes  pkg/hmenum/alarm.go        (AlarmJournalClass)
//   alarm journal events   internal/alarm/**          (JournalEntry.Event)
//   audit actions          internal/audit/audit.go    (Action constants)
//   capture states         internal/diagnostics/capture.go (Status)
//   health statuses        internal/health/tracker.go (Status)
//   incident severities    pkg/hmenum/incident.go     (IncidentSeverity)

const ALARM_JOURNAL_CLASSES = [
  "arm",
  "disarm",
  "trigger",
  "silence",
  "bypass",
  "fault",
  "test",
  "config",
  "maintenance",
];

const ALARM_JOURNAL_EVENTS = [
  "acknowledged",
  "acoustic_budget_exhausted",
  "activation_during_downtime",
  "always_on_activation",
  "arm_failed_on_restore",
  "arm_reminder",
  "armed",
  "armed_after_closing",
  "arming_resumed",
  "arming_started",
  "auto_rearm_cancelled",
  "auto_rearm_deferred",
  "auto_rearm_failed",
  "auto_rearm_mode_unavailable",
  "auto_rearm_resumed",
  "auto_rearm_scheduled",
  "auto_rearmed",
  "central_lost_while_armed",
  "central_restored",
  "code_action_failed",
  "code_locked_out",
  "code_lockout",
  "code_permission_denied",
  "cross_zone_first_hit",
  "disarmed",
  "disarmed_post_trigger",
  "duress",
  "failed_to_arm",
  "implausible_clock_on_restore",
  "incident_load_failed",
  "incident_lost_on_restore",
  "incident_persist_failed",
  "keypad_blocked",
  "keypad_press_unmatched",
  "mode_removed_while_armed",
  "motion_reset",
  "orphan_incident_adopted",
  "orphan_incident_closed",
  "output_fire_failed",
  "output_stop_failed",
  "output_stop_unverified",
  "pending_demoted_implausible_clock",
  "pending_elapsed_while_down",
  "pending_resumed",
  "pending_started",
  "pre_alarm_escalated",
  "pre_alarm_restored_as_full",
  "reconcile_stopped_unowned_siren",
  "refire_account_failed",
  "restart_loop_breaker_degraded",
  "retrigger_account_failed",
  "retrigger_cycle",
  "schedule_arm_failed",
  "sensor_activity",
  "sensor_activity_pending",
  "sensor_bypassed",
  "sensor_config_unparseable",
  "sensor_sabotage",
  "sensor_unavailable_while_armed",
  "silence_persist_failed",
  "silence_requested",
  "silenced",
  "silenced_incident_restored",
  "sounding_siren_adopted",
  "state_persist_failed",
  "sysvar_arm_failed",
  "sysvar_disarm_failed",
  "sysvar_disarm_refused",
  "sysvar_intent_ambiguous",
  "tamper_while_disarmed",
  "trigger_window_elapsed_while_down",
  "triggered",
  "triggered_restored",
  "triggered_restored_implausible_clock",
  "unknown_persisted_state",
  "walktest_finished",
  "walktest_sensor_seen",
  "walktest_started",
  "zone_config_unparseable",
  "zone_removed_while_armed",
];

const AUDIT_ACTIONS = [
  "active_profile",
  "addon_update_install",
  "alarm_acknowledge",
  "alarm_arm",
  "alarm_code_change",
  "alarm_config_change",
  "alarm_disarm",
  "alarm_motion_reset",
  "alarm_output_test",
  "alarm_silence",
  "alarm_walk_test",
  "area_change",
  "backup_pre_update",
  "backup_upload",
  "central_create",
  "central_delete",
  "central_update",
  "channel_flags",
  "config_section_delete",
  "config_section_update",
  "data_point_write",
  "device_assignment",
  "device_communication_test",
  "device_config_restore",
  "device_install_mode",
  "device_replace",
  "device_search",
  "device_team_set",
  "diagram_config",
  "group_admin",
  "incidents_clear",
  "install_mode",
  "install_mode_local",
  "link_activate",
  "link_add",
  "link_paramset_write",
  "link_remove",
  "link_update",
  "matter_commissioning",
  "matter_exposure_bulk",
  "matter_exposure_update",
  "matter_fabric_revoke",
  "matter_factory_reset",
  "matter_force_sync",
  "matter_share",
  "paramset_write",
  "program_delete",
  "program_execute",
  "recording_toggle",
  "room_function",
  "schedule_write",
  "system_ccu_position",
  "system_ccu_poweroff",
  "system_ccu_reboot",
  "system_ccu_recovery_mode",
  "system_ccu_safe_mode",
  "system_firmware_download",
  "tls_cert_upload",
  "token_create",
  "token_revoke",
  "un_ignore_update",
  "user_create",
  "user_delete",
  "user_update",
];

const CAPTURE_STATES = ["running", "stopped", "expired", "aborted"];
const HEALTH_STATUSES = ["healthy", "degraded", "unhealthy", "unknown"];
const INCIDENT_SEVERITIES = ["info", "warning", "error", "critical"];

// Fails with the offending keys listed rather than on the first miss, so one
// run reports the whole gap.
function expectAllResolve(prefix: string, tokens: string[]) {
  for (const locale of ["en", "de"] as const) {
    prefs.locale = locale;
    const missing = tokens
      .map((token) => `${prefix}${token}`)
      .filter((key) => t(key) === key);
    expect(missing, `missing in ${locale}`).toEqual([]);
  }
}

describe("daemon enum vocabularies have labels in both locales", () => {
  const originalLocale = prefs.locale;
  afterEach(() => {
    prefs.locale = originalLocale;
  });

  it("labels every alarm journal class", () => {
    expectAllResolve("alarm.journal_class.", ALARM_JOURNAL_CLASSES);
  });

  it("labels every alarm journal event", () => {
    expectAllResolve("alarm.journal_event.", ALARM_JOURNAL_EVENTS);
  });

  it("labels every audit action", () => {
    expectAllResolve("audit.action.", AUDIT_ACTIONS);
  });

  it("labels every debug-capture state", () => {
    expectAllResolve("diagnostics.capture_status.", CAPTURE_STATES);
  });

  it("labels every health status", () => {
    expectAllResolve("health.status.", HEALTH_STATUSES);
  });

  it("labels every incident severity", () => {
    expectAllResolve("diagnostics.incident_severity.", INCIDENT_SEVERITIES);
  });
});
