// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package mcp exposes the OpenCCU-Loom domain to LLM agents over the
// Model Context Protocol, as a north-bound adapter (ADR 0025). It is a
// thin projection of the same domain the REST surface serves: every
// tool is scoped per central, reads are always available, and writes
// are registered only when the operator opts in twice (Enabled +
// AllowWrites). Authorization is enforced by the REST listener the
// Streamable-HTTP handler mounts behind; the adapter holds no privilege
// path of its own.
package mcp

import (
	"context"
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CentralLister enumerates the configured CCUs — the scoping dimension
// every central-touching tool names explicitly (ADR 0002).
type CentralLister interface {
	Names() []string
}

// DeviceLister is the read surface over the device model. It mirrors
// the REST DeviceIndex contract so both adapters project the same data.
type DeviceLister interface {
	Devices() []*device.Device
	Device(address string) (*device.Device, bool)
	// CentralOf returns the owning CCU name, or "" when the device is
	// unknown.
	CentralOf(address string) string
}

// ValueWriter pushes a value to the CCU. Same contract as the REST
// DataPointWriter; only reached by the write-gated set_datapoint tool.
type ValueWriter interface {
	SetValue(
		ctx context.Context,
		address string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error
}

// Deps is the wiring surface. Writer may be nil (no writes available).
// Audit may be nil (no change-log surface); when present it serves both
// the read tool (List) and records MCP-origin writes (Record).
type Deps struct {
	Centrals    CentralLister
	Devices     DeviceLister
	Writer      ValueWriter
	Audit       audit.Recorder
	AllowWrites bool
	Version     string
}

// writesEnabled reports whether the write tools should be registered:
// the operator opted in (AllowWrites) and a writer is actually wired.
func (d Deps) writesEnabled() bool {
	return d.AllowWrites && d.Writer != nil
}

// NewServer builds the MCP server and registers the tool set. Read
// tools are always registered; write tools only when writes are enabled.
func NewServer(d Deps) *mcpsdk.Server {
	version := d.Version
	if version == "" {
		version = "0.0.0"
	}
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "openccu-loom",
		Version: version,
	}, nil)
	registerReadTools(s, d)
	if d.writesEnabled() {
		registerWriteTools(s, d)
	}
	return s
}

// Handler returns the Streamable-HTTP handler to mount on the REST
// listener (e.g. at /mcp). The server is built once and shared across
// sessions; the SDK manages per-session state.
func Handler(d Deps) http.Handler {
	srv := NewServer(d)
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return srv },
		nil,
	)
}
