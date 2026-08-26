// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// weekprofile_deep_test.go covers the Profile[T] generic wrapper and the
// underlying schedule model (Climate / Simple) in depth. It is
// deliberately placed in the weekprofile package so it can use both
// packages together via their public APIs.
//
// Tests are grouped:
//
// Cluster A — Profile construction and basic lifecycle
// Cluster B — OnChange subscription mechanics
// Cluster C — Concurrent safety
// Cluster D — ClimateProfile / ClimateWeekday / ClimatePeriod semantics
// Cluster E — Simple schedule semantics
// Cluster F — Cross-day / multi-profile Climate
// Cluster G — Edge-cases and boundary values

package weekprofile_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// Shared test stubs
// ---------------------------------------------------------------------------

type climateLoader struct {
	value *schedule.Climate
	err   error
}

func (l *climateLoader) Load(_ context.Context) (*schedule.Climate, error) {
	return l.value, l.err
}

type climateSaver struct {
	mu   sync.Mutex
	last *schedule.Climate
	err  error
}

func (s *climateSaver) Save(_ context.Context, v *schedule.Climate) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	s.last = v
	s.mu.Unlock()
	return nil
}

func (s *climateSaver) Last() *schedule.Climate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

type simpleLoader struct {
	value *schedule.Simple
	err   error
}

func (l *simpleLoader) Load(_ context.Context) (*schedule.Simple, error) {
	return l.value, l.err
}

// simpleSaver is reserved for future Simple-profile Save tests.
// Currently the deep-test surface only exercises Load on the Simple
// path; saving is covered by ClimateProfile tests.
//
//nolint:unused // future helper
type simpleSaver struct {
	mu   sync.Mutex
	last *schedule.Simple
	err  error
}

//nolint:unused // future helper
func (s *simpleSaver) Save(_ context.Context, v *schedule.Simple) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	s.last = v
	s.mu.Unlock()
	return nil
}

