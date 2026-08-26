// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// countingSysvarWriter accepts every write and counts it.
type countingSysvarWriter struct {
	mu sync.Mutex
	n  int
}

func (c *countingSysvarWriter) SetSysvar(_ context.Context, _ string, _ any) error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return nil
}

// TestSysvarWriterSurvivesConcurrentRefresh pins the writer field against
// the hub scan that replaces it in place on every pass.
//
// An interface value is two words wide: a write that is not on the same
// lock as the read lets a command observe half of the old value and half
// of the new, and the cheapest observable form of that is a Set that
// reports "no writer configured" while a writer was installed the whole
// time. Run under -race the test also fails on the unsynchronised read
// itself.
func TestSysvarWriterSurvivesConcurrentRefresh(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccuA", "Alarm", "", hmenum.HubValueTypeLogic, &countingSysvarWriter{})

	const rounds = 500
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range rounds {
			// The refresh installs a fresh backend every pass, exactly as
			// the hub scan does for every sysvar it re-reads.
			sv.SetWriter(&countingSysvarWriter{})
		}
	}()
	errs := make(chan error, rounds)
	go func() {
		defer wg.Done()
		for range rounds {
			if err := sv.Set(context.Background(), hmtypes.BoolValue(true)); err != nil {
				errs <- err
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Set failed while a writer was installed throughout: %v", err)
	}
}

// TestSysvarWritableTracksTheInstalledWriter pins the read side used by
// the payload, topic and subtype surfaces: they must answer from the
// currently installed backend, not from a stale snapshot.
func TestSysvarWritableTracksTheInstalledWriter(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccuA", "Alarm", "", hmenum.HubValueTypeLogic, nil)
	if sv.Writable() {
		t.Fatal("a sysvar built without a writer must not report writable")
	}
	if got := sv.MQTTTopics("loom", "ccuA").Set; got != "" {
		t.Fatalf("read-only sysvar advertised a command topic %q", got)
	}
	sv.SetWriter(&countingSysvarWriter{})
	if !sv.Writable() {
		t.Fatal("after SetWriter the sysvar must report writable")
	}
	if got := sv.MQTTTopics("loom", "ccuA").Set; got == "" {
		t.Fatal("a writable sysvar must advertise a command topic")
	}
	sv.SetWriter(nil)
	if sv.Writable() {
		t.Fatal("after SetWriter(nil) the sysvar must be read-only again")
	}
}
