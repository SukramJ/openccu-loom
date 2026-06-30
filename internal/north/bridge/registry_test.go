// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// fakeService records Start/Stop calls into a shared log. It implements
// Service but NOT HealthReporter; fakeServiceWithHealth embeds it to add
// the optional Healthy method.
type fakeService struct {
	name     string
	log      *[]string
	startErr error
}

type healthResult struct {
	ok     bool
	detail string
}

func (f *fakeService) Name() string { return f.name }

func (f *fakeService) Start(_ context.Context) error {
	*f.log = append(*f.log, "start:"+f.name)
	return f.startErr
}

func (f *fakeService) Stop(_ context.Context) error {
	*f.log = append(*f.log, "stop:"+f.name)
	return nil
}

// Healthy is present only when healthyResult is non-nil, so we use a separate
// type to avoid the method being present on every fakeService.
type fakeServiceWithHealth struct {
	fakeService
	result healthResult
}

func (f *fakeServiceWithHealth) Healthy() (ok bool, detail string) {
	return f.result.ok, f.result.detail
}

// newHealthy returns a fakeServiceWithHealth that reports healthy.
func newHealthy(name string, log *[]string) *fakeServiceWithHealth {
	return &fakeServiceWithHealth{
		fakeService: fakeService{name: name, log: log},
		result:      healthResult{ok: true},
	}
}

// newUnhealthy returns a fakeServiceWithHealth that reports unhealthy.
func newUnhealthy(name, reason string, log *[]string) *fakeServiceWithHealth {
	return &fakeServiceWithHealth{
		fakeService: fakeService{name: name, log: log},
		result:      healthResult{ok: false, detail: reason},
	}
}

// newPlain returns a fakeService that does NOT implement HealthReporter.
func newPlain(name string, log *[]string) *fakeService {
	return &fakeService{name: name, log: log}
}

// newFailing returns a fakeService whose Start returns an error.
func newFailing(name string, log *[]string, err error) *fakeService {
	return &fakeService{name: name, log: log, startErr: err}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestStartAllStartsInRegistrationOrder(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newPlain("alpha", &log))
	reg.Register(newPlain("beta", &log))
	reg.Register(newPlain("gamma", &log))

	if err := reg.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: unexpected error: %v", err)
	}

	want := []string{"start:alpha", "start:beta", "start:gamma"}
	assertLog(t, log, want)
}

func TestStopAllStopsInReverseStartOrder(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newPlain("alpha", &log))
	reg.Register(newPlain("beta", &log))
	reg.Register(newPlain("gamma", &log))

	if err := reg.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	log = log[:0] // clear start entries to inspect only stops
	reg.StopAll(context.Background())

	want := []string{"stop:gamma", "stop:beta", "stop:alpha"}
	assertLog(t, log, want)
}

func TestStartAllRollsBackOnError(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newPlain("alpha", &log))
	reg.Register(newPlain("beta", &log))
	reg.Register(newFailing("gamma", &log, errors.New("boom")))
	reg.Register(newPlain("delta", &log))

	err := reg.StartAll(context.Background())
	if err == nil {
		t.Fatal("StartAll: expected error, got nil")
	}
	if err.Error() != "boom" {
		t.Fatalf("StartAll: wrong error %q", err)
	}

	// alpha and beta were started, so they must be stopped in reverse order.
	// gamma's Start is recorded but its Stop must NOT appear (it never started).
	// delta must never appear at all.
	want := []string{
		"start:alpha",
		"start:beta",
		"start:gamma", // Start was called (it failed)
		"stop:beta",   // rollback: reverse order
		"stop:alpha",
	}
	assertLog(t, log, want)
}

func TestStopAllAfterRollbackIsNoop(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newFailing("alpha", &log, errors.New("fail")))

	_ = reg.StartAll(context.Background())
	log = log[:0]

	// After rollback, StopAll should do nothing.
	reg.StopAll(context.Background())

	if len(log) != 0 {
		t.Fatalf("StopAll after rollback: expected empty log, got %v", log)
	}
}

func TestStopAllIsIdempotent(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newPlain("alpha", &log))

	if err := reg.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	log = log[:0]

	reg.StopAll(context.Background())
	reg.StopAll(context.Background()) // second call must be a no-op

	want := []string{"stop:alpha"}
	assertLog(t, log, want)
}

func TestRegisterNilIsIgnored(t *testing.T) {
	reg := NewRegistry(nil)
	reg.Register(newPlain("real", nil))
	reg.Register(nil)

	if got := len(reg.Services()); got != 1 {
		t.Fatalf("Services(): want 1, got %d", got)
	}
}

func TestServicesReturnsCopy(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newPlain("alpha", &log))

	snap := reg.Services()
	snap[0] = nil // mutate the returned slice

	// The registry must still hold the original service.
	if got := len(reg.Services()); got != 1 {
		t.Fatalf("Services(): want 1 after external mutation, got %d", got)
	}
	if reg.Services()[0] == nil {
		t.Fatal("Services(): internal slice was mutated by external write")
	}
}

func TestHealthAllHealthy(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newHealthy("alpha", &log))
	reg.Register(newHealthy("beta", &log))

	ok, detail := reg.Health()
	if !ok {
		t.Fatalf("Health(): want ok=true, got false; detail=%q", detail)
	}
	if detail != "" {
		t.Fatalf("Health(): want empty detail, got %q", detail)
	}
}

func TestHealthNoReporters(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newPlain("alpha", &log))

	ok, detail := reg.Health()
	if !ok {
		t.Fatalf("Health(): service without HealthReporter should be treated as healthy; got ok=false detail=%q", detail)
	}
}

func TestHealthOneUnhealthy(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newHealthy("alpha", &log))
	reg.Register(newUnhealthy("beta", "disk full", &log))
	reg.Register(newHealthy("gamma", &log))

	ok, detail := reg.Health()
	if ok {
		t.Fatal("Health(): want ok=false when one reporter is unhealthy")
	}
	if !strings.Contains(detail, "beta") {
		t.Errorf("Health(): detail %q should contain unhealthy service name %q", detail, "beta")
	}
	if !strings.Contains(detail, "disk full") {
		t.Errorf("Health(): detail %q should contain unhealthy reason %q", detail, "disk full")
	}
}

func TestHealthNonReporterDoesNotFail(t *testing.T) {
	var log []string
	reg := NewRegistry(nil)
	reg.Register(newPlain("plain", &log))            // no HealthReporter
	reg.Register(newUnhealthy("sick", "oops", &log)) // has HealthReporter, unhealthy

	ok, detail := reg.Health()
	if ok {
		t.Fatal("Health(): want ok=false because sick is unhealthy")
	}
	if !strings.Contains(detail, "sick") {
		t.Errorf("Health(): detail %q should contain %q", detail, "sick")
	}
}

func TestNewRegistryNilLoggerDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewRegistry(nil) panicked: %v", r)
		}
	}()

	reg := NewRegistry(nil)
	var log []string
	reg.Register(newPlain("svc", &log))

	if err := reg.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	reg.StopAll(context.Background())
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func assertLog(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("log length: want %d, got %d\n  want: %v\n  got:  %v", len(want), len(got), want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("log[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}
