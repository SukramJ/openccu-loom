// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
)

// ccuRebooter is the narrow capability a backend exposes when it can reboot
// its CCU host. Only the CCU backend implements it; CUxD / Homegear backends
// do not, so a reboot request routed to one surfaces as unsupported.
type ccuRebooter interface {
	RebootCCU(ctx context.Context) (bool, error)
}

// ccuPositionSetter is the narrow capability a backend exposes when it can
// write its CCU's astro reference position. ReGa-backed only, for the same
// reason as ccuRebooter.
type ccuPositionSetter interface {
	SetCCUPosition(ctx context.Context, longitude, latitude float64) error
}

// ccuHostController is the narrow capability a backend exposes when it can
// drive its CCU host's power and boot mode. Same ReGa/JSON-RPC-only
// constraint as ccuRebooter: CUxD and Homegear backends do not host a CCU.
type ccuHostController interface {
	PoweroffCCU(ctx context.Context) (bool, error)
	EnterSafeMode(ctx context.Context) error
	EnterRecoveryMode(ctx context.Context) error
}

// ccuSystemServices are a CCU central's management ports. Every call
// resolves the central's primary backend at call time — the interface
// clients come and go with bring-up generations — and narrows it to the
// capability the operation needs; a backend without it (CUxD, Homegear)
// answers [backends.ErrUnsupported].
type ccuSystemServices struct {
	unit   *central.Unit
	writer *client.ValueWriter
}

// newCCUSystemServices returns the management ports of a CCU central.
func newCCUSystemServices(unit *central.Unit, writer *client.ValueWriter) central.SystemServices {
	s := &ccuSystemServices{unit: unit, writer: writer}
	return central.SystemServices{Power: s, Position: s, Firmware: s, Groups: &ccuHeatingGroups{unit: unit, writer: writer}}
}

func (s *ccuSystemServices) backend() (backends.Operations, error) {
	_, b, err := primaryBackendOf(s.unit, s.writer)
	return b, err
}

func (s *ccuSystemServices) hostController() (ccuHostController, error) {
	b, err := s.backend()
	if err != nil {
		return nil, err
	}
	hc, ok := b.(ccuHostController)
	if !ok {
		return nil, backends.ErrUnsupported
	}
	return hc, nil
}

// Reboot implements [central.PowerControl] with the reboot_ccu ReGa script.
func (s *ccuSystemServices) Reboot(ctx context.Context) error {
	b, err := s.backend()
	if err != nil {
		return err
	}
	rb, ok := b.(ccuRebooter)
	if !ok {
		return backends.ErrUnsupported
	}
	_, err = rb.RebootCCU(ctx)
	return err
}

// PowerOff implements [central.PowerControl].
func (s *ccuSystemServices) PowerOff(ctx context.Context) error {
	hc, err := s.hostController()
	if err != nil {
		return err
	}
	_, err = hc.PoweroffCCU(ctx)
	return err
}

// EnterSafeMode implements [central.PowerControl].
func (s *ccuSystemServices) EnterSafeMode(ctx context.Context) error {
	hc, err := s.hostController()
	if err != nil {
		return err
	}
	return hc.EnterSafeMode(ctx)
}

// EnterRecoveryMode implements [central.PowerControl].
func (s *ccuSystemServices) EnterRecoveryMode(ctx context.Context) error {
	hc, err := s.hostController()
	if err != nil {
		return err
	}
	return hc.EnterRecoveryMode(ctx)
}

// SetPosition implements [central.PositionWriter].
func (s *ccuSystemServices) SetPosition(ctx context.Context, longitude, latitude float64) error {
	b, err := s.backend()
	if err != nil {
		return err
	}
	ps, ok := b.(ccuPositionSetter)
	if !ok {
		return backends.ErrUnsupported
	}
	return ps.SetCCUPosition(ctx, longitude, latitude)
}

// DownloadSystemFirmware implements [central.SystemFirmwareDownloader].
func (s *ccuSystemServices) DownloadSystemFirmware(ctx context.Context) error {
	b, err := s.backend()
	if err != nil {
		return err
	}
	return b.DownloadFirmware(ctx)
}
