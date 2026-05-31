// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

// ---------------------------------------------------------------------------
// backend.go — Backend / CCUType / ProductGroup / Manufacturer
// ---------------------------------------------------------------------------

func TestBackendString(t *testing.T) {
	t.Parallel()
	cases := map[Backend]string{
		BackendCCU:      "CCU",
		BackendHomegear: "Homegear",
		BackendPyDevCCU: "PyDevCCU",
	}
	for b, want := range cases {
		if got := b.String(); got != want {
			t.Errorf("Backend(%q).String() = %q, want %q", b, got, want)
		}
	}
}

func TestCCUTypeString(t *testing.T) {
	t.Parallel()
	cases := map[CCUType]string{
		CCUTypeCCU:     "CCU",
		CCUTypeOpenCCU: "OpenCCU",
		CCUTypeUnknown: "Unknown",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("CCUType(%q).String() = %q, want %q", c, got, want)
		}
	}
}

func TestProductGroupString(t *testing.T) {
	t.Parallel()
	cases := map[ProductGroup]string{
		ProductGroupHM:      "BidCos-RF",
		ProductGroupHmIP:    "HmIP-RF",
		ProductGroupHmIPW:   "HmIP-Wired",
		ProductGroupHmW:     "BidCos-Wired",
		ProductGroupVirtual: "VirtualDevices",
		ProductGroupUnknown: "unknown",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("ProductGroup(%q).String() = %q, want %q", p, got, want)
		}
	}
}

func TestManufacturerString(t *testing.T) {
	t.Parallel()
	cases := map[Manufacturer]string{
		ManufacturerEQ3:         "eQ-3",
		ManufacturerHB:          "Homebrew",
		ManufacturerMoehlenhoff: "Möhlenhoff",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("Manufacturer(%q).String() = %q, want %q", m, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// cache.go — CacheType / CacheInvalidationReason / DataRefreshType / DataFetchOperation
// ---------------------------------------------------------------------------

func TestCacheTypeString(t *testing.T) {
	t.Parallel()
	cases := map[CacheType]string{
		CacheTypeDeviceDescription:   "device_description",
		CacheTypeParamsetDescription: "paramset_description",
		CacheTypeData:                "data",
		CacheTypeDetails:             "details",
		CacheTypeVisibility:          "visibility",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("CacheType(%q).String() = %q, want %q", c, got, want)
		}
	}
}

func TestCacheInvalidationReasonString(t *testing.T) {
	t.Parallel()
	cases := map[CacheInvalidationReason]string{
		CacheInvalidationReasonDeviceAdded:   "device_added",
		CacheInvalidationReasonDeviceRemoved: "device_removed",
		CacheInvalidationReasonRefresh:       "refresh",
		CacheInvalidationReasonManual:        "manual",
		CacheInvalidationReasonStartup:       "startup",
		CacheInvalidationReasonShutdown:      "shutdown",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("CacheInvalidationReason(%q).String() = %q, want %q", r, got, want)
		}
	}
}

func TestDataRefreshTypeString(t *testing.T) {
	t.Parallel()
	cases := map[DataRefreshType]string{
		DataRefreshTypeAlarmMessages: "alarm_messages",
		DataRefreshTypeClientData:    "client_data",
		DataRefreshTypeMetrics:       "metrics",
		DataRefreshTypeProgram:       "program",
		DataRefreshTypeSysvar:        "sysvar",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("DataRefreshType(%q).String() = %q, want %q", r, got, want)
		}
	}
}

