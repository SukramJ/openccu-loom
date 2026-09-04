// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// Binding implements the Matter Binding cluster (0x001E) per Matter
// Core Specification 1.5.1 §9.6, which is mandatory on bridged
// endpoints that support local actions referencing other Matter nodes
// (e.g. a Light switch endpoint binding to a target Light endpoint).
//
// Stub, not mounted: no bridged endpoint in v1.1 constructs a Binding
// server — [NewBinding] has no production caller (see
// internal/north/matter/cluster/core/doc.go's cluster inventory), so
// this cluster is absent from every endpoint's ServerList and no
// controller can reach the write handler below. The bridge does not
// originate traffic to bound nodes even where it is present, so
// mounting it would still be a round-trippable-but-opaque store, not
// live binding behaviour. Wiring this in requires deciding which
// bridged endpoints declare the binding feature; deferred until a
// concrete use-case arrives.
type Binding struct {
	mu       sync.RWMutex
	bindings []TargetStruct
}

// TargetStruct mirrors the Binding cluster's TargetStruct (Matter
// §9.6.5.1). All fields are optional except Endpoint XOR Group;
// FabricIndex is fabric-scoped.
type TargetStruct struct {
	Node        uint64 // 0 means "not present"
	Group       uint16 // 0 means "not present"
	Endpoint    uint16 // 0 means "not present"
	Cluster     uint32 // 0xFFFFFFFF means "not present"
	FabricIndex uint8
}

// Cluster ID + revision per Matter §9.6.
const (
	bindingClusterID       uint32 = 0x001E
	bindingClusterRevision uint16 = 1

	bindingAttrBinding uint32 = 0x0000
)

// NewBinding returns a Binding cluster server with an empty list.
func NewBinding() *Binding {
	return &Binding{bindings: []TargetStruct{}}
}

// Compile-time assertions: Binding satisfies MatterClusterServer and
// the attribute-lister capability.
var (
	_ mattercontract.ClusterServer          = (*Binding)(nil)
	_ mattercontract.ClusterAttributeLister = (*Binding)(nil)
)

// MatterClusterID implements [mattercontract.ClusterServer].
func (b *Binding) MatterClusterID() uint32 { return bindingClusterID }

// MatterRead returns the bindings list under a copy.
func (b *Binding) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case bindingAttrBinding:
		b.mu.RLock()
		out := append([]TargetStruct(nil), b.bindings...)
		b.mu.RUnlock()
		return out, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return bindingClusterRevision, true
	}
	return nil, false
}

// MatterWrite replaces the bindings list. The new list value must be
// a `[]TargetStruct`. Per spec the write is fabric-scoped — entries
// for fabrics other than the writer's are preserved by the dispatcher
// before this call (openccu-loom relies on the IM layer to enforce that).
func (b *Binding) MatterWrite(_ context.Context, attrID uint32, value any) error {
	if attrID != bindingAttrBinding {
		return fmt.Errorf("matter: Binding has no writable attribute 0x%04X", attrID)
	}
	list, ok := value.([]TargetStruct)
	if !ok {
		return fmt.Errorf("matter: Binding write expected []TargetStruct, got %T", value)
	}
	b.mu.Lock()
	b.bindings = append([]TargetStruct(nil), list...)
	b.mu.Unlock()
	return nil
}

// MatterInvoke always rejects — Binding has no commands.
func (b *Binding) MatterInvoke(_ context.Context, cmdID uint32, _ any) (any, error) {
	return nil, im.UnsupportedCommandf("matter: Binding has no commands (got 0x%02X)", cmdID)
}

// MatterReportable lists the subscribe-able attributes.
func (b *Binding) MatterReportable() []uint32 {
	return []uint32{bindingAttrBinding}
}

// MatterAttributes lists every Binding (0x001E) attribute the server
// implements via MatterRead. Apple Home's HAP service rebuild reads
// the full attribute set; without this the dispatcher falls back to
// MatterReportable's single attribute.
func (b *Binding) MatterAttributes() []uint32 {
	return []uint32{bindingAttrBinding}
}