func minutesToHHMM(m int) string {
	if m == 24*60 {
		return "24:00"
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// ---------------------------------------------------------------------------
// Cluster A — Profile construction and basic lifecycle
// ---------------------------------------------------------------------------

// TestProfileCurrentBeforeLoadReturnsErrNotLoaded asserts that calling
// Current() before any Load/Save produces ErrNotLoaded for both profile types.
func TestProfileCurrentBeforeLoadReturnsErrNotLoaded(t *testing.T) {
	t.Parallel()

	t.Run("Climate", func(t *testing.T) {
		t.Parallel()
		p := weekprofile.NewClimate(nil, nil)
		_, err := p.Current()
		if !errors.Is(err, weekprofile.ErrNotLoaded) {
			t.Fatalf("got %v, want ErrNotLoaded", err)
		}
	})

	t.Run("Default", func(t *testing.T) {
		t.Parallel()
		p := weekprofile.NewDefault(nil, nil)
		_, err := p.Current()
		if !errors.Is(err, weekprofile.ErrNotLoaded) {
			t.Fatalf("got %v, want ErrNotLoaded", err)
		}
	})
}

// TestProfileLoadSetsCurrentAndReturnsSameValue asserts Load() makes the
// schedule retrievable via Current() and returns the exact same pointer.
func TestProfileLoadSetsCurrentAndReturnsSameValue(t *testing.T) {
	t.Parallel()

	clim := schedule.NewClimate()
	loader := &climateLoader{value: clim}
	p := weekprofile.NewClimate(loader, nil)

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != clim {
		t.Fatal("Load must return the loader's value")
	}
	cur, err := p.Current()
	if err != nil {
		t.Fatalf("Current after load: %v", err)
	}
	if cur != clim {
		t.Fatal("Current must reflect the loaded value")
	}
}

// TestProfileLoadErrorLeavesNotLoaded asserts that a loader error does not
// publish a value and Current() still returns ErrNotLoaded.
func TestProfileLoadErrorLeavesNotLoaded(t *testing.T) {
	t.Parallel()

	boom := errors.New("ccu unreachable")
	loader := &climateLoader{err: boom}
	p := weekprofile.NewClimate(loader, nil)

	var fired int
	p.OnChange(func(_, _ *schedule.Climate) { fired++ })

	_, err := p.Load(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Load error: got %v, want %v", err, boom)
	}
	_, cur := p.Current()
	if !errors.Is(cur, weekprofile.ErrNotLoaded) {
		t.Fatalf("Current after load error: got %v, want ErrNotLoaded", cur)
	}
	if fired != 0 {
		t.Fatalf("OnChange fired %d times on error, want 0", fired)
	}
}

// TestProfileLoadWithoutLoaderReturnsError verifies a Profile with loader=nil
// returns a descriptive error rather than panicking.
func TestProfileLoadWithoutLoaderReturnsError(t *testing.T) {
	t.Parallel()

	p := weekprofile.NewClimate(nil, nil)
	_, err := p.Load(context.Background())
	if err == nil {
		t.Fatal("expected error when loader is nil")
	}
}

// TestProfileSaveWithoutSaverReturnsError verifies a Profile with saver=nil
// returns a descriptive error rather than panicking.
func TestProfileSaveWithoutSaverReturnsError(t *testing.T) {
	t.Parallel()

	p := weekprofile.NewClimate(nil, nil)
	err := p.Save(context.Background(), schedule.NewClimate())
	if err == nil {
		t.Fatal("expected error when saver is nil")
	}
}

// TestProfileSaveUpdatesCurrent asserts Save() updates Current() and invokes
// OnChange even without a prior Load().
func TestProfileSaveUpdatesCurrent(t *testing.T) {
	t.Parallel()

	saver := &climateSaver{}
	p := weekprofile.NewClimate(nil, saver)

	var prev, next *schedule.Climate
	p.OnChange(func(p2, n *schedule.Climate) { prev, next = p2, n })

	clim := schedule.NewClimate()
	if err := p.Save(context.Background(), clim); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cur, err := p.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if cur != clim {
		t.Fatal("Current must reflect saved value")
	}
	if next != clim {
		t.Fatalf("OnChange next=%v, want %v", next, clim)
	}
	// On first save, prev must be nil (zero value for *schedule.Climate).
	if prev != nil {
		t.Fatalf("OnChange prev=%v, want nil on first save", prev)
	}
}

// TestProfileSaveErrorDoesNotUpdateCurrent asserts that a saver error leaves
// the previous state intact and does not invoke OnChange.
func TestProfileSaveErrorDoesNotUpdateCurrent(t *testing.T) {
	t.Parallel()

	// Pre-load a good schedule so we have a baseline Current.
	good := schedule.NewClimate()
	saver := &climateSaver{}
	p := weekprofile.NewClimate(&climateLoader{value: good}, saver)
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	saver.err = errors.New("storage full")
	var fired int
	p.OnChange(func(_, _ *schedule.Climate) { fired++ })

	bad := schedule.NewClimate()
	if err := p.Save(context.Background(), bad); err == nil {
		t.Fatal("expected error from saver")
	}

	cur, _ := p.Current()
	if cur != good {
		t.Fatal("Current must remain unchanged after Save error")
	}
	if fired != 0 {
		t.Fatalf("OnChange fired %d times on error, want 0", fired)
	}
}

// TestProfileLoadThenSaveSequencePreservesOrder ensures that two consecutive
// Load + Save calls result in the most recently published value.
func TestProfileLoadThenSaveSequencePreservesOrder(t *testing.T) {
	t.Parallel()

	v1 := schedule.NewClimate()
	v2 := schedule.NewClimate()
	saver := &climateSaver{}
	p := weekprofile.NewClimate(&climateLoader{value: v1}, saver)

	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load v1: %v", err)
	}
	if err := p.Save(context.Background(), v2); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	cur, _ := p.Current()
	if cur != v2 {
		t.Fatal("Current must return the most recently published value")
	}
}

// ---------------------------------------------------------------------------
// Cluster B — OnChange subscription mechanics
// ---------------------------------------------------------------------------

// TestOnChangeCallbackReceivesPrevAndNext verifies the prev/next arguments
// are correctly threaded: first call prev is zero, subsequent call prev equals
// the previous value.
func TestOnChangeCallbackReceivesPrevAndNext(t *testing.T) {
	t.Parallel()

	saver := &climateSaver{}
	p := weekprofile.NewClimate(nil, saver)

	var calls []struct{ prev, next *schedule.Climate }
	p.OnChange(func(prev, next *schedule.Climate) {
		calls = append(calls, struct{ prev, next *schedule.Climate }{prev, next})
	})

	v1 := schedule.NewClimate()
	v2 := schedule.NewClimate()

	if err := p.Save(context.Background(), v1); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	if err := p.Save(context.Background(), v2); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("got %d OnChange calls, want 2", len(calls))
	}
	if calls[0].prev != nil {
		t.Fatalf("first call prev=%v, want nil", calls[0].prev)
	}
	if calls[0].next != v1 {
		t.Fatalf("first call next=%v, want v1", calls[0].next)
	}
	if calls[1].prev != v1 {
		t.Fatalf("second call prev=%v, want v1", calls[1].prev)
	}
	if calls[1].next != v2 {
		t.Fatalf("second call next=%v, want v2", calls[1].next)
	}
}

