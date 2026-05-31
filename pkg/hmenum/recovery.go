// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// ConnectionStage tracks reconnection progress after a connection loss.
type ConnectionStage int

// ConnectionStage values.
const (
	ConnectionStageLost         ConnectionStage = 0
	ConnectionStageTCPAvailable ConnectionStage = 1
	ConnectionStageRPCAvailable ConnectionStage = 2
	ConnectionStageWarmup       ConnectionStage = 3
	ConnectionStageEstablished  ConnectionStage = 4
)

// DisplayName returns a human-friendly stage description.
func (s ConnectionStage) DisplayName() string {
	switch s {
	case ConnectionStageLost:
		return "Connection Lost"
	case ConnectionStageTCPAvailable:
		return "TCP Port Available"
	case ConnectionStageRPCAvailable:
		return "RPC Responding"
	case ConnectionStageWarmup:
		return "Warmup Period"
	case ConnectionStageEstablished:
		return "Connection Established"
	default:
		return "Unknown"
	}
}

// RecoveryStage names a step of the unified recovery pipeline.
type RecoveryStage string

// RecoveryStage values.
const (
	RecoveryStageIdle           RecoveryStage = "idle"
	RecoveryStageDetecting      RecoveryStage = "detecting"
	RecoveryStageCooldown       RecoveryStage = "cooldown"
	RecoveryStageTCPChecking    RecoveryStage = "tcp_checking"
	RecoveryStageRPCChecking    RecoveryStage = "rpc_checking"
	RecoveryStageWarmingUp      RecoveryStage = "warming_up"
	RecoveryStageStabilityCheck RecoveryStage = "stability_check"
	RecoveryStageReconnecting   RecoveryStage = "reconnecting"
	RecoveryStageDataLoading    RecoveryStage = "data_loading"
	RecoveryStageSyncHubData    RecoveryStage = "sync_hub_data"
	RecoveryStageRecovered      RecoveryStage = "recovered"
	RecoveryStageFailed         RecoveryStage = "failed"
	RecoveryStageHeartbeat      RecoveryStage = "heartbeat"
)

// String returns the wire representation.
func (s RecoveryStage) String() string { return string(s) }

// RecoveryResult summarises a completed recovery attempt.
type RecoveryResult string

// RecoveryResult values.
const (
	RecoveryResultSuccess    RecoveryResult = "success"
	RecoveryResultPartial    RecoveryResult = "partial"
	RecoveryResultFailed     RecoveryResult = "failed"
	RecoveryResultMaxRetries RecoveryResult = "max_retries"
	RecoveryResultCancelled  RecoveryResult = "cancelled"
)

// String returns the wire representation.
func (r RecoveryResult) String() string { return string(r) }

// UpdateDeviceHint encodes the type of change reported via the CCU's
// updateDevice callback.
type UpdateDeviceHint int

// UpdateDeviceHint values.
const (
	UpdateDeviceHintFirmware UpdateDeviceHint = 0
	UpdateDeviceHintLinks    UpdateDeviceHint = 1
)
