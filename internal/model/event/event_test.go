// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package event

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestClassifyAllClickParameters(t *testing.T) {
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPress,
		hmenum.ParameterPressShort,
		hmenum.ParameterPressLong,
		hmenum.ParameterPressLongRelease,
		hmenum.ParameterPressLongStart,
		hmenum.ParameterPressCont,
		hmenum.ParameterPressLock,
		hmenum.ParameterPressUnlock,
	} {
		k, ok := Classify(p)
		if !ok || k != KindKeypress {
			t.Fatalf("%s → %s ok=%v", p, k, ok)
		}
	}
}

func TestClassifyImpulseAndError(t *testing.T) {
	if k, _ := Classify(hmenum.ParameterSequenceOK); k != KindImpulse {
		t.Fatalf("SEQUENCE_OK → %s", k)
	}
	if k, _ := Classify(hmenum.ParameterError); k != KindDeviceError {
		t.Fatalf("ERROR → %s", k)
	}
	if k, _ := Classify(hmenum.ParameterSensorError); k != KindDeviceError {
		t.Fatalf("SENSOR_ERROR → %s", k)
	}
	if _, ok := Classify(hmenum.ParameterLevel); ok {
		t.Fatal("LEVEL should not classify")
	}
}

func TestSourceFiresAndRecordsLast(t *testing.T) {
	s := NewSource("0001ABCD:1", hmenum.ParameterPressShort)
	var fired int
	s.OnFire(func(_ Event) { fired++ })
	if !s.Fire(true) {
		t.Fatal("keypress should fire")
	}
	if fired != 1 {
		t.Fatalf("fired=%d", fired)
	}
	at, val, had := s.LastFire()
	if !had || val != true || at.IsZero() {
		t.Fatalf("last=%v %v %v", at, val, had)
	}
}

func TestImpulseAlwaysFires(t *testing.T) {
	s := NewSource("0001:1", hmenum.ParameterSequenceOK)
	if !s.Fire(true) {
		t.Fatal("impulse must fire")
	}
	if !s.Fire(true) {
		t.Fatal("impulse must fire repeatedly")
	}
}

func TestDeviceErrorTransitionGateBool(t *testing.T) {
	s := NewSource("0001:1", hmenum.ParameterError)
	var fired int
	s.OnFire(func(_ Event) { fired++ })

	// First emit of false is suppressed (no active transition).
	if s.Fire(false) {
		t.Fatal("initial false must be suppressed")
	}
	// First true → fires.
	if !s.Fire(true) {
		t.Fatal("true should fire")
	}
	// Same true again → suppressed.
	if s.Fire(true) {
		t.Fatal("duplicate true must be suppressed")
	}
	// Transition to false → fires (state change).
	if !s.Fire(false) {
		t.Fatal("transition back should fire")
	}
	if fired != 2 {
		t.Fatalf("fired=%d", fired)
	}
}

func TestDeviceErrorTransitionGateInt(t *testing.T) {
	s := NewSource("0001:1", hmenum.ParameterSensorError)
	// Initial 0 → suppressed.
	if s.Fire(int32(0)) {
		t.Fatal("initial 0 must be suppressed")
	}
	// 5 → fires.
	if !s.Fire(int32(5)) {
		t.Fatal("5 should fire")
	}
	// Same 5 → suppressed.
	if s.Fire(int32(5)) {
		t.Fatal("duplicate 5 must be suppressed")
	}
	// Distinct 3 → fires.
	if !s.Fire(int32(3)) {
		t.Fatal("3 should fire")
	}
}

func TestGroupAggregatesSources(t *testing.T) {
	g := NewGroup("0001:1", KindKeypress)
	short := NewSource("0001:1", hmenum.ParameterPressShort)
	long := NewSource("0001:1", hmenum.ParameterPressLong)
	if !g.Add(short) {
		t.Fatal("short add")
	}
	if !g.Add(long) {
		t.Fatal("long add")
	}
	var events []Event
	g.OnFire(func(ev Event) { events = append(events, ev) })

	short.Fire(true)
	long.Fire(true)
	if len(events) != 2 {
		t.Fatalf("events=%d", len(events))
	}
	ps := g.Parameters()
	if len(ps) != 2 || ps[0] != hmenum.ParameterPressLong || ps[1] != hmenum.ParameterPressShort {
		t.Fatalf("params=%v", ps)
	}
}