// TestOnChangeMultipleSubscribersAllFire confirms that two independent
// subscribers both receive the event.
func TestOnChangeMultipleSubscribersAllFire(t *testing.T) {
	t.Parallel()

	p := weekprofile.NewClimate(nil, &climateSaver{})
	var count1, count2 atomic.Int32
	p.OnChange(func(_, _ *schedule.Climate) { count1.Add(1) })
	p.OnChange(func(_, _ *schedule.Climate) { count2.Add(1) })

	_ = p.Save(context.Background(), schedule.NewClimate())

	if count1.Load() != 1 || count2.Load() != 1 {
		t.Fatalf("count1=%d count2=%d, both want 1", count1.Load(), count2.Load())
	}
}

// TestOnChangeUnsubscribeStopsFiring asserts that the closure returned by
// OnChange stops the subscriber from receiving further events.
func TestOnChangeUnsubscribeStopsFiring(t *testing.T) {
	t.Parallel()

	p := weekprofile.NewClimate(nil, &climateSaver{})
	var count atomic.Int32
	unsub := p.OnChange(func(_, _ *schedule.Climate) { count.Add(1) })

	_ = p.Save(context.Background(), schedule.NewClimate())
	if count.Load() != 1 {
		t.Fatalf("before unsub: count=%d, want 1", count.Load())
	}

	unsub()
	_ = p.Save(context.Background(), schedule.NewClimate())
	if count.Load() != 1 {
		t.Fatalf("after unsub: count=%d, want 1 (no extra fire)", count.Load())
	}
}

// TestOnChangeUnsubscribeIsIdempotent asserts that calling the unsubscribe
// closure multiple times does not panic or produce extra effects.
func TestOnChangeUnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()

	p := weekprofile.NewClimate(nil, &climateSaver{})
	unsub := p.OnChange(func(_, _ *schedule.Climate) {})

	// Three calls — must be a no-op after the first.
	unsub()
	unsub()
	unsub()

	// Saving after idempotent-unsubscribe must not panic.
	if err := p.Save(context.Background(), schedule.NewClimate()); err != nil {
		t.Fatalf("Save after repeated unsub: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cluster C — Concurrent safety
// ---------------------------------------------------------------------------

// TestProfileConcurrentReadIsSafe fans out many goroutines reading Current()
// while Save() is running concurrently. Detects data races under -race.
func TestProfileConcurrentReadIsSafe(t *testing.T) {
	t.Parallel()

	saver := &climateSaver{}
	p := weekprofile.NewClimate(nil, saver)
	// Seed an initial value so readers don't get ErrNotLoaded every time.
	_ = p.Save(context.Background(), schedule.NewClimate())

	var wg sync.WaitGroup
	const readers = 50

	// Writer goroutine.
	wg.Go(func() {
		for range 20 {
			_ = p.Save(context.Background(), schedule.NewClimate())
		}
	})

	// Reader goroutines.
	for range readers {
		wg.Go(func() {
			for range 20 {
				_, _ = p.Current()
			}
		})
	}

	wg.Wait()
}

// TestProfileConcurrentOnChangeSafe registers OnChange from one goroutine
// while Saves happen from another. Must not race.
func TestProfileConcurrentOnChangeSafe(t *testing.T) {
	t.Parallel()

	p := weekprofile.NewClimate(nil, &climateSaver{})
	var wg sync.WaitGroup

	wg.Go(func() {
		for range 10 {
			_ = p.Save(context.Background(), schedule.NewClimate())
		}
	})

	wg.Go(func() {
		for range 10 {
			unsub := p.OnChange(func(_, _ *schedule.Climate) {})
			unsub()
		}
	})

	wg.Wait()
}

// ---------------------------------------------------------------------------
// Cluster D — ClimateProfile / ClimateWeekday / ClimatePeriod semantics
// ---------------------------------------------------------------------------

// TestClimatePeriod24HourEndTimeIsAccepted asserts that "24:00" is a valid
// end-time (required for the last slot of every day).
func TestClimatePeriod24HourEndTimeIsAccepted(t *testing.T) {
	t.Parallel()

	p := schedule.ClimatePeriod{StartTime: "22:00", EndTime: "24:00", Temperature: 18}
	if err := p.Validate(); err != nil {
		t.Fatalf("24:00 end-time must be accepted: %v", err)
	}
}

// TestClimatePeriodEndBeforeStartFails rejects periods where end ≤ start.
func TestClimatePeriodEndBeforeStartFails(t *testing.T) {
	t.Parallel()

	cases := []schedule.ClimatePeriod{
		{StartTime: "10:00", EndTime: "09:00", Temperature: 21},
		{StartTime: "08:00", EndTime: "08:00", Temperature: 21}, // equal
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("expected error for %s..%s", c.StartTime, c.EndTime)
		}
	}
}

// TestClimateWeekdayFullDaySingleSlotIsValid verifies an all-day single
// period (00:00–24:00) validates correctly.
func TestClimateWeekdayFullDaySingleSlotIsValid(t *testing.T) {
	t.Parallel()

	d := schedule.ClimateWeekday{
		BaseTemperature: 21,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "24:00", Temperature: 21},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("all-day single slot must validate: %v", err)
	}
}

