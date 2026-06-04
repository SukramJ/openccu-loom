// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_AddOnStopHook_CalledInDaemon pins that daemon.go registers at
// least one AddOnStopHook on a Unit.  Stop hooks are the only
// mechanism for graceful CCU teardown (unsubscribe, close connections);
// losing them leaves the CCU with orphaned callbacks after a restart.
func TestPin_AddOnStopHook_CalledInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"internal/central", "AddOnStopHook",
	)
}

// TestPin_BINRPCCallbackServer_Started pins that daemon.go constructs a
// BINRPCServer and populates WireDeps.BINRPCCallbackServer. Without this
// CUxD interfaces cannot receive push callbacks — only XML-RPC would be
// started, violating the critical rule that CUxD uses BIN-RPC exclusively.
func TestPin_BINRPCCallbackServer_Started(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"internal/central/rpcserver", "NewBINRPCServer",
	)
}

// TestPin_BINRPCCallbackServer_WiredToDeps pins that daemon.go sets
// BINRPCCallbackServer on WireDeps so every CUxD-backed central gets the
// shared listener registered at interface init time.
func TestPin_BINRPCCallbackServer_WiredToDeps(t *testing.T) {
	contract.MustFindStructLiteralField(
		t,
		"cmd/openccu-loom",
		"WireDeps", "BINRPCCallbackServer",
	)
}

// TestPin_NewMqttCollector_CalledInDaemon pins that daemon.go constructs the
// MQTT metrics collector via NewMqttCollector.  Without this call the MQTT
// supervisor never receives a collector and all per-topic publish metrics are
// silently dropped.
func TestPin_NewMqttCollector_CalledInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"internal/metrics", "NewMqttCollector",
	)
}

// TestPin_SetCollector_CalledInDaemon pins that daemon.go hands the
// mqttCollector to the MQTT supervisor via SetCollector.  The two-step
// pattern (construct then wire) is intentional; this pin ensures neither
// step is accidentally dropped.
func TestPin_SetCollector_CalledInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"internal/north/mqtt", "SetCollector",
	)
}