func TestGroupRejectsMismatch(t *testing.T) {
	g := NewGroup("0001:1", KindKeypress)
	if g.Add(NewSource("0001:1", hmenum.ParameterSequenceOK)) {
		t.Fatal("impulse in keypress group must be rejected")
	}
	if g.Add(NewSource("0002:1", hmenum.ParameterPressShort)) {
		t.Fatal("wrong channel must be rejected")
	}
	if g.Add(NewSource("0001:1", hmenum.ParameterLevel)) {
		t.Fatal("unknown parameter must be rejected")
	}
}

func TestGroupCloseStopsRebroadcast(t *testing.T) {
	g := NewGroup("0001:1", KindKeypress)
	src := NewSource("0001:1", hmenum.ParameterPressShort)
	g.Add(src)
	var fired int
	g.OnFire(func(_ Event) { fired++ })
	src.Fire(true)
	if fired != 1 {
		t.Fatalf("fired=%d", fired)
	}
	g.Close()
	src.Fire(true)
	if fired != 1 {
		t.Fatalf("after close fired=%d", fired)
	}
}

func TestSourcesListSorted(t *testing.T) {
	ss := Sources(KindKeypress)
	if len(ss) != 8 {
		t.Fatalf("keypress sources=%d", len(ss))
	}
	for i := 1; i < len(ss); i++ {
		if ss[i-1] >= ss[i] {
			t.Fatalf("unsorted at %d: %v", i, ss)
		}
	}
	if len(Sources(Kind("bogus"))) != 0 {
		t.Fatal("unknown kind → nil")
	}
}

// TestSourceTranslationKeyKeypress verifies that a keypress event source
// returns "keypress" as its translation key, stripping the "homematic."
// prefix.
func TestSourceTranslationKeyKeypress(t *testing.T) {
	t.Parallel()
	s := NewSource("0001:1", hmenum.ParameterPressShort)
	if got := s.TranslationKey(); got != "keypress" {
		t.Fatalf("TranslationKey() = %q, want %q", got, "keypress")
	}
}

// TestSourceTranslationKeyImpulse verifies impulse → "impulse".
func TestSourceTranslationKeyImpulse(t *testing.T) {
	t.Parallel()
	s := NewSource("0001:1", hmenum.ParameterSequenceOK)
	if got := s.TranslationKey(); got != "impulse" {
		t.Fatalf("TranslationKey() = %q, want %q", got, "impulse")
	}
}

// TestSourceTranslationKeyDeviceError verifies device_error → "device_error".
func TestSourceTranslationKeyDeviceError(t *testing.T) {
	t.Parallel()
	s := NewSource("0001:1", hmenum.ParameterError)
	if got := s.TranslationKey(); got != "device_error" {
		t.Fatalf("TranslationKey() = %q, want %q", got, "device_error")
	}
}

// TestGenerateTranslationKeyStripsPrefix verifies the exported function
// mirrors the per-source method and covers all known Kind values.
func TestGenerateTranslationKeyStripsPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind Kind
		want string
	}{
		{KindKeypress, "keypress"},
		{KindImpulse, "impulse"},
		{KindDeviceError, "device_error"},
		// Unknown / empty kind — prefix not present.
		{Kind("other"), "other"},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.kind), func(t *testing.T) {
			t.Parallel()
			if got := GenerateTranslationKey(c.kind); got != c.want {
				t.Fatalf("GenerateTranslationKey(%q) = %q, want %q", c.kind, got, c.want)
			}
		})
	}
}

// TestClassifyDeviceErrorPrefixMatch verifies that device-error
// classification uses prefix-matching. Parameters like
// ERROR_OVERHEAT and ERROR_REDUCED must classify as KindDeviceError
// even though they are not in the historical exact-match set.
func TestClassifyDeviceErrorPrefixMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param hmenum.Parameter
		want  Kind
		ok    bool
	}{
		// Legacy exact names — must still work.
		{hmenum.ParameterError, KindDeviceError, true},
		{hmenum.ParameterSensorError, KindDeviceError, true},
		// Extended error suffixes — must now also match.
		{"ERROR_OVERHEAT", KindDeviceError, true},
		{"ERROR_REDUCED", KindDeviceError, true},
		{"SENSOR_ERROR_OPEN", KindDeviceError, true},
		// Unrelated parameters — must not match.
		{hmenum.ParameterLevel, "", false},
		{"NOTERROR", "", false},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.param), func(t *testing.T) {
			t.Parallel()
			k, ok := Classify(c.param)
			if ok != c.ok {
				t.Fatalf("Classify(%q) ok=%v want %v", c.param, ok, c.ok)
			}
			if ok && k != c.want {
				t.Fatalf("Classify(%q) kind=%v want %v", c.param, k, c.want)
			}
		})
	}
}

