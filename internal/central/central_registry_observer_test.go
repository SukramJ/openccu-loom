// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"slices"
	"sync"
	"testing"
)

// newTestUnit builds a minimal named unit for registry-observer assertions.
func newTestUnit(t *testing.T, name string) *Unit {
	t.Helper()
	u, err := New(Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%s): %v", name, err)
	}
	return u
}

// TestOnRegisterReplaysOverUnitsAlreadyRegistered is the reason the observer
// exists at all. Every collaborator that wires itself per central is built
// long after the boot centrals have entered the registry, so an observer that
// only fired for future registrations would recreate the exact defect it
// replaces — with more machinery.
func TestOnRegisterReplaysOverUnitsAlreadyRegistered(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, name := range []string{"beta", "alpha"} {
		if err := r.Register(newTestUnit(t, name)); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}

	var seen []string
	remove := r.OnRegister(func(u *Unit) func() {
		seen = append(seen, u.Name())
		return nil
	})
	defer remove()

	if want := []string{"alpha", "beta"}; !slices.Equal(seen, want) {
		t.Fatalf("replay saw %v, want %v (sorted by name, like List)", seen, want)
	}
}

// TestOnRegisterFiresForAUnitRegisteredAfterwards pins the runtime-adopt half:
// a CCU that joins the registry after a collaborator wired itself must reach
// that collaborator without anyone remembering a second registration call.
func TestOnRegisterFiresForAUnitRegisteredAfterwards(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	var seen []string
	remove := r.OnRegister(func(u *Unit) func() {
		seen = append(seen, u.Name())
		return nil
	})
	defer remove()

	if err := r.Register(newTestUnit(t, "adopted")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if want := []string{"adopted"}; !slices.Equal(seen, want) {
		t.Fatalf("observer saw %v, want %v", seen, want)
	}
}

// TestObserversRunInRegistrationOrderAndUnwireInReverse pins the ordering the
// composition root depends on: attach order is registration order, teardown is
// its mirror. An unordered observer set would break the guarantees silently.
func TestObserversRunInRegistrationOrderAndUnwireInReverse(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	var attach, detach []string
	for _, name := range []string{"first", "second", "third"} {
		remove := r.OnRegister(func(*Unit) func() {
			attach = append(attach, name)
			return func() { detach = append(detach, name) }
		})
		t.Cleanup(remove)
	}

	if err := r.Register(newTestUnit(t, "ccu")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if want := []string{"first", "second", "third"}; !slices.Equal(attach, want) {
		t.Fatalf("attach order %v, want %v", attach, want)
	}
	if !r.Unregister("ccu") {
		t.Fatal("Unregister reported the central was not present")
	}
	if want := []string{"third", "second", "first"}; !slices.Equal(detach, want) {
		t.Fatalf("detach order %v, want %v", detach, want)
	}
}

// TestUnregisterRunsEveryObserverUnwireExactlyOnce guards the teardown half.
// A central that is removed and whose observer unwires never ran keeps
// publishing on every plane it was attached to; running them twice is just as
// wrong, because an unsubscribe that runs after the slot was reused detaches a
// live subscription.
func TestUnregisterRunsEveryObserverUnwireExactlyOnce(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	var calls int
	remove := r.OnRegister(func(*Unit) func() {
		return func() { calls++ }
	})

	if err := r.Register(newTestUnit(t, "ccu")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Unregister("ccu")
	r.Unregister("ccu")
	remove()
	remove()
	if calls != 1 {
		t.Fatalf("unwire ran %d times, want exactly 1", calls)
	}
}

// TestRemoveObserverDropsWhatItAttached pins the collaborator's own Stop: a
// subscriber that stops must drop every subscription it holds, including the
// ones for centrals that entered the registry after it started.
func TestRemoveObserverDropsWhatItAttached(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(newTestUnit(t, "boot")); err != nil {
		t.Fatalf("Register(boot): %v", err)
	}

	var detached []string
	remove := r.OnRegister(func(u *Unit) func() {
		name := u.Name()
		return func() { detached = append(detached, name) }
	})
	if err := r.Register(newTestUnit(t, "adopted")); err != nil {
		t.Fatalf("Register(adopted): %v", err)
	}

	remove()
	slices.Sort(detached)
	if want := []string{"adopted", "boot"}; !slices.Equal(detached, want) {
		t.Fatalf("remove detached %v, want %v", detached, want)
	}

	// A later Unregister must not run the same unwire a second time.
	r.Unregister("boot")
	if len(detached) != 2 {
		t.Fatalf("Unregister re-ran a removed observer's unwire: %v", detached)
	}
}

// TestObserverIsNotFiredForARejectedRegistration keeps the observer honest
// about what actually entered the registry: a duplicate name is refused, and
// wiring a unit that is not in the registry would leave a subscription nothing
// can ever tear down.
func TestObserverIsNotFiredForARejectedRegistration(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	var calls int
	remove := r.OnRegister(func(*Unit) func() {
		calls++
		return nil
	})
	defer remove()

	if err := r.Register(newTestUnit(t, "ccu")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register(newTestUnit(t, "ccu")); err == nil {
		t.Fatal("duplicate Register succeeded")
	}
	if calls != 1 {
		t.Fatalf("observer ran %d times, want 1", calls)
	}
}

// TestObserverWiringIsSafeUnderConcurrentRegistration drives the registrar the
// way the daemon does — boot wiring on one goroutine, runtime adopt on another
// — so -race speaks up if the observer list or the per-central unwire ledger is
// ever touched without the wiring lock.
func TestObserverWiringIsSafeUnderConcurrentRegistration(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(newTestUnit(t, "boot")); err != nil {
		t.Fatalf("Register(boot): %v", err)
	}

	var mu sync.Mutex
	var attached int
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for _, name := range []string{"a", "b", "c", "d"} {
			_ = r.Register(newTestUnit(t, name))
		}
	}()
	go func() {
		defer wg.Done()
		for range 4 {
			remove := r.OnRegister(func(*Unit) func() {
				mu.Lock()
				attached++
				mu.Unlock()
				return func() {}
			})
			remove()
		}
	}()
	go func() {
		defer wg.Done()
		for range 4 {
			r.Unregister("a")
			_ = r.Names()
		}
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if attached == 0 {
		t.Fatal("no observer ever attached; the concurrency drive proved nothing")
	}
}
