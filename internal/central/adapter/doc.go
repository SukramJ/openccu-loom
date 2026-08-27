// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package adapter wires the central domain into the north-bound
// handler interfaces. Every adapter is a thin pointer-wrap around a
// [*central.Unit] (or the multi-CCU [*central.Registry]) so
// the REST/UI packages can consume the domain without importing
// anything transient.
//
// # File taxonomy
//
// The package is a single cohesive wiring layer (the composition-root
// helpers call into the feature adapters, which share unexported
// helpers and the [*central.Unit] type). It is intentionally NOT split
// into sub-packages — see ADR 0034. To keep a 95-file package navigable,
// the files group into these clusters:
//
//   - Composition root / wiring: ccu_wiring, hub_wiring, cuxd_wiring,
//     pingpong_wiring, health_wiring, hotplug_wiring, reliability_wiring,
//     load_refresh, central_bringup, shutdown_deinit,
//     relevant_init, config, interfaces, stubs — assemble a Unit's
//     coordinators and register the south-bound clients.
//   - Transport callers & callbacks: xmlrpc_caller, jsonrpc_caller,
//     binrpc_caller, ordered_caller, rpc_recorder, callback_handlers,
//     xmlrpc_announcer, wire_value — bridge the InterfaceClient
//     transports to the domain.
//   - CCU session & lifecycle: ccu_auth, ccu_readiness, ccu_maintenance —
//     authenticate, wait for the boot marker, reboot.
//   - Hub surface: hub, hub_mqtt_publisher, hub_sysvar_fetch — programs,
//     sysvars, inbox, service/alarm messages.
//   - Device lifecycle: devices, device_admin, device_availability,
//     device_pipeline, device_reloader, datapoint_resolver,
//     custom_dp_dispatcher, combined_bridge, bound_writer,
//     pending_devices, device_communication.
//   - Device administration: device_replace, device_search, device_team,
//     install_mode, firmware_domain, rssi_domain — the DeviceAdminDomain
//     surface and its siblings.
//   - Groups & rooms: groups, groups_write, room_function_admin.
//   - Direct links: central_links, links, link_resolver, link_profile,
//     link_profiles_adapter, link_param_metadata,
//     climate_link_peer_refresh.
//   - Schedules & week profiles: schedule_enabled, schedule_io,
//     schedule_query_adapter, schedules, week_profile_filter,
//     week_profile_io.
//   - UI schema & labels: uischema_adapter, uischema_groups, uischema_link,
//     labels, valuelabels, parameter_determiner.
//   - Export: config_export, definition_export_service,
//     descriptor_persistence.
//   - Values cache & sources: values_cache_evict, values_cache_flush,
//     values_source_lifecycle, master_values_evict, channel_flags_evict,
//     paramsets.
//   - Reliability & background jobs: reconnector, connectivity_probe,
//     auto_refresh, unobserved_sweep, unobserved_sweep_job,
//     throttle_pools, safego.
//   - Incidents: incidents, incident_publisher.
//   - North-bound sinks: mqtt_sink, mqtt_fanout, eventbridge,
//     eventbridge_live_subs, event_source_feed.
//   - Backup: backup_restorer, backup_storage.
//   - Diagnostics & misc: diagnostics_introspect, interface_id.
//
// When adding a file, place it in (or name it after) the matching
// cluster so this map stays a reliable index.
package adapter