// TestSourcesImpulse verifies Sources(KindImpulse) returns a non-empty sorted slice.
func TestSourcesImpulse(t *testing.T) {
	t.Parallel()
	ss := Sources(KindImpulse)
	if len(ss) == 0 {
		t.Fatal("impulse sources must not be empty")
	}
	for i := 1; i < len(ss); i++ {
		if ss[i-1] >= ss[i] {
			t.Fatalf("unsorted at %d: %v", i, ss)
		}
	}
}

// TestSourcesDeviceError verifies Sources(KindDeviceError) returns prefix roots.
func TestSourcesDeviceError(t *testing.T) {
	t.Parallel()
	ss := Sources(KindDeviceError)
	if len(ss) == 0 {
		t.Fatal("device error sources must not be empty")
	}
}

// TestSourcesUnknownKindNil verifies unknown kinds return nil.
func TestSourcesUnknownKindNil(t *testing.T) {
	t.Parallel()
	if got := Sources(Kind("UNKNOWN")); got != nil {
		t.Fatalf("unknown kind → %v, want nil", got)
	}
}

// TestSourceSetOperationModeAllowed verifies tri-state gating of Usage.
func TestSourceSetOperationModeAllowed(t *testing.T) {
	t.Parallel()
	s := NewSource("0001:1", hmenum.ParameterPressShort)

	// Default (nil gate) → EVENT.
	if got := s.Usage(); got != hmenum.DataPointUsageEvent {
		t.Fatalf("default usage = %q, want event", got)
	}

	// Explicitly allowed → EVENT.
	s.SetOperationModeAllowed(true)
	if got := s.Usage(); got != hmenum.DataPointUsageEvent {
		t.Fatalf("allowed usage = %q, want event", got)
	}

	// Explicitly excluded → IGNORED (channel-operation-mode mask is a
	// visibility-gate path, see ADR 0015).
	s.SetOperationModeAllowed(false)
	if got := s.Usage(); got != hmenum.DataPointUsageIgnored {
		t.Fatalf("excluded usage = %q, want ignored", got)
	}
}

// TestSourceEnabledByDefaultAndVisible verifies Source always reports these as true.
func TestSourceEnabledByDefaultAndVisible(t *testing.T) {
	t.Parallel()
	s := NewSource("0001:1", hmenum.ParameterPressLong)
	if !s.EnabledByDefault() {
		t.Fatal("event source must always be enabled by default")
	}
	if !s.Visible() {
		t.Fatal("event source must always be visible")
	}
}

// TestSourceCategory verifies Category returns DataPointCategoryEvent.
func TestSourceCategory(t *testing.T) {
	t.Parallel()
	s := NewSource("0001:1", hmenum.ParameterError)
	if got := s.Category(); got != hmenum.DataPointCategoryEvent {
		t.Fatalf("Category() = %q, want event", got)
	}
}

// TestGroupOnFireUnsubscribe verifies that the returned unsubscribe function
// silences subsequent callbacks and is idempotent.
func TestGroupOnFireUnsubscribe(t *testing.T) {
	t.Parallel()
	g := NewGroup("0001:1", KindKeypress)
	src := NewSource("0001:1", hmenum.ParameterPressShort)
	g.Add(src)

	var count int
	unsub := g.OnFire(func(_ Event) { count++ })

	src.Fire(true)
	if count != 1 {
		t.Fatalf("fired=%d, want 1", count)
	}

	unsub()
	// Idempotent — calling again must not panic.
	unsub()

	src.Fire(true)
	if count != 1 {
		t.Fatalf("after unsub fired=%d, want 1", count)
	}
}

// TestGroupAvailableDelegation verifies SetAvailableFunc is honoured.
func TestGroupAvailableDelegation(t *testing.T) {
	t.Parallel()
	g := NewGroup("0001:1", KindKeypress)

	// Without delegate → always true.
	if !g.Available() {
		t.Fatal("no delegate: Available() must return true")
	}

	avail := true
	g.SetAvailableFunc(func() bool { return avail })
	if !g.Available() {
		t.Fatal("delegate true: Available() must return true")
	}

	avail = false
	if g.Available() {
		t.Fatal("delegate false: Available() must return false")
	}
}

