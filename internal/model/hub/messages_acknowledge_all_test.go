// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// messages_acknowledge_all_test.go covers ServiceMessages.AcknowledgeAll,
// AlarmMessages.AcknowledgeAll and the shared SetAcknowledgers wiring.

package hub

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// stubBulkAck implements BulkMessageAcknowledger with configurable counts
// and errors and records how many times each method was invoked.
type stubBulkAck struct {
	serviceCount int
	alarmCount   int
	serviceErr   error
	alarmErr     error
	serviceCalls int
	alarmCalls   int
}

func (s *stubBulkAck) AcknowledgeAllServiceMessages(context.Context) (int, error) {
	s.serviceCalls++
	return s.serviceCount, s.serviceErr
}

func (s *stubBulkAck) AcknowledgeAllAlarmMessages(context.Context) (int, error) {
	s.alarmCalls++
	return s.alarmCount, s.alarmErr
}

// ─── AlarmMessages.AcknowledgeAll ────────────────────────────────────────────

func TestAlarmMessagesAcknowledgeAll_noBulkAcker(t *testing.T) {
	a := NewAlarmMessages(nil)
	a.Replace([]AlarmMessage{{ID: "x", Timestamp: time.Now()}})
	if _, err := a.AcknowledgeAll(context.Background()); err == nil {
		t.Fatal("expected error without bulk acknowledger")
	}
}

func TestAlarmMessagesAcknowledgeAll_returnsCountAndClears(t *testing.T) {
	bulk := &stubBulkAck{alarmCount: 2}
	a := NewAlarmMessages(nil)
	a.SetAcknowledgers(nil, bulk)
	a.Replace([]AlarmMessage{
		{ID: "a", Timestamp: time.Now()},
		{ID: "b", Timestamp: time.Now()},
	})
	var fired atomic.Int32
	a.OnUpdate(func([]AlarmMessage) { fired.Add(1) })

	n, err := a.AcknowledgeAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count=%d, want 2", n)
	}
	if a.Count() != 0 {
		t.Fatalf("Count=%d after AcknowledgeAll, want 0", a.Count())
	}
	if bulk.alarmCalls != 1 {
		t.Fatalf("alarm bulk called %d times, want 1", bulk.alarmCalls)
	}
	if fired.Load() != 1 {
		t.Fatalf("callback fired %d times, want 1", fired.Load())
	}
}

func TestAlarmMessagesAcknowledgeAll_error_keepsSet(t *testing.T) {
	sentinel := errors.New("rega down")
	bulk := &stubBulkAck{alarmErr: sentinel}
	a := NewAlarmMessages(nil)
	a.SetAcknowledgers(nil, bulk)
	a.Replace([]AlarmMessage{{ID: "a", Timestamp: time.Now()}})

	if _, err := a.AcknowledgeAll(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if a.Count() != 1 {
		t.Fatalf("Count=%d after failed AcknowledgeAll, want 1 (set must be untouched)", a.Count())
	}
}

func TestAlarmMessagesAcknowledgeAll_emptySetNoCallback(t *testing.T) {
	bulk := &stubBulkAck{alarmCount: 0}
	a := NewAlarmMessages(nil)
	a.SetAcknowledgers(nil, bulk)
	a.Replace(nil)
	var fired atomic.Int32
	a.OnUpdate(func([]AlarmMessage) { fired.Add(1) })

	if _, err := a.AcknowledgeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fired.Load() != 0 {
		t.Fatal("callback must not fire when the set was already empty")
	}
}

// ─── ServiceMessages.AcknowledgeAll ──────────────────────────────────────────

func TestServiceMessagesAcknowledgeAll_noBulkAcker(t *testing.T) {
	s := NewServiceMessages(nil)
	s.Replace([]ServiceMessage{{ID: "x", Timestamp: time.Now(), Quittable: true}})
	if _, err := s.AcknowledgeAll(context.Background()); err == nil {
		t.Fatal("expected error without bulk acknowledger")
	}
}

func TestServiceMessagesAcknowledgeAll_removesQuittableOnly(t *testing.T) {
	bulk := &stubBulkAck{serviceCount: 2}
	s := NewServiceMessages(nil)
	s.SetAcknowledgers(nil, bulk)
	s.Replace([]ServiceMessage{
		{ID: "q1", Timestamp: time.Now(), Quittable: true},
		{ID: "q2", Timestamp: time.Now(), Quittable: true},
		{ID: "keep", Timestamp: time.Now(), Quittable: false},
	})
	var fired atomic.Int32
	s.OnUpdate(func([]ServiceMessage) { fired.Add(1) })

	n, err := s.AcknowledgeAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count=%d, want 2", n)
	}
	if s.Count() != 1 {
		t.Fatalf("Count=%d after AcknowledgeAll, want 1 (non-quittable kept)", s.Count())
	}
	remaining := s.List()
	if len(remaining) != 1 || remaining[0].ID != "keep" {
		t.Fatalf("expected only non-quittable 'keep' to remain, got %+v", remaining)
	}
	if bulk.serviceCalls != 1 {
		t.Fatalf("service bulk called %d times, want 1", bulk.serviceCalls)
	}
	if fired.Load() != 1 {
		t.Fatalf("callback fired %d times, want 1", fired.Load())
	}
}

func TestServiceMessagesAcknowledgeAll_error_keepsSet(t *testing.T) {
	sentinel := errors.New("rega down")
	bulk := &stubBulkAck{serviceErr: sentinel}
	s := NewServiceMessages(nil)
	s.SetAcknowledgers(nil, bulk)
	s.Replace([]ServiceMessage{{ID: "q1", Timestamp: time.Now(), Quittable: true}})

	if _, err := s.AcknowledgeAll(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("Count=%d after failed AcknowledgeAll, want 1", s.Count())
	}
}

func TestServiceMessagesAcknowledgeAll_noQuittableNoCallback(t *testing.T) {
	bulk := &stubBulkAck{serviceCount: 0}
	s := NewServiceMessages(nil)
	s.SetAcknowledgers(nil, bulk)
	s.Replace([]ServiceMessage{{ID: "keep", Timestamp: time.Now(), Quittable: false}})
	var fired atomic.Int32
	s.OnUpdate(func([]ServiceMessage) { fired.Add(1) })

	if _, err := s.AcknowledgeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fired.Load() != 0 {
		t.Fatal("callback must not fire when nothing quittable was removed")
	}
	if s.Count() != 1 {
		t.Fatalf("Count=%d, want 1", s.Count())
	}
}

// ─── SetAcknowledgers wires both the single and bulk halves ──────────────────

func TestServiceMessagesSetAcknowledgers_wiresBothHalves(t *testing.T) {
	single := &stubAck{}
	bulk := &stubBulkAck{serviceCount: 3}
	s := NewServiceMessages(nil)
	s.SetAcknowledgers(single, bulk)
	s.Replace([]ServiceMessage{{ID: "x", Timestamp: time.Now(), Quittable: true}})

	if err := s.Acknowledge(context.Background(), "x"); err != nil {
		t.Fatalf("single Acknowledge after SetAcknowledgers: %v", err)
	}
	if len(single.ids) != 1 || single.ids[0] != "x" {
		t.Fatalf("single acker not invoked: %+v", single.ids)
	}
	n, err := s.AcknowledgeAll(context.Background())
	if err != nil {
		t.Fatalf("bulk AcknowledgeAll after SetAcknowledgers: %v", err)
	}
	if n != 3 {
		t.Fatalf("bulk count=%d, want 3", n)
	}
}
