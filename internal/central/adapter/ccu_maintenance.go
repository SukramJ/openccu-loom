// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// CCUMaintenanceDomain runs per-central host-maintenance operations
// (reboot, power off, safe and recovery mode, the astro position, the
// firmware download). It resolves the target central from the registry
// and calls the management ports its south profile installed, so the same
// request reaches a CCU's ReGa scripts or an openccu-lite box's system API.
type CCUMaintenanceDomain struct {
	registry *central.Registry
}

// NewCCUMaintenanceDomain wires the live adapter.
func NewCCUMaintenanceDomain(r *central.Registry) *CCUMaintenanceDomain {
	return &CCUMaintenanceDomain{registry: r}
}

// services resolves the named central and its management ports. A central
// that has not come up yet has none installed; that is reported with the
// error a CCU without an interface client gave before the ports existed.
func (a *CCUMaintenanceDomain) services(centralName string) (*central.Unit, central.SystemServices, error) {
	if a.registry == nil {
		return nil, central.SystemServices{}, hmerr.ErrUnknownCentral
	}
	unit, ok := a.registry.Get(centralName)
	if !ok || unit == nil {
		return nil, central.SystemServices{}, hmerr.ErrUnknownCentral
	}
	services := unit.SystemServices()
	return unit, services, nil
}

func errNoSystemServices(unit *central.Unit) error {
	return fmt.Errorf("%w: central %s has no system services yet", ErrSysvarCreatorNoPrimary, unit.Name())
}

// power resolves the named central's power port.
func (a *CCUMaintenanceDomain) power(centralName string) (central.PowerControl, error) {
	unit, s, err := a.services(centralName)
	if err != nil {
		return nil, err
	}
	if s.Power == nil {
		return nil, errNoSystemServices(unit)
	}
	return s.Power, nil
}

// RebootCCU reboots the system behind the named central. It returns
// [hmerr.ErrUnknownCentral] when the central is not registered and
// backends.ErrUnsupported when the system cannot reboot.
func (a *CCUMaintenanceDomain) RebootCCU(ctx context.Context, centralName string) error {
	p, err := a.power(centralName)
	if err != nil {
		return err
	}
	return p.Reboot(ctx)
}

// SetCCUPosition writes the astro reference position of the system behind
// the named central. It returns [hmerr.ErrUnknownCentral] when the central
// is not registered, backends.ErrUnsupported when the system has no such
// setting, and the validation error when a coordinate is out of range.
//
// On success the central's cached SystemInfo is patched in place so the
// fleet view reflects the new position without waiting for the next hub
// wiring pass - the values are known-good, since the CCU path only returns
// nil after the CCU read them back unchanged.
func (a *CCUMaintenanceDomain) SetCCUPosition(ctx context.Context, centralName string, longitude, latitude float64) error {
	unit, s, err := a.services(centralName)
	if err != nil {
		return err
	}
	if s.Position == nil {
		return errNoSystemServices(unit)
	}
	if err := s.Position.SetPosition(ctx, longitude, latitude); err != nil {
		return err
	}
	unit.PatchSystemPosition(longitude, latitude)
	return nil
}

// PoweroffCCU shuts down the system behind the named central. Unlike a
// reboot nothing brings it back, so the central stays in the readiness
// gate's "waiting for CCU" state until it is powered on again.
func (a *CCUMaintenanceDomain) PoweroffCCU(ctx context.Context, centralName string) error {
	p, err := a.power(centralName)
	if err != nil {
		return err
	}
	return p.PowerOff(ctx)
}

// EnterSafeMode restarts the system behind the named central into safe
// mode, where the ReGa logic layer stays down so a broken configuration
// can be repaired.
func (a *CCUMaintenanceDomain) EnterSafeMode(ctx context.Context, centralName string) error {
	p, err := a.power(centralName)
	if err != nil {
		return err
	}
	return p.EnterSafeMode(ctx)
}

// EnterRecoveryMode restarts the system behind the named central into its
// recovery system. Only OpenCCU firmware and openccu-lite implement it; a
// stock CCU3 answers with a JSON-RPC error, which is propagated rather than
// swallowed so the operator learns the action did nothing.
func (a *CCUMaintenanceDomain) EnterRecoveryMode(ctx context.Context, centralName string) error {
	p, err := a.power(centralName)
	if err != nil {
		return err
	}
	return p.EnterRecoveryMode(ctx)
}

// DownloadFirmware asks the system behind the named central to fetch the
// newest firmware for itself and stage it for a later install. When
// centralName is empty and exactly one central is registered, that central
// is used — matching the single-CCU convenience of the other system
// endpoints.
//
// There is no image parameter: the system resolves the download from its
// own version and board, so the target is the box, not a caller's URL.
//
// Returns [hmerr.ErrUnknownCentral] when the central cannot be resolved
// and backends.ErrUnsupported when the system cannot download firmware;
// the system's own error is propagated verbatim otherwise, including its
// report that the download failed.
func (a *CCUMaintenanceDomain) DownloadFirmware(ctx context.Context, centralName string) error {
	if a.registry == nil {
		return hmerr.ErrUnknownCentral
	}
	unit, err := a.resolveCentral(centralName)
	if err != nil {
		return err
	}
	s := unit.SystemServices()
	if s.Firmware == nil {
		return errNoSystemServices(unit)
	}
	return s.Firmware.DownloadSystemFirmware(ctx)
}

// resolveCentral looks up the target central by name, defaulting to the
// sole registered central when name is empty. Returns
// [hmerr.ErrUnknownCentral] when the name is unknown or when no name was
// given but the daemon manages more than one central.
func (a *CCUMaintenanceDomain) resolveCentral(name string) (*central.Unit, error) {
	if name != "" {
		unit, ok := a.registry.Get(name)
		if !ok || unit == nil {
			return nil, hmerr.ErrUnknownCentral
		}
		return unit, nil
	}
	units := a.registry.List()
	if len(units) == 1 && units[0] != nil {
		return units[0], nil
	}
	return nil, hmerr.ErrUnknownCentral
}
