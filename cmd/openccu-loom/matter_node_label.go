// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// nodeLabelSetter is the one node capability the labeler needs
// (node.Node.SetNodeLabel); naming it keeps the labeler testable without a
// running node.
type nodeLabelSetter interface {
	SetNodeLabel(ctx context.Context, label string) error
}

// matterNodeLabeler carries north.matter.node_label to the running node.
//
// go-fabric persists a label set on the node and restores it over the
// configured one at every start, as it does a label a controller wrote. So
// the configured value cannot simply be handed to the node at each start:
// it would never win over a stored label, and handing it over
// unconditionally would undo a controller's rename on every start. The
// labeler applies the configured label only when it differs from the one it
// last applied (matter_host_identity.applied_node_label): an operator's
// change reaches the node at once, a change saved while the bridge was off
// reaches it at the next start, and a controller's rename stands until the
// operator changes the setting again. The marker is seeded with the label
// configured at the first Matter start that has it, so that start takes
// the configured label as applied.
//
// Apply is safe for concurrent use: a config save and the file watcher can
// race, and the comparison, the node write and the marker write form one
// step.
type matterNodeLabeler struct {
	mu      sync.Mutex
	node    nodeLabelSetter
	db      *gosql.DB
	applied string
	logger  *slog.Logger
}

// newMatterNodeLabeler binds the labeler to the node and the stored marker
// applied (the label it last applied).
func newMatterNodeLabeler(n nodeLabelSetter, db *gosql.DB, applied string, logger *slog.Logger) *matterNodeLabeler {
	if logger == nil {
		logger = slog.Default()
	}
	return &matterNodeLabeler{node: n, db: db, applied: applied, logger: logger}
}

// Apply sets label on the node when it differs from the label last applied,
// and records it. It reports whether the node's label changed. The node
// refuses a label outside the attribute's constraint; the config validator
// holds north.matter.node_label to the same constraint, so a refusal here
// means the two disagree and is returned. A failed marker write is returned
// too: the node then serves the new label, and the next start applies it
// once more, which changes nothing unless a controller renamed the node
// meanwhile.
func (l *matterNodeLabeler) Apply(ctx context.Context, label string) (bool, error) {
	if l == nil {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if label == l.applied {
		return false, nil
	}
	if err := l.node.SetNodeLabel(ctx, label); err != nil {
		return false, fmt.Errorf("matter node label: %w", err)
	}
	prev := l.applied
	l.applied = label
	if err := sqlite.SetMatterAppliedNodeLabel(ctx, l.db, label); err != nil {
		return true, fmt.Errorf("matter node label: %w", err)
	}
	l.logger.Info("matter.bridge.node_label_applied",
		slog.String("from", prev),
		slog.String("to", label))
	return true, nil
}

// rebind moves the labeler to n, a node an erase brought up, and sets the
// label last applied on it. The erase took the label the old node stored,
// a controller's rename included, so the new node would otherwise serve the
// label configured when the daemon started rather than the operator's last.
// The marker stays: the label it names is the one the node now serves.
func (l *matterNodeLabeler) rebind(ctx context.Context, n nodeLabelSetter) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.node = n
	if err := n.SetNodeLabel(ctx, l.applied); err != nil {
		return fmt.Errorf("matter node label: %w", err)
	}
	return nil
}
