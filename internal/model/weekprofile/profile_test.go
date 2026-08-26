// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

type stubLoader struct {
	sched *schedule.Simple
	err   error
}

func (s *stubLoader) Load(_ context.Context) (*schedule.Simple, error) {
	return s.sched, s.err
}

type stubSaver struct {
	last *schedule.Simple
	err  error
}

func (s *stubSaver) Save(_ context.Context, v *schedule.Simple) error {
	if s.err != nil {
		return s.err
	}
	s.last = v
	return nil
}

func TestDefaultProfileCurrentBeforeLoad(t *testing.T) {
	p := NewDefault(&stubLoader{}, &stubSaver{})
	if _, err := p.Current(); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("err=%v", err)
	}
}

func TestDefaultProfileLoadPublishes(t *testing.T) {
	want := schedule.NewSimple()
	_ = want.Put(1, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "08:00",
		Level:    1,
	})
	p := NewDefault(&stubLoader{sched: want}, &stubSaver{})
	var fired int
	p.OnChange(func(_, _ *schedule.Simple) { fired++ })

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatal("Load must return loader value")
	}
	cur, _ := p.Current()
	if cur != want {
		t.Fatal("Current() must reflect loaded value")
	}
	if fired != 1 {
		t.Fatalf("fired=%d", fired)
	}
}

func TestDefaultProfileSavePublishes(t *testing.T) {
	saver := &stubSaver{}
	p := NewDefault(nil, saver)
	sched := schedule.NewSimple()
	if err := p.Save(context.Background(), sched); err != nil {
		t.Fatalf("save: %v", err)
	}
	if saver.last != sched {
		t.Fatal("saver last must be sched")
	}
	cur, _ := p.Current()
	if cur != sched {
		t.Fatal("Current must reflect saved value")
	}
}

func TestDefaultProfileSaveErrorDoesNotPublish(t *testing.T) {
	p := NewDefault(nil, &stubSaver{err: errors.New("boom")})
	var fired int
	p.OnChange(func(_, _ *schedule.Simple) { fired++ })
	if err := p.Save(context.Background(), schedule.NewSimple()); err == nil {
		t.Fatal("error must propagate")
	}
	if fired != 0 {
		t.Fatalf("fired=%d", fired)
	}
}

func TestClimateProfileRoundtrip(t *testing.T) {
	sched := schedule.NewClimate()
	p := NewClimate(nil, &struct{ saverFnClimate }{
		saverFnClimate{fn: func(v *schedule.Climate) error { return nil }},
	})
	if err := p.Save(context.Background(), sched); err != nil {
		t.Fatalf("save: %v", err)
	}
}

type saverFnClimate struct {
	fn func(*schedule.Climate) error
}

func (s saverFnClimate) Save(_ context.Context, v *schedule.Climate) error {
	return s.fn(v)
}