// TestClimateWeekdayEmptyPeriodsIsValid verifies that a day with no periods
// (base-temperature-only) is a valid representation.
func TestClimateWeekdayEmptyPeriodsIsValid(t *testing.T) {
	t.Parallel()

	d := schedule.ClimateWeekday{BaseTemperature: 18}
	if err := d.Validate(); err != nil {
		t.Fatalf("empty-periods weekday must be valid: %v", err)
	}
}

// TestClimateWeekdayMaxPeriodsAccepted verifies that exactly MaxClimatePeriods
// (13) slots — spanning 00:00 to 24:00 — validate without error.
func TestClimateWeekdayMaxPeriodsAccepted(t *testing.T) {
	t.Parallel()

	// Thirteen equal slots do not divide 1440 minutes, so the layout is
	// built by hand — 12 one-hour slots plus a final twelve-hour one — to
	// exercise the exact MaxClimatePeriods = 13 cap with valid 24-hour
	// coverage.
	periods := make([]schedule.ClimatePeriod, 13)
	for i := range 12 {
		periods[i] = schedule.ClimatePeriod{
			StartTime:   minutesToHHMM(i * 60),
			EndTime:     minutesToHHMM((i + 1) * 60),
			Temperature: 20,
		}
	}
	periods[12] = schedule.ClimatePeriod{
		StartTime:   minutesToHHMM(12 * 60),
		EndTime:     "24:00",
		Temperature: 20,
	}
	d := schedule.ClimateWeekday{BaseTemperature: 20, Periods: periods}
	if err := d.Validate(); err != nil {
		t.Fatalf("13 contiguous periods must validate: %v", err)
	}
}

// TestClimateWeekdayExceedsMaxPeriodsRejected asserts more than
// MaxClimatePeriods slots are rejected.
func TestClimateWeekdayExceedsMaxPeriodsRejected(t *testing.T) {
	t.Parallel()

	// 14 one-hour periods (not contiguous, but that's OK — count check runs
	// before the coverage check in production).
	periods := make([]schedule.ClimatePeriod, 14)
	for i := range 14 {
		periods[i] = schedule.ClimatePeriod{
			StartTime:   minutesToHHMM(i * 60),
			EndTime:     minutesToHHMM((i + 1) * 60),
			Temperature: 20,
		}
	}
	// Adjust last to reach 24:00
	periods[13].StartTime = minutesToHHMM(13 * 60)
	periods[13].EndTime = "24:00"
	d := schedule.ClimateWeekday{BaseTemperature: 20, Periods: periods}
	if err := d.Validate(); err == nil {
		t.Fatalf("14 periods must be rejected (limit %d)", schedule.MaxClimatePeriods)
	}
}

