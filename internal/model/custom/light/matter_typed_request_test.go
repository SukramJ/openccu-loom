// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"testing"

	"github.com/SukramJ/go-fabric/cluster/spec"
	levelcontrolspec "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"
	onoffspec "github.com/SukramJ/go-fabric/cluster/spec/onoff"
)

// TestExtractOnWithTimedOffAcceptsBothBridgeShapes pins that the decoder
// reads the generated request struct and the tag-keyed map to the same
// values, so the command does not depend on which definition packages the
// binary links.
func TestExtractOnWithTimedOffAcceptsBothBridgeShapes(t *testing.T) {
	cases := []struct {
		name   string
		fields any
	}{
		{"generated struct", onoffspec.OnWithTimedOffRequest{OnOffControl: onoffspec.OnOffControlAcceptOnlyWhenOn, OnTime: 30, OffWaitTime: 7}},
		{"tag map", map[uint8]any{0: uint64(1), 1: uint64(30), 2: uint64(7)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			control, onTime, offWait, err := extractOnWithTimedOff(tc.fields)
			if err != nil {
				t.Fatalf("extractOnWithTimedOff: %v", err)
			}
			if control != 1 || onTime != 30 || offWait != 7 {
				t.Fatalf("got control=%d onTime=%d offWait=%d, want 1/30/7", control, onTime, offWait)
			}
		})
	}
	// The command-field constraints hold for the struct shape too.
	if _, _, _, err := extractOnWithTimedOff(onoffspec.OnWithTimedOffRequest{OnTime: 0xFFFF}); err == nil {
		t.Fatal("OnTime 0xFFFF in the generated struct must be a constraint error")
	}
}

// TestLevelStepAndMoveDecodersAcceptBothShapes pins that the Step / Move
// readers take the generated LevelControl structs as well as the tag map
// the bridge delivers today (it decodes these commands ahead of the
// generated definition), so a change of that ordering does not break them.
func TestLevelStepAndMoveDecodersAcceptBothShapes(t *testing.T) {
	steps := []struct {
		name   string
		fields any
	}{
		{"generated struct", levelcontrolspec.StepRequest{
			StepMode: levelcontrolspec.StepModeEnum(1), StepSize: 25,
			OptionsMask: 1, OptionsOverride: 1,
		}},
		{"tag map", map[uint8]any{0: uint64(1), 1: uint64(25), 3: uint64(1), 4: uint64(1)}},
	}
	for _, tc := range steps {
		t.Run("step "+tc.name, func(t *testing.T) {
			size, err := extractStepSize(tc.fields)
			if err != nil || size != 25 {
				t.Fatalf("extractStepSize = (%d, %v), want (25, nil)", size, err)
			}
			mode, err := extractStepMode(tc.fields)
			if err != nil || mode != 1 {
				t.Fatalf("extractStepMode = (%d, %v), want (1, nil)", mode, err)
			}
			mask, override := extractLevelOptions(tc.fields)
			if mask != 1 || override != 1 {
				t.Fatalf("extractLevelOptions = (%d, %d), want (1, 1)", mask, override)
			}
		})
	}

	moves := []struct {
		name   string
		fields any
		rate   uint8
		ok     bool
	}{
		{"generated struct", levelcontrolspec.MoveRequest{Rate: spec.Nullable[uint8]{Value: 7}}, 7, true},
		{"generated struct null rate", levelcontrolspec.MoveRequest{Rate: spec.Nullable[uint8]{Null: true}}, 0, false},
		{"tag map", map[uint8]any{1: uint64(7)}, 7, true},
		{"tag map null rate", map[uint8]any{1: nil}, 0, false},
	}
	for _, tc := range moves {
		t.Run("move "+tc.name, func(t *testing.T) {
			rate, ok := extractMoveRate(tc.fields)
			if rate != tc.rate || ok != tc.ok {
				t.Fatalf("extractMoveRate = (%d, %v), want (%d, %v)", rate, ok, tc.rate, tc.ok)
			}
		})
	}
}