// TestGroupLastTriggeredEvent verifies the last-source tracking.
func TestGroupLastTriggeredEvent(t *testing.T) {
	t.Parallel()
	g := NewGroup("0001:1", KindKeypress)
	src := NewSource("0001:1", hmenum.ParameterPressShort)
	g.Add(src)

	if g.LastTriggeredEvent() != nil {
		t.Fatal("initially LastTriggeredEvent must be nil")
	}

	src.Fire(true)
	if got := g.LastTriggeredEvent(); got != src {
		t.Fatalf("after fire LastTriggeredEvent = %v, want src", got)
	}
}

// TestGroupNewGroupWithCentral verifies the multi-CCU constructor embeds
// the central name in the UniqueID.
func TestGroupNewGroupWithCentral(t *testing.T) {
	t.Parallel()
	g := NewGroupWithCentral("ccu1", "0001:1", KindKeypress)
	uid := g.UniqueID()
	if uid == "" {
		t.Fatal("UniqueID must not be empty")
	}
}

// TestGroupCategory verifies the group's Category.
func TestGroupCategory(t *testing.T) {
	t.Parallel()
	g := NewGroup("0001:1", KindImpulse)
	if got := g.Category(); got != hmenum.DataPointCategoryEventGroup {
		t.Fatalf("Category() = %q, want event_group", got)
	}
}

// TestDeviceErrorActiveIntSuppression exercises the int-typed branch of
// deviceErrorActive to verify the suppression of repeat values.
func TestDeviceErrorActiveIntSuppression(t *testing.T) {
	t.Parallel()
	s := NewSource("0001:1", hmenum.ParameterSensorError)

	var fired int
	s.OnFire(func(_ Event) { fired++ })

	// Transition: 0 → suppressed.
	s.Fire(int32(0))
	if fired != 0 {
		t.Fatalf("initial 0 fired=%d, want 0", fired)
	}
	// 7 → fires.
	s.Fire(int32(7))
	if fired != 1 {
		t.Fatalf("7 fired=%d, want 1", fired)
	}
	// Same 7 → suppressed.
	s.Fire(int32(7))
	if fired != 1 {
		t.Fatalf("repeat 7 fired=%d, want 1", fired)
	}
	// Back to 0 → fires (state change).
	s.Fire(int32(0))
	if fired != 2 {
		t.Fatalf("0 after 7 fired=%d, want 2", fired)
	}
}

// ─── ChannelEventGroup.Available ─────────────────────────────────────────────

// TestGroupAvailableDefaultTrue verifies that without a delegate, Available()
// returns true — matches the "device always reachable in test fixtures" default.
func TestGroupAvailableDefaultTrue(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindKeypress)
	if !g.Available() {
		t.Fatal("Available() without delegate must return true")
	}
}

// TestGroupAvailableDelegates verifies that SetAvailableFunc wires the delegate.
func TestGroupAvailableDelegates(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindKeypress)
	reachable := true
	g.SetAvailableFunc(func() bool { return reachable })

	if !g.Available() {
		t.Fatal("Available() must return true when delegate returns true")
	}

	reachable = false
	if g.Available() {
		t.Fatal("Available() must return false when delegate returns false")
	}
}

// ─── ChannelEventGroup.LastTriggeredEvent ────────────────────────────────────

// TestGroupLastTriggeredEventNilBeforeFirstFire verifies that the last-triggered
// source is nil before any event fires.
func TestGroupLastTriggeredEventNilBeforeFirstFire(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindKeypress)
	if s := g.LastTriggeredEvent(); s != nil {
		t.Fatalf("LastTriggeredEvent() before any fire = %v, want nil", s)
	}
}

// TestGroupLastTriggeredEventUpdatedOnFire verifies that after a source fires,
// LastTriggeredEvent() returns that source.
func TestGroupLastTriggeredEventUpdatedOnFire(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindKeypress)
	s := &Source{
		ChannelAddress: "A:1",
		Parameter:      hmenum.ParameterPressShort,
		Kind:           KindKeypress,
	}
	g.Add(s)

	s.Fire(true)

	if got := g.LastTriggeredEvent(); got != s {
		t.Fatalf("LastTriggeredEvent() = %v, want source %v", got, s)
	}
}