// TestClimateWeekdayOverlappingPeriodsRejected asserts overlapping time
// windows in a weekday are detected.
func TestClimateWeekdayOverlappingPeriodsRejected(t *testing.T) {
	t.Parallel()

	d := schedule.ClimateWeekday{
		BaseTemperature: 21,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "12:00", Temperature: 18},
			{StartTime: "10:00", EndTime: "24:00", Temperature: 21},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("overlapping periods must be rejected")
	}
}

// TestClimateWeekdayGapBetweenPeriodsRejected asserts a gap between
// consecutive periods (i.e. non-contiguous coverage) is an error.
func TestClimateWeekdayGapBetweenPeriodsRejected(t *testing.T) {
	t.Parallel()

	d := schedule.ClimateWeekday{
		BaseTemperature: 20,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "08:00", Temperature: 18},
			// gap: 08:00 → 10:00
			{StartTime: "10:00", EndTime: "24:00", Temperature: 21},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("gap between periods must be rejected")
	}
}

// TestClimateWeekdayFirstPeriodMustStartAtMidnight asserts that the coverage
// rule requires the first period to begin at 00:00.
func TestClimateWeekdayFirstPeriodMustStartAtMidnight(t *testing.T) {
	t.Parallel()

	d := schedule.ClimateWeekday{
		BaseTemperature: 20,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "01:00", EndTime: "24:00", Temperature: 21},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("first period not at 00:00 must fail")
	}
}

// TestClimateWeekdayLastPeriodMustEndAt2400 asserts the coverage rule
// requires the last period to end at 24:00.
func TestClimateWeekdayLastPeriodMustEndAt2400(t *testing.T) {
	t.Parallel()

	d := schedule.ClimateWeekday{
		BaseTemperature: 20,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "20:00", Temperature: 21},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("last period not ending at 24:00 must fail")
	}
}

// TestClimateProfileInvalidWeekdayRejected verifies that an unknown weekday
// string is rejected when calling Put().
func TestClimateProfileInvalidWeekdayRejected(t *testing.T) {
	t.Parallel()

	p := schedule.NewClimateProfile()
	if err := p.Put("FUNDAY", schedule.ClimateWeekday{BaseTemperature: 20}); err == nil {
		t.Fatal("invalid weekday must be rejected")
	}
}

// TestClimateProfileAllSevenDays verifies that all seven Weekday values are
// valid keys and can be stored.
func TestClimateProfileAllSevenDays(t *testing.T) {
	t.Parallel()

	p := schedule.NewClimateProfile()
	for _, day := range schedule.Weekdays {
		if err := p.Put(day, schedule.ClimateWeekday{BaseTemperature: 20}); err != nil {
			t.Errorf("weekday %s must be accepted: %v", day, err)
		}
	}
	if len(p.Days) != 7 {
		t.Fatalf("expected 7 days in profile, got %d", len(p.Days))
	}
}

// TestClimateProfilePutOverwritesExistingDay verifies that Put() on an
// already-stored weekday replaces the old entry.
func TestClimateProfilePutOverwritesExistingDay(t *testing.T) {
	t.Parallel()

	p := schedule.NewClimateProfile()
	d1 := schedule.ClimateWeekday{BaseTemperature: 18}
	d2 := schedule.ClimateWeekday{BaseTemperature: 22}

	_ = p.Put(schedule.WeekdayMonday, d1)
	_ = p.Put(schedule.WeekdayMonday, d2)

	stored := p.Days[schedule.WeekdayMonday]
	if stored.BaseTemperature != d2.BaseTemperature {
		t.Fatalf("Put did not overwrite: got %.1f, want %.1f",
			stored.BaseTemperature, d2.BaseTemperature)
	}
}

// ---------------------------------------------------------------------------
// Cluster E — Simple schedule semantics
// ---------------------------------------------------------------------------

// TestSimpleScheduleSlotRangeEnforced asserts that slots outside 1..24 are
// rejected.
func TestSimpleScheduleSlotRangeEnforced(t *testing.T) {
	t.Parallel()

	s := schedule.NewSimple()
	e := schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "08:00",
		Level:    1,
	}
	if err := s.Put(0, e); err == nil {
		t.Fatal("slot 0 must be rejected")
	}
	if err := s.Put(schedule.SimpleMaxSlot+1, e); err == nil {
		t.Fatalf("slot %d must be rejected", schedule.SimpleMaxSlot+1)
	}
	if err := s.Put(1, e); err != nil {
		t.Fatalf("slot 1 must be accepted: %v", err)
	}
	// 25 used to be the first rejected slot. The CCU declares up to 75 on
	// a switch/dimmer/blind channel and edits all of them, so every slot
	// in that range has to be storable.
	if err := s.Put(25, e); err != nil {
		t.Fatalf("slot 25 must be accepted: %v", err)
	}
	if err := s.Put(schedule.SimpleMaxSlot, e); err != nil {
		t.Fatalf("slot %d must be accepted: %v", schedule.SimpleMaxSlot, err)
	}
}

