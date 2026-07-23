// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// RegaScript names a HomeMatic Script that openccu-loom can run on the
// CCU via ReGa.runScript. The identifier matches the filename (without
// the .fn extension) of the embedded script body in
// internal/client/rega/scripts.
type RegaScript string

// Known ReGa scripts. The MVP ships only the subset relevant to v1.0
// hub entities; post-MVP additions (inbox, backup, firmware upgrade)
// will land alongside the coordinators that use them.
const (
	RegaScriptAcknowledgeMessage            RegaScript = "acknowledge_message"
	RegaScriptAcknowledgeAllServiceMessages RegaScript = "acknowledge_all_service_messages"
	RegaScriptAcknowledgeAllAlarmMessages   RegaScript = "acknowledge_all_alarm_messages"
	RegaScriptFetchAllDeviceData            RegaScript = "fetch_all_device_data"
	RegaScriptGetAlarmMessages              RegaScript = "get_alarm_messages"
	RegaScriptGetBackendInfo                RegaScript = "get_backend_info"
	RegaScriptGetProgramDescriptions        RegaScript = "get_program_descriptions"
	RegaScriptGetSerial                     RegaScript = "get_serial"
	RegaScriptGetServiceMessages            RegaScript = "get_service_messages"
	RegaScriptGetSystemUpdateInfo           RegaScript = "get_system_update_info"
	RegaScriptGetSystemVariableDescriptions RegaScript = "get_system_variable_descriptions"
	RegaScriptSetProgramState               RegaScript = "set_program_state"
	RegaScriptExecuteProgramConditional     RegaScript = "execute_program_conditional"
	RegaScriptDeleteProgram                 RegaScript = "delete_program"
	RegaScriptSetSystemVariable             RegaScript = "set_system_variable"
	// Lifecycle scripts. Names match
	// Reference where one exists
	// openccu-loom-only additions (sysvar CRUD, room assignment) carry
	// their own filenames.
	RegaScriptCreateSystemVariable  RegaScript = "create_system_variable"
	RegaScriptUpdateSystemVariable  RegaScript = "update_system_variable"
	RegaScriptSetDeviceRooms        RegaScript = "set_device_rooms"
	RegaScriptSetDeviceFunctions    RegaScript = "set_device_functions"
	RegaScriptStartComTest          RegaScript = "start_com_test"
	RegaScriptPollComTest           RegaScript = "poll_com_test"
	RegaScriptGetUserLevel          RegaScript = "get_user_level"
	RegaScriptCreateRoom            RegaScript = "create_room"
	RegaScriptRenameRoom            RegaScript = "rename_room"
	RegaScriptDeleteRoom            RegaScript = "delete_room"
	RegaScriptCreateFunction        RegaScript = "create_function"
	RegaScriptRenameFunction        RegaScript = "rename_function"
	RegaScriptDeleteFunction        RegaScript = "delete_function"
	RegaScriptAcceptDeviceInInbox   RegaScript = "accept_device_in_inbox"
	RegaScriptGetInboxDevices       RegaScript = "get_inbox_devices"
	RegaScriptTriggerFirmwareUpdate RegaScript = "trigger_firmware_update"
	RegaScriptRebootCCU             RegaScript = "reboot_ccu"
	RegaScriptCreateBackupStart     RegaScript = "create_backup_start"
	RegaScriptCreateBackupStatus    RegaScript = "create_backup_status"
	RegaScriptUsageBySysvar         RegaScript = "usage_by_sysvar"
)

// String returns the script's internal identifier.
func (s RegaScript) String() string { return string(s) }

// AllRegaScripts lists every known script. Stable order — tests iterate
// over this slice.
var AllRegaScripts = []RegaScript{
	RegaScriptAcknowledgeMessage,
	RegaScriptAcknowledgeAllServiceMessages,
	RegaScriptAcknowledgeAllAlarmMessages,
	RegaScriptFetchAllDeviceData,
	RegaScriptGetAlarmMessages,
	RegaScriptGetBackendInfo,
	RegaScriptGetProgramDescriptions,
	RegaScriptGetSerial,
	RegaScriptGetServiceMessages,
	RegaScriptGetSystemUpdateInfo,
	RegaScriptGetSystemVariableDescriptions,
	RegaScriptSetProgramState,
	RegaScriptExecuteProgramConditional,
	RegaScriptDeleteProgram,
	RegaScriptSetSystemVariable,
	RegaScriptCreateSystemVariable,
	RegaScriptUpdateSystemVariable,
	RegaScriptSetDeviceRooms,
	RegaScriptSetDeviceFunctions,
	RegaScriptStartComTest,
	RegaScriptPollComTest,
	RegaScriptGetUserLevel,
	RegaScriptCreateRoom,
	RegaScriptRenameRoom,
	RegaScriptDeleteRoom,
	RegaScriptCreateFunction,
	RegaScriptRenameFunction,
	RegaScriptDeleteFunction,
	RegaScriptAcceptDeviceInInbox,
	RegaScriptGetInboxDevices,
	RegaScriptTriggerFirmwareUpdate,
	RegaScriptRebootCCU,
	RegaScriptCreateBackupStart,
	RegaScriptCreateBackupStatus,
	RegaScriptUsageBySysvar,
}