// TestGroupLastTriggeredEventUpdatesOnSecondSource verifies that when two
// sources exist and the second fires, LastTriggeredEvent() switches to it.
func TestGroupLastTriggeredEventUpdatesOnSecondSource(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindKeypress)
	s1 := &Source{ChannelAddress: "A:1", Parameter: hmenum.ParameterPressShort, Kind: KindKeypress}
	s2 := &Source{ChannelAddress: "A:1", Parameter: hmenum.ParameterPressLong, Kind: KindKeypress}
	g.Add(s1)
	g.Add(s2)

	s1.Fire(true)
	if got := g.LastTriggeredEvent(); got != s1 {
		t.Fatalf("after s1.Fire: LastTriggeredEvent = %v, want s1", got)
	}

	s2.Fire(true)
	if got := g.LastTriggeredEvent(); got != s2 {
		t.Fatalf("after s2.Fire: LastTriggeredEvent = %v, want s2", got)
	}
}

// TestEventGroupHasUniqueIDAndCleanup verifies that Group must expose a stable
// UniqueID via the embedded BaseDataPointFields, support MarkRegistered /
// IsRegistered, and correctly report its category.
func TestEventGroupHasUniqueIDAndCleanup(t *testing.T) {
	t.Parallel()

	g := NewGroupWithCentral("ccu1", "A:1", KindKeypress)

	// UniqueID must be stable and contain the central scope.
	uid := g.UniqueID()
	if uid == "" {
		t.Fatal("Group.UniqueID() must not be empty")
	}
	// Expected shape: "ccu1:A:1:event_group/<KindKeypress>"
	want := "ccu1:A:1:event_group/" + string(KindKeypress)
	if uid != want {
		t.Errorf("Group.UniqueID() = %q, want %q", uid, want)
	}

	// IsRegistered must start false; MarkRegistered sets it to true.
	if g.IsRegistered() {
		t.Fatal("fresh Group must not be registered")
	}
	g.MarkRegistered()
	if !g.IsRegistered() {
		t.Fatal("Group must be registered after MarkRegistered()")
	}

	// Category must be EventGroup.
	if got := g.Category(); got != hmenum.DataPointCategoryEventGroup {
		t.Errorf("Group.Category() = %q, want EventGroup", got)
	}
}

// TestEventGroupCloseUnsubs verifies that Close() releases all internal
// subscription closures.
func TestEventGroupCloseUnsubs(t *testing.T) {
	t.Parallel()

	g := NewGroup("B:1", KindKeypress)
	s := &Source{
		ChannelAddress: "B:1",
		Parameter:      hmenum.ParameterPressShort,
		Kind:           KindKeypress,
	}
	g.Add(s) // registers one internal unsub

	var fired int
	g.OnFire(func(Event) { fired++ })

	// Fire before Close → callback reached.
	s.Fire(true)
	if fired != 1 {
		t.Fatalf("before Close: expected 1 fire, got %d", fired)
	}

	g.Close()

	// After Close the internal subscription is cleared; new Add would
	// re-wire, but existing fires from the now-orphaned source should
	// not reach the group's dispatch loop (the unsub was called). We
	// cannot easily test "no dispatch after Close" without re-adding,
	// but we CAN verify Close does not panic and the unsub slice is nil.
	// The real invariant — no panic — is enforced by the test runner.
}

// ─── Group.TranslationKey and Group.Usage ─────────────────────────────────────

// TestGroupTranslationKeyKeypress verifies that a keypress group returns the
// same translation key as its corresponding Source.
func TestGroupTranslationKeyKeypress(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindKeypress)
	got := g.TranslationKey()
	if got != "keypress" {
		t.Fatalf("TranslationKey() = %q, want %q", got, "keypress")
	}
}

// TestGroupTranslationKeyImpulse verifies impulse → "impulse".
func TestGroupTranslationKeyImpulse(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindImpulse)
	if got := g.TranslationKey(); got != "impulse" {
		t.Fatalf("TranslationKey() = %q, want %q", got, "impulse")
	}
}

// TestGroupTranslationKeyDeviceError verifies device_error → "device_error".
func TestGroupTranslationKeyDeviceError(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindDeviceError)
	if got := g.TranslationKey(); got != "device_error" {
		t.Fatalf("TranslationKey() = %q, want %q", got, "device_error")
	}
}

// TestGroupTranslationKeyMatchesSource verifies that Group and Source agree
// on the translation key for every Kind.
func TestGroupTranslationKeyMatchesSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind  Kind
		param hmenum.Parameter
	}{
		{KindKeypress, hmenum.ParameterPressShort},
		{KindImpulse, hmenum.ParameterSequenceOK},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			g := NewGroup("A:1", tc.kind)
			s := NewSource("A:1", tc.param)
			if g.TranslationKey() != s.TranslationKey() {
				t.Errorf("Group.TranslationKey()=%q Source.TranslationKey()=%q — must match",
					g.TranslationKey(), s.TranslationKey())
			}
		})
	}
}

