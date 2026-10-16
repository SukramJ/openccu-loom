// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"sync"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/alarm"
	"github.com/SukramJ/go-fabric/contract"
)

// recordingEmitter captures every event the alarm server emits.
type recordingEmitter struct {
	mu     sync.Mutex
	events []uint32
}

func (e *recordingEmitter) MatterEmitEvent(_ uint16, _, event uint32, _ any, _ contract.EventPriority) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *recordingEmitter) snapshot() []uint32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]uint32(nil), e.events...)
}

// TestSmokeSirenEmitsSmokeAlarmEventOnConfirmedStatusChange pins that a
// CCU-confirmed status change reaches the alarm server's event path: the
// status DP's confirmed update must tell the server to diff, otherwise a
// subscribed controller never sees the SmokeAlarm event.
func TestSmokeSirenEmitsSmokeAlarmEventOnConfirmedStatusChange(t *testing.T) {
	t.Parallel()

	s, _, statusDP := newSmokeSirenRig(t)
	srv, ok := s.MatterClusterServers()[0].(*alarm.Server)
	if !ok {
		t.Fatalf("SmokeSiren assembles %T, want *alarm.Server", s.MatterClusterServers()[0])
	}
	rec := &recordingEmitter{}
	srv.SetMatterEventEmitter(rec)

	fireSmokeStatus(t, statusDP, string(SmokeStatusPrimaryAlarm))

	var got int
	for _, id := range rec.snapshot() {
		if id == alarm.EventSmokeAlarm {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("SmokeAlarm events emitted = %d (all events %v), want 1", got, rec.snapshot())
	}
}