// TestSimpleScheduleSlotsReturnsSortedKeys asserts that Slots() always
// returns keys in ascending order regardless of insertion order.
func TestSimpleScheduleSlotsReturnsSortedKeys(t *testing.T) {
	t.Parallel()

	s := schedule.NewSimple()
	e := func(day schedule.Weekday) schedule.SimpleEntry {
		return schedule.SimpleEntry{Weekdays: []schedule.Weekday{day}, Time: "09:00", Level: 1}
	}

	// Insert in non-monotonic order.
	insertOrder := []int{5, 1, 3, 2, 4}
	for _, slot := range insertOrder {
		if err := s.Put(slot, e(schedule.WeekdayFriday)); err != nil {
			t.Fatalf("Put(%d): %v", slot, err)
		}
	}

	slots := s.Slots()
	for i := 1; i < len(slots); i++ {
		if slots[i] <= slots[i-1] {
			t.Fatalf("Slots not sorted: %v", slots)
		}
	}
}

// TestSimpleScheduleValidateForSwitch enforces that switch entries must use
// level 0 or 1.
func TestSimpleScheduleValidateForSwitch(t *testing.T) {
	t.Parallel()

	s := schedule.NewSimple()
	_ = s.Put(1, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "07:00",
		Level:    0.5, // illegal for switch
	})
	if err := s.ValidateAll(hmenum.DataPointCategorySwitch); err == nil {
		t.Fatal("level 0.5 for switch must be rejected")
	}

	s2 := schedule.NewSimple()
	_ = s2.Put(1, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "07:00",
		Level:    1,
	})
	if err := s2.ValidateAll(hmenum.DataPointCategorySwitch); err != nil {
		t.Fatalf("level 1 for switch must be accepted: %v", err)
	}
}

// TestSimpleScheduleValidateForLight passes non-binary levels.
func TestSimpleScheduleValidateForLight(t *testing.T) {
	t.Parallel()

	s := schedule.NewSimple()
	_ = s.Put(1, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayTuesday},
		Time:     "08:00",
		Level:    0.5,
	})
	if err := s.ValidateAll(hmenum.DataPointCategoryLight); err != nil {
		t.Fatalf("dimmer at 0.5 must be valid for light: %v", err)
	}
}

// TestSimpleEntryMultipleWeekdaysValidate confirms an entry that spans
// several weekdays validates correctly.
func TestSimpleEntryMultipleWeekdaysValidate(t *testing.T) {
	t.Parallel()

	e := schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{
			schedule.WeekdayMonday,
			schedule.WeekdayWednesday,
			schedule.WeekdayFriday,
		},
		Time:  "07:30",
		Level: 1,
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("multi-day entry must validate: %v", err)
	}
}

// TestSimpleEntryInvalidTimeRejected asserts "25:00" and other malformed
// times are rejected. Note: the production regex allows single-digit hours
// (e.g. "8:00") and rejects "24:00" for non-climate entries (valid only for
// climate end-times). The test only uses unambiguously bad values.
func TestSimpleEntryInvalidTimeRejected(t *testing.T) {
	t.Parallel()

	cases := []string{"25:00", "24:00", "ab:cd", "", "08:60"}
	for _, tc := range cases {
		e := schedule.SimpleEntry{
			Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
			Time:     tc,
			Level:    1,
		}
		if err := e.Validate(); err == nil {
			t.Errorf("time %q should be rejected", tc)
		}
	}
}

