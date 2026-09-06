// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// weekprofileLocksRecordingBackend records the COMBINED_PARAMETER writes the
// schedule-enabled backend fallback issues. The embedded interface stays nil:
// the fallback reaches no other backend method, and a nil-pointer panic is a
// louder failure than a silent default.
type weekprofileLocksRecordingBackend struct {
	backends.Operations

	values []string
}

func (b *weekprofileLocksRecordingBackend) SetValue(
	_ context.Context,
	_ string,
	parameter hmenum.Parameter,
	value any,
	_ hmenum.CommandPriority,
	_ hmenum.CommandRxMode,
) error {
	if parameter == hmenum.ParameterCombinedParameter {
		s, _ := value.(string)
		b.values = append(b.values, s)
	}
	return nil
}

// weekprofileLocksFallbackDomain builds a schedules domain over one central
// holding a device whose only schedule surface is a *_WEEK_PROFILE channel
// with NO ProfileDataPoint attached, so SetScheduleEnabled cannot delegate to
// the model writer and takes the adapter's backend fallback — the path that
// used to render the frame from its own bit table.
func weekprofileLocksFallbackDomain(t *testing.T) (*adapter.SchedulesDomain, *weekprofileLocksRecordingBackend, string) {
	t.Helper()
	const (
		centralName = "ccu-01"
		interfaceID = "HmIP-RF"
		address     = "00WPLOCKS01"
	)
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	d := device.New(device.Config{
		Address:     address,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: interfaceID,
		Model:       "HmIP-BSM",
	})
	d.AddChannel(address+":4", 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	d.AddChannel(address+":8", 8, "SWITCH_WEEK_PROFILE", hmenum.ParamsetKeyMaster)
	c.ModelRegistry.Put(d)

	backend := &weekprofileLocksRecordingBackend{}
	w := client.NewValueWriter()
	w.Register(centralName, interfaceID, backend)
	return adapter.NewSchedulesDomain(reg, w), backend, address
}

// TestWeekprofileLocksBackendFallbackUsesTheModelBitTable pins the adapter's
// unmodelled-device write path to the week-profile model's own encoding.
//
// The adapter used to carry a second copy of a channel table and its own
// WPTCLS/WPTCL rendering, so a bit reassigned in the model left the fallback
// writing the previous bit — a wrong channel toggled on a real device. Expectation
// and observation here share no literal: the want side is built from
// weekprofile.TargetBitOrder over the fixture device's own channels +
// BuildCombinedParameterValue, the got side is whatever the live
// SetScheduleEnabled path put on the wire.
//
// Without a modelled week profile only the fallback key "1_1" resolves — to
// the lowest bit of the device's schedule-relevant list — and every other key
// is refused rather than guessed: the key→channel map is minted from the
// custom-DP groups, and a device without one has no such map.
func TestWeekprofileLocksBackendFallbackUsesTheModelBitTable(t *testing.T) {
	t.Parallel()
	domain, backend, address := weekprofileLocksFallbackDomain(t)
	ctx := context.Background()

	order := weekprofile.TargetBitOrder("HmIP-BSM", []weekprofile.TypedChannel{
		{No: 4, Type: "SWITCH_VIRTUAL_RECEIVER"}, {No: 8, Type: "SWITCH_WEEK_PROFILE"},
	})
	bit, ok := order[4]
	if !ok {
		t.Fatal("fixture device's receiver channel 4 has no position in the model's order")
	}
	written := 0
	for _, enabled := range []bool{true, false} {
		if err := domain.SetScheduleEnabled(ctx, address, enabled, "1_1"); err != nil {
			t.Fatalf("SetScheduleEnabled(1_1, %v): %v", enabled, err)
		}
		if len(backend.values) != written+1 {
			t.Fatalf("enabled=%v: got %d COMBINED_PARAMETER writes, want %d", enabled, len(backend.values), written+1)
		}
		want := weekprofile.BuildCombinedParameterValue(uint32(1)<<bit, enabled)
		if got := backend.values[written]; got != want {
			t.Errorf("enabled=%v: wrote %q, model renders %q", enabled, got, want)
		}
		written++
	}
	for _, key := range []string{"1_2", "2_1", "9_9"} {
		if err := domain.SetScheduleEnabled(ctx, address, true, key); err == nil {
			t.Errorf("key %q was written for a device with no modelled key map; it must be refused", key)
		}
	}
	if len(backend.values) != written {
		t.Errorf("refused keys still reached the wire: %d writes, want %d", len(backend.values), written)
	}
}

// TestWeekprofileLocksWireFormatLivesOnlyInTheModel is the cheap recurrence
// guard for the same defect: the WPTCLS/WPTCL payload grammar must be spelled
// in exactly one package. A second Sprintf anywhere in the daemon is how the
// two copies drifted apart the first time, and the effect guard above cannot
// see a duplicate that still happens to agree.
//
// Only string literals count — a comment naming the format is documentation,
// not a second renderer.
func TestWeekprofileLocksWireFormatLivesOnlyInTheModel(t *testing.T) {
	t.Parallel()
	const owner = "internal/model/weekprofile"
	var offenders []string
	for _, root := range []string{"internal", "cmd", "pkg"} {
		dir := filepath.Join(repoRoot(t), root)
		err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(path)
			if e.IsDir() || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			if strings.Contains(rel, owner) {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return parseErr
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if strings.Contains(lit.Value, "WPTCLS") {
					offenders = append(offenders, fmt.Sprintf("%s: %s", rel, lit.Value))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("WPTCLS payload grammar rendered outside %s: %v\n"+
			"call weekprofile.BuildCombinedParameterValue instead of re-rendering the frame",
			owner, offenders)
	}
}
