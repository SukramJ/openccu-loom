// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"errors"
	"fmt"

	"github.com/SukramJ/go-fabric/cluster/spec"
	closurecontroldef "github.com/SukramJ/go-fabric/cluster/spec/closurecontrol"
	colorcontroldef "github.com/SukramJ/go-fabric/cluster/spec/colorcontrol"
	doorlockdef "github.com/SukramJ/go-fabric/cluster/spec/doorlock"
	levelcontroldef "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"
	modeselectdef "github.com/SukramJ/go-fabric/cluster/spec/modeselect"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	smokecoalarmdef "github.com/SukramJ/go-fabric/cluster/spec/smokecoalarm"
	thermostatdef "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	thermostatuidef "github.com/SukramJ/go-fabric/cluster/spec/thermostatuserinterfaceconfiguration"
	windowcoveringdef "github.com/SukramJ/go-fabric/cluster/spec/windowcovering"
	"github.com/SukramJ/go-fabric/contract"
	fabricdevice "github.com/SukramJ/go-fabric/device"
)

// hostServerDeclarations maps a cluster id to the generated declaration
// that mounts a profile's own server for it.
//
// go-fabric's device layer takes a host's server for a cluster with a
// generated package only as that package's Declare{HostServer: srv}, and
// refuses device.WithServer for it (go-fabric device/options.go:153-160).
// A profile hands its servers over as a list, so the assembler picks the
// declaration by the server's cluster id. The set is the clusters loom's
// profiles serve; a server for any other cluster with a generated package
// is refused by [hostServerDeclaration], and the endpoint falls back to
// the hand-built spec.
var hostServerDeclarations = map[uint32]func(contract.ClusterServer) fabricdevice.Declaration{
	onoffdef.ClusterID:        func(s contract.ClusterServer) fabricdevice.Declaration { return onoffdef.Declare{HostServer: s} },
	levelcontroldef.ClusterID: func(s contract.ClusterServer) fabricdevice.Declaration { return levelcontroldef.Declare{HostServer: s} },
	colorcontroldef.ClusterID: func(s contract.ClusterServer) fabricdevice.Declaration { return colorcontroldef.Declare{HostServer: s} },
	windowcoveringdef.ClusterID: func(s contract.ClusterServer) fabricdevice.Declaration {
		return windowcoveringdef.Declare{HostServer: s}
	},
	closurecontroldef.ClusterID: func(s contract.ClusterServer) fabricdevice.Declaration {
		return closurecontroldef.Declare{HostServer: s}
	},
	thermostatdef.ClusterID:   func(s contract.ClusterServer) fabricdevice.Declaration { return thermostatdef.Declare{HostServer: s} },
	thermostatuidef.ClusterID: func(s contract.ClusterServer) fabricdevice.Declaration { return thermostatuidef.Declare{HostServer: s} },
	doorlockdef.ClusterID:     func(s contract.ClusterServer) fabricdevice.Declaration { return doorlockdef.Declare{HostServer: s} },
	smokecoalarmdef.ClusterID: func(s contract.ClusterServer) fabricdevice.Declaration { return smokecoalarmdef.Declare{HostServer: s} },
	modeselectdef.ClusterID:   func(s contract.ClusterServer) fabricdevice.Declaration { return modeselectdef.Declare{HostServer: s} },
}

// errNoHostServerDeclaration reports a profile server whose cluster has a
// generated package but no entry in [hostServerDeclarations].
var errNoHostServerDeclaration = errors.New("matteradapter: no host-server declaration for the cluster")

// hostServerDeclaration returns the declaration that mounts srv: the
// generated Declare{HostServer} for a cluster in [hostServerDeclarations],
// device.WithServer for a cluster without a generated package (a
// manufacturer-specific one), and errNoHostServerDeclaration otherwise.
func hostServerDeclaration(srv contract.ClusterServer) (fabricdevice.Declaration, error) {
	id := srv.MatterClusterID()
	if declare, ok := hostServerDeclarations[id]; ok {
		return declare(srv), nil
	}
	if spec.Lookup(id) != nil {
		return nil, fmt.Errorf("%w 0x%04X", errNoHostServerDeclaration, id)
	}
	return fabricdevice.WithServer(srv), nil
}
