// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestLiteFeatureTableMatchesRefusingPorts pins that every feature the
// lite table marks as absent from the system is refused, with that
// feature named, by the port the lite profile actually installs. The
// feature set a client reads and the behaviour it meets are built from the
// same table and cannot drift: a key without a refusing port here fails,
// and so does a port that answers anything but the refusal.
func TestLiteFeatureTableMatchesRefusingPorts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	unit, err := central.New(central.Config{Name: "box"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.SetFeatures(liteFeatures(occulited.ExpandScopes([]string{"*"})))
	p := &liteProfile{cc: config.CentralConfig{Name: "box"}, logger: slog.New(slog.DiscardHandler)}
	unit.SetSystemServices(p.SystemServices(unit, WireDeps{}))
	p.wireHubWriters(unit, newLiteSystem(p, unit))
	services := unit.SystemServices()

	calls := map[hmenum.Feature]func() error{
		hmenum.FeatureHubSysvars: func() error {
			return unit.HubModel.CreateSysvarRemote(ctx, hub.SysvarCreateSpec{Name: "x"})
		},
		hmenum.FeatureHubPrograms:      func() error { return unit.Hub.ExecuteProgram(ctx, "1") },
		hmenum.FeatureHubAlarmMessages: func() error { return unit.HubModel.Messages.Acknowledge(ctx, "1") },
		hmenum.FeatureHubInbox:         func() error { return unit.HubModel.AcceptInboxDeviceRemote(ctx, "VCU0000001") },
		hmenum.FeatureHubServiceMessagesAck: func() error {
			return unit.HubModel.ServiceMessages.Acknowledge(ctx, "1")
		},
		hmenum.FeatureSystemSafeMode: func() error { return services.Power.EnterSafeMode(ctx) },
		hmenum.FeatureSystemPosition: func() error { return services.Position.SetPosition(ctx, 1, 1) },
		hmenum.FeatureDeviceCommunicationTest: func() error {
			_, err := backends.NewLiteBackend(hmenum.InterfaceHmIPRF, nil, nil).TestDevice(ctx, "VCU0000001", 0, 0)
			return err
		},
	}

	var absent []hmenum.Feature
	for k, req := range liteFeatureTable {
		if req.never {
			absent = append(absent, k)
		}
	}
	sort.Slice(absent, func(i, j int) bool { return absent[i] < absent[j] })
	for _, k := range absent {
		call, ok := calls[k]
		if !ok {
			t.Errorf("%s is absent on openccu-lite but no refusing port is exercised for it", k)
			continue
		}
		err := call()
		fe, ok := errors.AsType[*hmerr.FeatureUnavailableError](err)
		if !ok || fe.Feature != k || fe.Reason != hmenum.FeatureReasonNotSupported {
			t.Errorf("%s: err = %v, want FeatureUnavailableError for that feature, not_supported_by_system", k, err)
		}
	}
	for k := range calls {
		if req := liteFeatureTable[k]; !req.never {
			t.Errorf("%s has a refusing port here but the table offers it", k)
		}
	}
}
