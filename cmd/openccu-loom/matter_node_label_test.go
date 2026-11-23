// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// recordingLabelNode is a node that records every label it is given, or
// refuses with err.
type recordingLabelNode struct {
	labels []string
	err    error
}

func (n *recordingLabelNode) SetNodeLabel(_ context.Context, label string) error {
	if n.err != nil {
		return n.err
	}
	n.labels = append(n.labels, label)
	return nil
}

func newSeededLabeler(t *testing.T, n nodeLabelSetter, applied string) *matterNodeLabeler {
	t.Helper()
	db := openTestLoomDB(t)
	if _, err := sqlite.LoadMatterHostIdentity(context.Background(), db,
		sqlite.MatterHostIdentity{RootSerial: "0123456789abcdef", AppliedNodeLabel: applied}); err != nil {
		t.Fatalf("seed host identity: %v", err)
	}
	return newMatterNodeLabeler(n, db, applied, nil)
}

// TestMatterNodeLabelerRefusalKeepsTheMarker: a label the node refuses is
// not recorded as applied, so the next attempt (a corrected save, the next
// start) tries again instead of believing the node serves it.
func TestMatterNodeLabelerRefusalKeepsTheMarker(t *testing.T) {
	t.Parallel()
	node := &recordingLabelNode{err: errors.New("constraint")}
	l := newSeededLabeler(t, node, "A")
	if changed, err := l.Apply(context.Background(), "B"); err == nil || changed {
		t.Fatalf("Apply on a refusing node = (%v, %v), want (false, error)", changed, err)
	}
	node.err = nil
	if changed, err := l.Apply(context.Background(), "B"); err != nil || !changed {
		t.Fatalf("Apply after the refusal = (%v, %v), want (true, nil)", changed, err)
	}
	if len(node.labels) != 1 || node.labels[0] != "B" {
		t.Fatalf("node labels = %v, want [B]", node.labels)
	}
}

// TestHotReloadHandlerAppliesAChangedNodeLabel: a north.matter.node_label
// change the file watcher sees reaches the running node through the same
// applier the config save uses, with the effective label, and is no
// longer reported as restart-required.
func TestHotReloadHandlerAppliesAChangedNodeLabel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	node := &recordingLabelNode{}
	deps := newReloadDeps()
	deps.SetMatterNodeLabeler(newSeededLabeler(t, node, "openccu-loom"))
	effective := config.Default()
	effective.North.Matter.NodeLabel = "Bridge Keller"
	deps.SetConfigAssembler(func(context.Context) (*config.Config, error) { return effective, nil })

	prev := config.Default()
	next := config.Default()
	next.North.Matter.NodeLabel = "Bridge Keller"
	if err := hotReloadHandler(logger, deps)(prev, next); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(node.labels) != 1 || node.labels[0] != "Bridge Keller" {
		t.Fatalf("node labels = %v, want [Bridge Keller]", node.labels)
	}
	out := buf.String()
	if strings.Contains(out, "field=north.matter.node_label") {
		t.Errorf("node_label still reported restart-required:\n%s", out)
	}
	if !strings.Contains(out, "hot_reloaded_fields=1") {
		t.Errorf("expected hot_reloaded_fields=1, got:\n%s", out)
	}
}