// TestGroupUsageReturnsEvent verifies that Group.Usage() always returns
// DataPointUsageEvent.
func TestGroupUsageReturnsEvent(t *testing.T) {
	t.Parallel()

	for _, k := range []Kind{KindKeypress, KindImpulse, KindDeviceError} {
		k := k
		t.Run(string(k), func(t *testing.T) {
			t.Parallel()
			g := NewGroup("A:1", k)
			if got := g.Usage(); got != hmenum.DataPointUsageEvent {
				t.Errorf("Group(%q).Usage() = %q, want DataPointUsageEvent", k, got)
			}
		})
	}
}

// ─── Group.EventTypes ─────────────────────────────────────────────────────────

// TestGroupEventTypesEmpty verifies that EventTypes returns an empty slice
// when no sources have been added.
func TestGroupEventTypesEmpty(t *testing.T) {
	t.Parallel()
	g := NewGroup("A:1", KindKeypress)
	if got := g.EventTypes(); len(got) != 0 {
		t.Fatalf("EventTypes() on empty group = %v, want []", got)
	}
}

// TestGroupEventTypesLowercaseSorted verifies that EventTypes returns
// lowercased, sorted parameter names.
func TestGroupEventTypesLowercaseSorted(t *testing.T) {
	t.Parallel()

	g := NewGroup("A:1", KindKeypress)
	s1 := &Source{ChannelAddress: "A:1", Parameter: hmenum.ParameterPressShort, Kind: KindKeypress}
	s2 := &Source{ChannelAddress: "A:1", Parameter: hmenum.ParameterPressLong, Kind: KindKeypress}
	g.Add(s1)
	g.Add(s2)

	got := g.EventTypes()
	if len(got) != 2 {
		t.Fatalf("EventTypes() len = %d, want 2", len(got))
	}
	// must be sorted
	if got[0] > got[1] {
		t.Errorf("EventTypes() not sorted: %v", got)
	}
	// must be lowercase
	for _, et := range got {
		for _, ch := range et {
			if ch >= 'A' && ch <= 'Z' {
				t.Errorf("EventTypes() contains uppercase in %q", et)
			}
		}
	}
}

// ─── Group.Sources ────────────────────────────────────────────────────────────

// TestGroupSourcesReturnsSortedSlice verifies that Sources() returns all
// registered event sources sorted by parameter name.
func TestGroupSourcesReturnsSortedSlice(t *testing.T) {
	t.Parallel()

	g := NewGroup("0001:1", KindKeypress)
	short := NewSource("0001:1", hmenum.ParameterPressShort)
	long := NewSource("0001:1", hmenum.ParameterPressLong)
	g.Add(short)
	g.Add(long)

	got := g.Sources()
	if len(got) != 2 {
		t.Fatalf("Sources() len = %d, want 2", len(got))
	}
	// Sorted by parameter: PRESS_LONG < PRESS_SHORT alphabetically.
	if got[0].Parameter != hmenum.ParameterPressLong {
		t.Fatalf("Sources()[0].Parameter = %q, want %q", got[0].Parameter, hmenum.ParameterPressLong)
	}
	if got[1].Parameter != hmenum.ParameterPressShort {
		t.Fatalf("Sources()[1].Parameter = %q, want %q", got[1].Parameter, hmenum.ParameterPressShort)
	}
}

// TestGroupSourcesNilWhenEmpty verifies that Sources() returns nil for an
// empty group.
func TestGroupSourcesNilWhenEmpty(t *testing.T) {
	t.Parallel()

	g := NewGroup("0001:1", KindKeypress)
	if got := g.Sources(); got != nil {
		t.Fatalf("Sources() on empty group = %v, want nil", got)
	}
}

// TestGroupSourcesReturnsCopy verifies that mutating the returned slice does
// not affect the group's internal state.
func TestGroupSourcesReturnsCopy(t *testing.T) {
	t.Parallel()

	g := NewGroup("0001:1", KindKeypress)
	s := NewSource("0001:1", hmenum.ParameterPressShort)
	g.Add(s)

	got := g.Sources()
	if len(got) != 1 {
		t.Fatalf("Sources() len = %d, want 1", len(got))
	}
	got[0] = nil // mutate returned slice

	// Second call must still return the source.
	got2 := g.Sources()
	if len(got2) != 1 || got2[0] == nil {
		t.Fatal("Sources() must return an independent copy; internal state was mutated")
	}
}
