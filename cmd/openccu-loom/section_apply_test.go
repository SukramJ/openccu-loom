// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/configstore"
)

// fakeSectionReloader records the reloads a section apply asked for.
type fakeSectionReloader struct {
	calls       int
	err         error
	unavailable bool
}

func (f *fakeSectionReloader) Available() bool { return !f.unavailable }

func (f *fakeSectionReloader) Reload(context.Context) (time.Duration, error) {
	f.calls++
	return time.Millisecond, f.err
}

func TestSectionApplierRebuildsTheMQTTStack(t *testing.T) {
	t.Parallel()

	r := &fakeSectionReloader{}
	applied, err := newSectionApplier(r, nil).ApplySection(context.Background(), configstore.SectionMQTT)
	if err != nil {
		t.Fatalf("ApplySection: %v", err)
	}
	if !applied {
		t.Error("a north.mqtt save must report applied")
	}
	if r.calls != 1 {
		t.Errorf("the MQTT stack was rebuilt %d times, want 1 — the operator's edit never reaches "+
			"the running bridge", r.calls)
	}
}

// TestSectionApplierLeavesOtherSectionsAlone pins that a save of some
// unrelated section does not tear down and rebuild the broker link.
func TestSectionApplierLeavesOtherSectionsAlone(t *testing.T) {
	t.Parallel()

	r := &fakeSectionReloader{}
	a := newSectionApplier(r, nil)
	for _, section := range []configstore.Section{
		configstore.SectionREST,
		configstore.SectionMatter,
	} {
		applied, err := a.ApplySection(context.Background(), section)
		if err != nil {
			t.Fatalf("ApplySection(%s): %v", section, err)
		}
		if applied {
			t.Errorf("section %s reported applied, but no subsystem takes it live", section)
		}
	}
	if r.calls != 0 {
		t.Errorf("an unrelated section save rebuilt the MQTT stack %d times", r.calls)
	}
}

// TestSectionApplierReportsAFailedReload pins that a refused rebuild is
// reported rather than swallowed: the value is stored either way, and
// only the error distinguishes "live now" from "waiting for a restart".
func TestSectionApplierReportsAFailedReload(t *testing.T) {
	t.Parallel()

	r := &fakeSectionReloader{err: errors.New("broker refused the connection")}
	applied, err := newSectionApplier(r, nil).ApplySection(context.Background(), configstore.SectionMQTT)
	if err == nil {
		t.Fatal("a failed rebuild must be reported")
	}
	if applied {
		t.Error("a failed rebuild must not report applied")
	}
}

// TestSectionApplierWithoutAReloaderReportsNotApplied pins the answer a
// daemon whose MQTT supervisor never came up gives: not applied, and no
// error.
//
// The distinction is the whole point. The reload adapter is constructed
// unconditionally, so a plain nil check cannot see the absence, and
// calling Reload on it yields "supervisor unavailable" — an error the
// operator would see on every north.mqtt save even though nothing went
// wrong and the value is stored correctly.
func TestSectionApplierWithoutAReloaderReportsNotApplied(t *testing.T) {
	t.Parallel()

	absent := newMQTTReloadAdapter(nil, nil, nil, nil)
	applied, err := newSectionApplier(absent, nil).ApplySection(context.Background(), configstore.SectionMQTT)
	if err != nil {
		t.Fatalf("ApplySection: %v", err)
	}
	if applied {
		t.Error("a daemon with no MQTT supervisor must not report the section as applied")
	}
}