// TestSimpleEntryAstroOffsetRangEnforced verifies that astro offsets outside
// -720..+720 are rejected.
func TestSimpleEntryAstroOffsetRangEnforced(t *testing.T) {
	t.Parallel()

	base := schedule.SimpleEntry{
		Weekdays:  []schedule.Weekday{schedule.WeekdayMonday},
		Time:      "08:00",
		Condition: schedule.ConditionAstro,
		AstroType: schedule.AstroSunrise,
		Level:     1,
	}

	base.AstroOffsetMinutes = -721
	if err := base.Validate(); err == nil {
		t.Fatal("-721 astro offset must be rejected")
	}
	base.AstroOffsetMinutes = 721
	if err := base.Validate(); err == nil {
		t.Fatal("+721 astro offset must be rejected")
	}
	base.AstroOffsetMinutes = 0
	if err := base.Validate(); err != nil {
		t.Fatalf("0 astro offset must be valid: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cluster F — Cross-day / multi-profile Climate
// ---------------------------------------------------------------------------

// TestClimateMultipleProfileKeys verifies that Climate can hold up to 6
// profiles (P1..P6) and Keys() returns them in sorted order.
func TestClimateMultipleProfileKeys(t *testing.T) {
	t.Parallel()

	c := schedule.NewClimate()
	for _, k := range []string{"P3", "P1", "P6", "P2"} {
		if err := c.Put(k, schedule.NewClimateProfile()); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	keys := c.Keys()
	if len(keys) != 4 {
		t.Fatalf("Keys()=%v, want 4 keys", keys)
	}
	// Sorted check.
	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Fatalf("Keys not sorted: %v", keys)
		}
	}
}

// TestClimateRejectsInvalidProfileKeys asserts profile keys outside P1..P6
// are rejected.
func TestClimateRejectsInvalidProfileKeys(t *testing.T) {
	t.Parallel()

	c := schedule.NewClimate()
	for _, bad := range []string{"P0", "P7", "X1", "1", "", "p1"} {
		if err := c.Put(bad, schedule.NewClimateProfile()); err == nil {
			t.Errorf("profile key %q must be rejected", bad)
		}
	}
}

// TestClimateProfileValidateAllDays runs full validation including periods on
// a profile containing all seven days with valid full-day schedules.
func TestClimateProfileValidateAllDays(t *testing.T) {
	t.Parallel()

	prof := schedule.NewClimateProfile()
	for _, day := range schedule.Weekdays {
		d := schedule.ClimateWeekday{
			BaseTemperature: 20,
			Periods: []schedule.ClimatePeriod{
				{StartTime: "00:00", EndTime: "08:00", Temperature: 18},
				{StartTime: "08:00", EndTime: "22:00", Temperature: 21},
				{StartTime: "22:00", EndTime: "24:00", Temperature: 18},
			},
		}
		if err := prof.Put(day, d); err != nil {
			t.Fatalf("Put(%s): %v", day, err)
		}
	}
	if err := prof.Validate(); err != nil {
		t.Fatalf("full 7-day profile must validate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cluster G — Edge-cases and boundary values
// ---------------------------------------------------------------------------

// TestClimateProfileRoundtripViaProfile exercises the ClimateProfile round-
// trip through the weekprofile.Profile[T] wrapper: construct → Save → Load
// must yield an identical (pointer-equal) schedule.
func TestClimateProfileRoundtripViaProfile(t *testing.T) {
	t.Parallel()

	clim := schedule.NewClimate()
	if err := clim.Put("P1", schedule.NewClimateProfile()); err != nil {
		t.Fatalf("Climate.Put: %v", err)
	}

	saver := &climateSaver{}
	loader := &climateLoader{}

	// Save via profile.
	p := weekprofile.NewClimate(&climateLoader{value: clim}, saver)
	if err := p.Save(context.Background(), clim); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate reload: point loader at what was saved.
	loader.value = saver.Last()

	p2 := weekprofile.NewClimate(loader, nil)
	got, err := p2.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Pointer equality: the loader stub just echoes what was stored.
	if got != saver.Last() {
		t.Fatal("round-trip load must return the saved schedule pointer")
	}
}

// TestDefaultProfileSimpleEntriesPreservedAcrossLoad verifies that a Simple
// schedule with three entries is intact after a Load round-trip via the stub.
func TestDefaultProfileSimpleEntriesPreservedAcrossLoad(t *testing.T) {
	t.Parallel()

	s := schedule.NewSimple()
	for slot, day := range []schedule.Weekday{
		schedule.WeekdayMonday, schedule.WeekdayWednesday, schedule.WeekdayFriday,
	} {
		if err := s.Put(slot+1, schedule.SimpleEntry{
			Weekdays: []schedule.Weekday{day},
			Time:     "07:00",
			Level:    1,
		}); err != nil {
			t.Fatalf("Put(%d): %v", slot+1, err)
		}
	}

	loader := &simpleLoader{value: s}
	p := weekprofile.NewDefault(loader, nil)

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, slot := range got.Slots() {
		orig, ok := s.Entries[slot]
		if !ok {
			t.Errorf("slot %d missing in original", slot)
			continue
		}
		if got.Entries[slot].Time != orig.Time {
			t.Errorf("slot %d: time %s, want %s", slot, got.Entries[slot].Time, orig.Time)
		}
	}
}

// TestClimateWeekdayValidateIgnoresOrder asserts that out-of-order period
// submission is sorted before validation (Validate sorts internally).
func TestClimateWeekdayValidateIgnoresOrder(t *testing.T) {
	t.Parallel()

	// Periods submitted in reverse order — production must sort them.
	d := schedule.ClimateWeekday{
		BaseTemperature: 20,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "08:00", EndTime: "24:00", Temperature: 21},
			{StartTime: "00:00", EndTime: "08:00", Temperature: 18},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("reverse-order full-day schedule must be valid after sort: %v", err)
	}
}

// TestClimateWeekdayBaseTemperatureNotValidated asserts that base temperature
// is not range-checked at the model layer (it is a device constraint, not
// a schedule constraint). Any float64 must pass.
func TestClimateWeekdayBaseTemperatureNotValidated(t *testing.T) {
	t.Parallel()

	// Extreme temperatures — should not be rejected at the schedule layer.
	for _, temp := range []float64{-100, 0, 99.9} {
		d := schedule.ClimateWeekday{BaseTemperature: temp}
		if err := d.Validate(); err != nil {
			t.Errorf("BaseTemperature %.1f rejected at schedule layer: %v", temp, err)
		}
	}
}

// TestWeekdaysConstantCount confirms that the package exports exactly seven
// Weekday constants in the Weekdays slice (immutability / completeness guard).
func TestWeekdaysConstantCount(t *testing.T) {
	t.Parallel()

	if len(schedule.Weekdays) != 7 {
		t.Fatalf("schedule.Weekdays has %d entries, want 7", len(schedule.Weekdays))
	}
}

// TestSimpleEntryLevelOutOfRangeRejected asserts levels above 1.01 or below
// 0 are rejected.
func TestSimpleEntryLevelOutOfRangeRejected(t *testing.T) {
	t.Parallel()

	base := schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "08:00",
	}

	base.Level = -0.01
	if err := base.Validate(); err == nil {
		t.Fatal("negative level must be rejected")
	}
	base.Level = 1.02
	if err := base.Validate(); err == nil {
		t.Fatal("level > 1.01 must be rejected")
	}
	base.Level = 1.01
	if err := base.Validate(); err != nil {
		t.Fatalf("level 1.01 must be accepted: %v", err)
	}
}

// TestRepeatedLoadOfTheSameProfileIsNotAChange pins the boundary between a
// refresh and a change.
//
// The north-bound snapshot pass warms every climate channel's profile with a
// background Load — on boot, on every broker reconnect, on every
// device-created pass and on every device rename. Each of those published to
// the OnChange subscribers, which is how the WebSocket `schedules.changed`
// broadcast came to fire per climate channel when no schedule had moved. The
// contract behind that frame says a week profile changed, and a client acting
// on it makes a CCU round-trip per channel to re-read something identical.
//
// The first load still notifies: a subscriber has nothing before it, so the
// profile's arrival genuinely is new information.
func TestRepeatedLoadOfTheSameProfileIsNotAChange(t *testing.T) {
	t.Parallel()

	loader := &climateLoader{value: schedule.NewClimate()}
	p := weekprofile.NewClimate(loader, nil)

	var calls int
	p.OnChange(func(_, _ *schedule.Climate) { calls++ })

	for range 4 {
		if _, err := p.Load(context.Background()); err != nil {
			t.Fatalf("Load: %v", err)
		}
	}

	if calls != 1 {
		t.Errorf("four warm-up loads of an unchanged profile produced %d change "+
			"notifications, want 1 (the first load only) — every extra one becomes a "+
			"schedules.changed broadcast telling clients to re-read a schedule that "+
			"never moved", calls)
	}
}