func TestDataFetchOperationString(t *testing.T) {
	t.Parallel()
	cases := map[DataFetchOperation]string{
		DataFetchOperationFetchDeviceDescriptions:   "fetch_device_descriptions",
		DataFetchOperationFetchParamsetDescriptions: "fetch_paramset_descriptions",
	}
	for o, want := range cases {
		if got := o.String(); got != want {
			t.Errorf("DataFetchOperation(%q).String() = %q, want %q", o, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// command.go — RollbackReason / CallSource / ServiceScope / SystemEventType
// ---------------------------------------------------------------------------

func TestRollbackReasonString(t *testing.T) {
	t.Parallel()
	cases := map[RollbackReason]string{
		RollbackReasonTimeout:       "timeout",
		RollbackReasonSendError:     "send_error",
		RollbackReasonValueMismatch: "mismatch",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("RollbackReason(%q).String() = %q, want %q", r, got, want)
		}
	}
}

func TestCallSourceString(t *testing.T) {
	t.Parallel()
	cases := map[CallSource]string{
		CallSourceHAInit:            "ha_init",
		CallSourceHMInit:            "hm_init",
		CallSourceManualOrScheduled: "manual_or_scheduled",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("CallSource(%q).String() = %q, want %q", s, got, want)
		}
	}
}

func TestServiceScopeString(t *testing.T) {
	t.Parallel()
	cases := map[ServiceScope]string{
		ServiceScopeExternal: "external",
		ServiceScopeInternal: "internal",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("ServiceScope(%q).String() = %q, want %q", s, got, want)
		}
	}
}

func TestSystemEventTypeString(t *testing.T) {
	t.Parallel()
	cases := map[SystemEventType]string{
		SystemEventTypeDeleteDevices:  "deleteDevices",
		SystemEventTypeDevicesCreated: "devicesCreated",
		SystemEventTypeError:          "error",
		SystemEventTypeNewDevices:     "newDevices",
		SystemEventTypeListDevices:    "listDevices",
	}
	for e, want := range cases {
		if got := e.String(); got != want {
			t.Errorf("SystemEventType(%q).String() = %q, want %q", e, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — DataPointCategory / DataPointType / DataPointUsage
// ---------------------------------------------------------------------------

func TestDataPointCategoryString(t *testing.T) {
	t.Parallel()
	cases := map[DataPointCategory]string{
		DataPointCategoryAction:       "action",
		DataPointCategoryBinarySensor: "binary_sensor",
		DataPointCategorySwitch:       "switch",
		DataPointCategoryUndefined:    "undefined",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("DataPointCategory(%q).String() = %q, want %q", c, got, want)
		}
	}
}

func TestDataPointTypeString(t *testing.T) {
	t.Parallel()
	cases := map[DataPointType]string{
		DataPointTypeBinarySensor: "binary_sensor",
		DataPointTypeSwitch:       "switch",
		DataPointTypeSensor:       "sensor",
	}
	for typ, want := range cases {
		if got := typ.String(); got != want {
			t.Errorf("DataPointType(%q).String() = %q, want %q", typ, got, want)
		}
	}
}

func TestDataPointUsageString(t *testing.T) {
	t.Parallel()
	cases := map[DataPointUsage]string{
		DataPointUsageCDPPrimary:   "ce_primary",
		DataPointUsageCDPSecondary: "ce_secondary",
		DataPointUsageDataPoint:    "data_point",
		DataPointUsageEvent:        "event",
		DataPointUsageNoCreate:     "no_create",
	}
	for u, want := range cases {
		if got := u.String(); got != want {
			t.Errorf("DataPointUsage(%q).String() = %q, want %q", u, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// device.go — DeviceFirmwareState / ForcedDeviceAvailability /
//             CommandRxMode / DescriptionMarker / ProfileKey /
//             SourceOfDeviceCreation / DeviceLifecycleSubtype
// ---------------------------------------------------------------------------

func TestDeviceFirmwareStateString(t *testing.T) {
	t.Parallel()
	cases := map[DeviceFirmwareState]string{
		DeviceFirmwareStateUnknown:          "UNKNOWN",
		DeviceFirmwareStateUpToDate:         "UP_TO_DATE",
		DeviceFirmwareStateReadyForUpdate:   "READY_FOR_UPDATE",
		DeviceFirmwareStatePerformingUpdate: "PERFORMING_UPDATE",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("DeviceFirmwareState(%q).String() = %q, want %q", s, got, want)
		}
	}
}

func TestForcedDeviceAvailabilityString(t *testing.T) {
	t.Parallel()
	cases := map[ForcedDeviceAvailability]string{
		ForcedDeviceAvailabilityForceFalse: "forced_not_available",
		ForcedDeviceAvailabilityForceTrue:  "forced_available",
		ForcedDeviceAvailabilityNotSet:     "not_set",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("ForcedDeviceAvailability(%q).String() = %q, want %q", a, got, want)
		}
	}
}

func TestCommandRxModeString(t *testing.T) {
	t.Parallel()
	cases := map[CommandRxMode]string{
		CommandRxModeUnset:      "",
		CommandRxModeBurst:      "BURST",
		CommandRxModeWakeup:     "WAKEUP",
		CommandRxModeLazyConfig: "LAZY_CONFIG",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("CommandRxMode(%q).String() = %q, want %q", m, got, want)
		}
	}
}

func TestDescriptionMarkerString(t *testing.T) {
	t.Parallel()
	cases := map[DescriptionMarker]string{
		DescriptionMarkerHAHM:     "HAHM",
		DescriptionMarkerHX:       "HX",
		DescriptionMarkerInternal: "INTERNAL",
		DescriptionMarkerMQTT:     "MQTT",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("DescriptionMarker(%q).String() = %q, want %q", m, got, want)
		}
	}
}

func TestProfileKeyString(t *testing.T) {
	t.Parallel()
	cases := map[ProfileKey]string{
		ProfileKeyAdditionalDPs:  "additional_dps",
		ProfileKeyDefaultDPs:     "default_dps",
		ProfileKeyDeviceGroup:    "device_group",
		ProfileKeyPrimaryChannel: "primary_channel",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("ProfileKey(%q).String() = %q, want %q", k, got, want)
		}
	}
}

func TestSourceOfDeviceCreationString(t *testing.T) {
	t.Parallel()
	cases := map[SourceOfDeviceCreation]string{
		SourceOfDeviceCreationCache:   "CACHE",
		SourceOfDeviceCreationInit:    "INIT",
		SourceOfDeviceCreationManual:  "MANUAL",
		SourceOfDeviceCreationNew:     "NEW",
		SourceOfDeviceCreationRefresh: "REFRESH",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("SourceOfDeviceCreation(%q).String() = %q, want %q", s, got, want)
		}
	}
}

func TestDeviceLifecycleSubtypeString(t *testing.T) {
	t.Parallel()
	cases := map[DeviceLifecycleSubtype]string{
		DeviceLifecycleSubtypeCreated:             "CREATED",
		DeviceLifecycleSubtypeDelayed:             "DELAYED",
		DeviceLifecycleSubtypeUpdated:             "UPDATED",
		DeviceLifecycleSubtypeRemoved:             "REMOVED",
		DeviceLifecycleSubtypeAvailabilityChanged: "AVAILABILITY_CHANGED",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("DeviceLifecycleSubtype(%q).String() = %q, want %q", s, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// device_profile.go — DeviceProfile
// ---------------------------------------------------------------------------

func TestDeviceProfileString(t *testing.T) {
	t.Parallel()
	cases := map[DeviceProfile]string{
		DeviceProfileIPThermostat: "IPThermostat",
		DeviceProfileIPLock:       "IPLock",
		DeviceProfileRfThermostat: "RfThermostat",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("DeviceProfile(%q).String() = %q, want %q", p, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// hub.go — HubValueType / ProgramTrigger / InternalCustomID
// ---------------------------------------------------------------------------

func TestHubValueTypeString(t *testing.T) {
	t.Parallel()
	cases := map[HubValueType]string{
		HubValueTypeAlarm:   "ALARM",
		HubValueTypeFloat:   "FLOAT",
		HubValueTypeInteger: "INTEGER",
		HubValueTypeList:    "LIST",
		HubValueTypeLogic:   "LOGIC",
		HubValueTypeNumber:  "NUMBER",
		HubValueTypeString:  "STRING",
	}
	for h, want := range cases {
		if got := h.String(); got != want {
			t.Errorf("HubValueType(%q).String() = %q, want %q", h, got, want)
		}
	}
}

func TestProgramTriggerString(t *testing.T) {
	t.Parallel()
	cases := map[ProgramTrigger]string{
		ProgramTriggerAPI:        "api",
		ProgramTriggerUser:       "user",
		ProgramTriggerScheduler:  "scheduler",
		ProgramTriggerAutomation: "automation",
	}
	for tr, want := range cases {
		if got := tr.String(); got != want {
			t.Errorf("ProgramTrigger(%q).String() = %q, want %q", tr, got, want)
		}
	}
}

func TestInternalCustomIDString(t *testing.T) {
	t.Parallel()
	cases := map[InternalCustomID]string{
		InternalCustomIDDefault:  "cid_default",
		InternalCustomIDLinkPeer: "cid_link_peer",
		InternalCustomIDManuTemp: "cid_manu_temp",
	}
	for id, want := range cases {
		if got := id.String(); got != want {
			t.Errorf("InternalCustomID(%q).String() = %q, want %q", id, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// incident.go — IncidentType / IncidentSeverity / IntegrationIssueSeverity /
//               IntegrationIssueType / DeviceTriggerEventType /
//               ServiceMessageType
// ---------------------------------------------------------------------------

func TestIncidentTypeString(t *testing.T) {
	t.Parallel()
	cases := map[IncidentType]string{
		IncidentTypeAuthFailure:    "auth_failure",
		IncidentTypeConnectionLost: "connection_lost",
		IncidentTypeRPCFault:       "rpc_fault",
		IncidentTypeParamsetPatch:  "paramset_patch",
		IncidentTypeConfigError:    "config_error",
	}
	for it, want := range cases {
		if got := it.String(); got != want {
			t.Errorf("IncidentType(%q).String() = %q, want %q", it, got, want)
		}
	}
}

func TestIncidentSeverityString(t *testing.T) {
	t.Parallel()
	cases := map[IncidentSeverity]string{
		IncidentSeverityInfo:     "info",
		IncidentSeverityWarning:  "warning",
		IncidentSeverityError:    "error",
		IncidentSeverityCritical: "critical",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("IncidentSeverity(%q).String() = %q, want %q", s, got, want)
		}
	}
}

func TestIntegrationIssueSeverityString(t *testing.T) {
	t.Parallel()
	cases := map[IntegrationIssueSeverity]string{
		IntegrationIssueSeverityError:   "error",
		IntegrationIssueSeverityWarning: "warning",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("IntegrationIssueSeverity(%q).String() = %q, want %q", s, got, want)
		}
	}
}

func TestIntegrationIssueTypeString(t *testing.T) {
	t.Parallel()
	cases := map[IntegrationIssueType]string{
		IntegrationIssueTypePingPongMismatch:     "ping_pong_mismatch",
		IntegrationIssueTypeFetchDataFailed:      "fetch_data_failed",
		IntegrationIssueTypeIncompleteDeviceData: "incomplete_device_data",
	}
	for it, want := range cases {
		if got := it.String(); got != want {
			t.Errorf("IntegrationIssueType(%q).String() = %q, want %q", it, got, want)
		}
	}
}

func TestDeviceTriggerEventTypeString(t *testing.T) {
	t.Parallel()
	cases := map[DeviceTriggerEventType]string{
		DeviceTriggerEventTypeDeviceError: "homematic.device_error",
		DeviceTriggerEventTypeImpulse:     "homematic.impulse",
		DeviceTriggerEventTypeKeypress:    "homematic.keypress",
	}
	for et, want := range cases {
		if got := et.String(); got != want {
			t.Errorf("DeviceTriggerEventType(%q).String() = %q, want %q", et, got, want)
		}
	}
}

func TestServiceMessageTypeString(t *testing.T) {
	t.Parallel()
	cases := map[ServiceMessageType]string{
		ServiceMessageTypeGeneric:       "generic",
		ServiceMessageTypeSticky:        "sticky",
		ServiceMessageTypeConfigPending: "config_pending",
		ServiceMessageTypeAlarm:         "alarm",
		ServiceMessageTypeUpdatePending: "update_pending",
		ServiceMessageTypeCommunication: "communication",
		ServiceMessageType(99):          "unknown",
	}
	for mt, want := range cases {
		if got := mt.String(); got != want {
			t.Errorf("ServiceMessageType(%d).String() = %q, want %q", mt, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// parameter.go — Parameter.String
// ---------------------------------------------------------------------------

func TestParameterString(t *testing.T) {
	t.Parallel()
	cases := []Parameter{
		ParameterState,
		ParameterLevel,
		ParameterUnreach,
		ParameterLowBat,
		ParameterConfigPending,
		ParameterPressShort,
		ParameterPressLong,
	}
	for _, p := range cases {
		if got := p.String(); got != string(p) {
			t.Errorf("Parameter(%q).String() = %q, want %q", string(p), got, string(p))
		}
	}
}

// ---------------------------------------------------------------------------
// paramset.go — ParamsetKey.String / ParameterType.String /
//               ParameterStatus.String / Operations predicates
// ---------------------------------------------------------------------------

func TestParamsetKeyString(t *testing.T) {
	t.Parallel()
	cases := []ParamsetKey{
		ParamsetKeyMaster, ParamsetKeyValues, ParamsetKeyLink,
		ParamsetKeyService, ParamsetKeyCalculated, ParamsetKeyCombined, ParamsetKeyDummy,
	}
	for _, k := range cases {
		if got := k.String(); got != string(k) {
			t.Errorf("ParamsetKey(%q).String() = %q, want %q", string(k), got, string(k))
		}
	}
}

func TestParameterTypeString(t *testing.T) {
	t.Parallel()
	cases := []ParameterType{
		ParameterTypeAction, ParameterTypeBool, ParameterTypeDummy,
		ParameterTypeEnum, ParameterTypeFloat, ParameterTypeInteger,
		ParameterTypeString, ParameterTypeEmpty,
	}
	for _, pt := range cases {
		if got := pt.String(); got != string(pt) {
			t.Errorf("ParameterType(%q).String() = %q, want %q", string(pt), got, string(pt))
		}
	}
}

func TestParameterStatusString(t *testing.T) {
	t.Parallel()
	cases := []ParameterStatus{
		ParameterStatusNormal, ParameterStatusUnknown, ParameterStatusOverflow,
		ParameterStatusUnderflow, ParameterStatusError, ParameterStatusInvalid,
		ParameterStatusUnused, ParameterStatusExternal,
	}
	for _, ps := range cases {
		if got := ps.String(); got != string(ps) {
			t.Errorf("ParameterStatus(%q).String() = %q, want %q", string(ps), got, string(ps))
		}
	}
}

func TestOperationsPredicates(t *testing.T) {
	t.Parallel()
	full := OperationsRead | OperationsWrite | OperationsEvent
	if !full.IsReadable() {
		t.Error("full: IsReadable() should be true")
	}
	if !full.IsWritable() {
		t.Error("full: IsWritable() should be true")
	}
	if !full.IsEvent() {
		t.Error("full: IsEvent() should be true")
	}
	if !full.Has(OperationsRead | OperationsWrite) {
		t.Error("full: Has(Read|Write) should be true")
	}

	none := OperationsNone
	if none.IsReadable() {
		t.Error("none: IsReadable() should be false")
	}
	if none.IsWritable() {
		t.Error("none: IsWritable() should be false")
	}
	if none.IsEvent() {
		t.Error("none: IsEvent() should be false")
	}
}

// ---------------------------------------------------------------------------
// quantity.go — Quantity.String / ValueBehavior.String
// ---------------------------------------------------------------------------

func TestQuantityString(t *testing.T) {
	t.Parallel()
	cases := []Quantity{
		QuantityTemperature, QuantityHumidity, QuantityPower,
		QuantityEnergy, QuantityVoltage, QuantityCurrent,
		QuantityFrequency, QuantityIlluminance,
		QuantityDistance, QuantitySpeed, QuantityWindDirection,
		QuantityPrecipitation, QuantityDuration, QuantityVolume,
	}
	for _, q := range cases {
		if got := q.String(); got != string(q) {
			t.Errorf("Quantity(%q).String() = %q", string(q), got)
		}
	}
}

func TestValueBehaviorString(t *testing.T) {
	t.Parallel()
	cases := []ValueBehavior{
		ValueBehaviorNone, ValueBehaviorInstantaneous,
		ValueBehaviorCumulative, ValueBehaviorMonotonic,
	}
	for _, v := range cases {
		if got := v.String(); got != string(v) {
			t.Errorf("ValueBehavior(%q).String() = %q", string(v), got)
		}
	}
}

// ---------------------------------------------------------------------------
// recovery.go — RecoveryStage.String / RecoveryResult.String
// ---------------------------------------------------------------------------

func TestRecoveryStageString(t *testing.T) {
	t.Parallel()
	stages := []RecoveryStage{
		RecoveryStageIdle, RecoveryStageDetecting, RecoveryStageCooldown,
		RecoveryStageTCPChecking, RecoveryStageRPCChecking, RecoveryStageWarmingUp,
		RecoveryStageStabilityCheck, RecoveryStageReconnecting, RecoveryStageDataLoading,
		RecoveryStageRecovered, RecoveryStageFailed, RecoveryStageHeartbeat,
	}
	for _, s := range stages {
		if got := s.String(); got != string(s) {
			t.Errorf("RecoveryStage(%q).String() = %q", string(s), got)
		}
	}
}

func TestRecoveryResultString(t *testing.T) {
	t.Parallel()
	results := []RecoveryResult{
		RecoveryResultSuccess, RecoveryResultPartial, RecoveryResultFailed,
		RecoveryResultMaxRetries, RecoveryResultCancelled,
	}
	for _, r := range results {
		if got := r.String(); got != string(r) {
			t.Errorf("RecoveryResult(%q).String() = %q", string(r), got)
		}
	}
}

// ---------------------------------------------------------------------------
// rpc.go — PingPongMismatchType.String / OptionalSettings.String
// ---------------------------------------------------------------------------

func TestPingPongMismatchTypeString(t *testing.T) {
	t.Parallel()
	cases := []PingPongMismatchType{PingPongMismatchPending, PingPongMismatchUnknown}
	for _, c := range cases {
		if got := c.String(); got != string(c) {
			t.Errorf("PingPongMismatchType(%q).String() = %q", string(c), got)
		}
	}
}

func TestOptionalSettingsString(t *testing.T) {
	t.Parallel()
	cases := []OptionalSettings{
		OptionalSettingSRDisableRandomizeOutput,
		OptionalSettingSRRecordSystemInit,
	}
	for _, o := range cases {
		if got := o.String(); got != string(o) {
			t.Errorf("OptionalSettings(%q).String() = %q", string(o), got)
		}
	}
}

// ---------------------------------------------------------------------------
// schedule.go — ScheduleType.String / ScheduleProfile.String / WeekdayStr.String
// ---------------------------------------------------------------------------

func TestScheduleTypeString(t *testing.T) {
	t.Parallel()
	cases := []ScheduleType{ScheduleTypeClimate, ScheduleTypeDefault}
	for _, s := range cases {
		if got := s.String(); got != string(s) {
			t.Errorf("ScheduleType(%q).String() = %q", string(s), got)
		}
	}
}

func TestScheduleProfileString(t *testing.T) {
	t.Parallel()
	profiles := []ScheduleProfile{
		ScheduleProfileP1, ScheduleProfileP2, ScheduleProfileP3,
		ScheduleProfileP4, ScheduleProfileP5, ScheduleProfileP6,
	}
	for _, p := range profiles {
		if got := p.String(); got != string(p) {
			t.Errorf("ScheduleProfile(%q).String() = %q", string(p), got)
		}
	}
}

func TestWeekdayStrString(t *testing.T) {
	t.Parallel()
	days := []WeekdayStr{
		WeekdayStrMonday, WeekdayStrTuesday, WeekdayStrWednesday,
		WeekdayStrThursday, WeekdayStrFriday, WeekdayStrSaturday, WeekdayStrSunday,
	}
	for _, d := range days {
		if got := d.String(); got != string(d) {
			t.Errorf("WeekdayStr(%q).String() = %q", string(d), got)
		}
	}
}

// ---------------------------------------------------------------------------
// state.go — CentralState.String / ClientState.String /
//             CircuitState.String / FailureReason.String
// ---------------------------------------------------------------------------

func TestCentralStateString(t *testing.T) {
	t.Parallel()
	states := []CentralState{
		CentralStateStarting, CentralStateInitializing, CentralStateRunning,
		CentralStateDegraded, CentralStateRecovering, CentralStateFailed, CentralStateStopped,
	}
	for _, s := range states {
		if got := s.String(); got != string(s) {
			t.Errorf("CentralState(%q).String() = %q", string(s), got)
		}
	}
}

func TestClientStateString(t *testing.T) {
	t.Parallel()
	states := []ClientState{
		ClientStateCreated, ClientStateInitializing, ClientStateInitialized,
		ClientStateConnecting, ClientStateConnected, ClientStateDisconnected,
		ClientStateReconnecting, ClientStateStopping, ClientStateStopped, ClientStateFailed,
	}
	for _, s := range states {
		if got := s.String(); got != string(s) {
			t.Errorf("ClientState(%q).String() = %q", string(s), got)
		}
	}
}

func TestCircuitStateString(t *testing.T) {
	t.Parallel()
	states := []CircuitState{CircuitStateClosed, CircuitStateOpen, CircuitStateHalfOpen}
	for _, s := range states {
		if got := s.String(); got != string(s) {
			t.Errorf("CircuitState(%q).String() = %q", string(s), got)
		}
	}
}

func TestFailureReasonString(t *testing.T) {
	t.Parallel()
	reasons := []FailureReason{
		FailureReasonNone, FailureReasonAuth, FailureReasonNetwork,
		FailureReasonInternal, FailureReasonTimeout, FailureReasonCircuitBreaker,
		FailureReasonExhausted, FailureReasonUnknown,
	}
	for _, r := range reasons {
		if got := r.String(); got != string(r) {
			t.Errorf("FailureReason(%q).String() = %q", string(r), got)
		}
	}
}
