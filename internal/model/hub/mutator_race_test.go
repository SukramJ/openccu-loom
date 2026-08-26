// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"sync"
	"testing"
)

// raceMutator is a no-op implementation of every hub mutator interface,
// used to exercise the guarded setter/getter under -race.
type raceMutator struct{}

func (raceMutator) CreateSysvar(context.Context, SysvarCreateSpec) error       { return nil }
func (raceMutator) DeleteSysvar(context.Context, string) error                 { return nil }
func (raceMutator) UpdateSysvar(context.Context, SysvarUpdateSpec) error       { return nil }
func (raceMutator) SetDeviceRooms(context.Context, string, []string) error     { return nil }
func (raceMutator) SetDeviceFunctions(context.Context, string, []string) error { return nil }
func (raceMutator) TriggerBackup(context.Context) error                        { return nil }
func (raceMutator) BackupStatus(context.Context) (string, error)               { return "", nil }
func (raceMutator) TriggerFirmwareUpdate(context.Context) error                { return nil }
func (raceMutator) AcceptDeviceInInbox(context.Context, string) error          { return nil }

// TestHubMutatorConcurrentSetAndRead is the race tripwire for the background
// WireHub recovery: a successful retry re-applies the hub mutators while the
// daemon may be concurrently servicing a hub write (CreateSysvar, TriggerBackup,
// …). Direct field access would be a data race; SetMutator + the guarded
// reader methods must serialise through the hub mutex. Run with -race.
func TestHubMutatorConcurrentSetAndRead(t *testing.T) {
	t.Parallel()
	h := NewHub("race-central")
	m := raceMutator{}
	ctx := context.Background()

	var wg sync.WaitGroup
	// Writer: repeatedly (re)wire the mutators, as the retry path does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			h.SetMutator(m)
		}
	}()
	// Readers: the production remote-operation methods snapshot the mutator.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				_ = h.TriggerBackupRemote(ctx)
				_ = h.CreateSysvarRemote(ctx, SysvarCreateSpec{Name: "x", ValueType: "FLOAT", Min: "0", Max: "1"})
				_ = h.SetDeviceRoomsRemote(ctx, "ABC:1", nil)
				_ = h.AcceptInboxDeviceRemote(ctx, "ABC")
			}
		}()
	}
	wg.Wait()
}
