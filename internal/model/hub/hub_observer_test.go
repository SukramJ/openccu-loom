// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestOnSysvarRegistered_FiresOnPutSysvar locks the contract used by
// HubMQTTPublisher: PutSysvar after registration must invoke the
// observer with the freshly-registered sysvar so late ReGa refreshes
// trigger per-sysvar wiring.
func TestOnSysvarRegistered_FiresOnPutSysvar(t *testing.T) {
	t.Parallel()
	h := NewHub("c")

	var got []*Sysvar
	var mu sync.Mutex
	h.OnSysvarRegistered(func(s *Sysvar) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	})

	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "Presence"}, ValueType: hmenum.HubValueTypeLogic}
	h.PutSysvar(sv)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != sv {
		t.Fatalf("observer received %v, want [%p]", got, sv)
	}
}

func TestOnSysvarRegistered_NotRetroactive(t *testing.T) {
	t.Parallel()
	h := NewHub("c")
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "early"}})

	var fired atomic.Int32
	h.OnSysvarRegistered(func(*Sysvar) { fired.Add(1) })
	if v := fired.Load(); v != 0 {
		t.Fatalf("retroactive fire: count=%d", v)
	}

	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "late"}})
	if v := fired.Load(); v != 1 {
		t.Fatalf("post-register count=%d, want 1", v)
	}
}

func TestOnSysvarRegistered_UnsubscribeStopsFiring(t *testing.T) {
	t.Parallel()
	h := NewHub("c")
	var fired atomic.Int32
	unsub := h.OnSysvarRegistered(func(*Sysvar) { fired.Add(1) })
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "a"}})
	unsub()
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "b"}})
	// Double-unsub is a no-op.
	unsub()
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "c"}})
	if v := fired.Load(); v != 1 {
		t.Fatalf("count=%d after unsubscribe, want 1", v)
	}
}

func TestOnSysvarRegistered_NilCallbackIsNoOp(t *testing.T) {
	t.Parallel()
	h := NewHub("c")
	unsub := h.OnSysvarRegistered(nil)
	if unsub == nil {
		t.Fatal("unsub must not be nil even for nil callback")
	}
	unsub() // no panic
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: "x"}})
}

func TestOnSysvarRegistered_PutSysvarReplacingExistingFires(t *testing.T) {
	t.Parallel()
	// Replacing under the same name must also fire — the observer
	// rewires OnUpdate to the new instance.
	h := NewHub("c")
	var got []*Sysvar
	h.OnSysvarRegistered(func(s *Sysvar) { got = append(got, s) })
	first := &Sysvar{HubDataPoint: HubDataPoint{Name: "X"}}
	second := &Sysvar{HubDataPoint: HubDataPoint{Name: "X"}}
	h.PutSysvar(first)
	h.PutSysvar(second)
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("replace did not fire twice: %v", got)
	}
}

func TestOnProgramRegistered_FiresOnPutProgram(t *testing.T) {
	t.Parallel()
	h := NewHub("c")
	var got []*Program
	h.OnProgramRegistered(func(p *Program) { got = append(got, p) })

	prog := &Program{ID: "P1", HubDataPoint: HubDataPoint{Name: "Morning"}}
	h.PutProgram(prog)
	if len(got) != 1 || got[0] != prog {
		t.Fatalf("observer got %v, want [%p]", got, prog)
	}
}

func TestOnProgramRegistered_UnsubscribeStopsFiring(t *testing.T) {
	t.Parallel()
	h := NewHub("c")
	var fired atomic.Int32
	unsub := h.OnProgramRegistered(func(*Program) { fired.Add(1) })
	h.PutProgram(&Program{ID: "P1"})
	unsub()
	h.PutProgram(&Program{ID: "P2"})
	if v := fired.Load(); v != 1 {
		t.Fatalf("count=%d after unsubscribe, want 1", v)
	}
}

func TestPutSysvar_IgnoresNilAndEmptyName(t *testing.T) {
	t.Parallel()
	h := NewHub("c")
	var fired atomic.Int32
	h.OnSysvarRegistered(func(*Sysvar) { fired.Add(1) })
	h.PutSysvar(nil)
	h.PutSysvar(&Sysvar{HubDataPoint: HubDataPoint{Name: ""}})
	if v := fired.Load(); v != 0 {
		t.Fatalf("observer fired for invalid input: count=%d", v)
	}
}

func TestPutProgram_IgnoresNilAndEmptyID(t *testing.T) {
	t.Parallel()
	h := NewHub("c")
	var fired atomic.Int32
	h.OnProgramRegistered(func(*Program) { fired.Add(1) })
	h.PutProgram(nil)
	h.PutProgram(&Program{ID: ""})
	if v := fired.Load(); v != 0 {
		t.Fatalf("observer fired for invalid input: count=%d", v)
	}
}
