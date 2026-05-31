// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// select.go — NewSelect, SetLabel, SetIndex, checkEnumIndex
// ---------------------------------------------------------------------------

func TestSelect_SetLabel_NotWritable(t *testing.T) {
	t.Parallel()
	s := NewSelect(baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead))
	if err := s.SetLabel(context.Background(), "A", hmenum.CommandPriorityHigh); !errors.Is(err, ErrNotWritable) {
		t.Errorf("expected ErrNotWritable, got %v", err)
	}
}

func TestSelect_SetLabel_EmptyValueList(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsWrite)
	s := NewSelect(cfg)
	w := &stubWriter{}
	s.Writer = w
	if err := s.SetLabel(context.Background(), "A", hmenum.CommandPriorityHigh); !errors.Is(err, ErrEmptyValueList) {
		t.Errorf("expected ErrEmptyValueList, got %v", err)
	}
}

func TestSelect_SetLabel_UnknownLabel(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"A", "B", "C"}
	s := NewSelect(cfg)
	w := &stubWriter{}
	s.Writer = w
	if err := s.SetLabel(context.Background(), "Z", hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("unknown label: expected error")
	}
}

func TestSelect_SetIndex_OutOfRange(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"X", "Y"}
	s := NewSelect(cfg)
	w := &stubWriter{}
	s.Writer = w
	if err := s.SetIndex(context.Background(), 5, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("out-of-range index: expected error")
	}
}

// ---------------------------------------------------------------------------
// action_select.go — checkEnumIndex edge cases
// ---------------------------------------------------------------------------

func TestCheckEnumIndex_NegativeIdx_EmptyList(t *testing.T) {
	t.Parallel()
	if err := checkEnumIndex(nil, -1); err == nil {
		t.Error("negative index with empty list should fail")
	}
}

func TestCheckEnumIndex_EmptyList_NonNegative(t *testing.T) {
	t.Parallel()
	if err := checkEnumIndex(nil, 0); err != nil {
		t.Errorf("non-negative index, empty list: expected nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// select.go — NewSelect partial config, Label before observation
// ---------------------------------------------------------------------------

func TestSelectNewSelectPartialConfig(t *testing.T) {
	t.Parallel()
	// No writer, no value list → still constructs.
	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsRead)
	s := NewSelect(cfg)
	if s == nil {
		t.Fatal("NewSelect must not return nil")
	}
	// Label returns ("", false) when unobserved.
	_, ok := s.Label()
	if ok {
		t.Fatal("unobserved Label must return ok=false")
	}
}

func TestSelectSetIndexOutOfRange(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"A", "B"}
	cfg.Writer = &stubWriter{}
	s := NewSelect(cfg)
	if err := s.SetIndex(context.Background(), 5, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("out-of-range index must error")
	}
}
