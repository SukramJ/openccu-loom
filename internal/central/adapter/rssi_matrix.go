// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DefaultReceiverMarginDB is the minimum advantage in dB another RF gateway
// must show over the current one before a switch is proposed. Consecutive
// readings of the same radio pair routinely differ by a few dB; a smaller
// margin would propose moving a device back and forth on noise alone.
const DefaultReceiverMarginDB = 6

// RSSIMatrixDomain is the live implementation of
// [interfaces.RSSIMatrixService]. It reads the BidCos-RF daemon's pairwise
// reception matrix (`rssiInfo`) and gateway list live from every central's
// BidCos-RF backend and derives the best-gateway dry run from them. HmIP
// has no pairwise matrix, so only BidCos-RF takes part.
type RSSIMatrixDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewRSSIMatrixDomain wires the live adapter. The writer is the
// (central, interface) backend registry the BidCos-RF backend is resolved
// from.
func NewRSSIMatrixDomain(r *central.Registry, w *client.ValueWriter) *RSSIMatrixDomain {
	return &RSSIMatrixDomain{registry: r, writer: w}
}

var _ interfaces.RSSIMatrixService = (*RSSIMatrixDomain)(nil)

// ErrNoRSSIMatrixSource is returned when the domain has no central
// registry or backend registry to read from.
var ErrNoRSSIMatrixSource = errors.New("rssi matrix: no central registry")

// centralMatrix is one central's live reading: the raw matrix as the
// daemon answered it and the central's RF gateways, or the failure that
// prevented reading them.
type centralMatrix struct {
	unit     *central.Unit
	wireID   hmtypes.WireInterfaceID
	matrix   map[string]map[string][2]int
	gateways []jsonrpc.BidcosInterface
	err      error
}

// readMatrices reads the matrix and gateway list of every central that
// has a BidCos-RF backend. A central without one, or whose backend does
// not offer the matrix, is skipped. A read failure is kept on that
// central's entry instead of failing the call: with several CCUs, one
// unreachable box must not blank the readings of the others.
func (d *RSSIMatrixDomain) readMatrices(ctx context.Context) ([]centralMatrix, error) {
	if d.registry == nil || d.writer == nil {
		return nil, ErrNoRSSIMatrixSource
	}
	var out []centralMatrix
	for _, u := range d.registry.List() {
		if u == nil {
			continue
		}
		wireID := hmtypes.NewWireInterfaceID(u.Name(), hmenum.InterfaceBidCosRF)
		backend, ok := d.writer.Backend(u.Name(), wireID)
		if !ok {
			continue
		}
		cm := centralMatrix{unit: u, wireID: wireID}
		matrix, err := backend.RSSIInfo(ctx)
		if err != nil {
			if errors.Is(err, backends.ErrUnsupported) {
				continue
			}
			cm.err = fmt.Errorf("read matrix: %w", err)
			out = append(out, cm)
			continue
		}
		// The gateway listing is the same call the periodic duty-cycle
		// poll makes; the wire takes the bare interface name.
		if lister, ok := backend.(bidcosGatewayLister); ok {
			raw, err := lister.ListBidcosInterfaces(ctx, string(hmenum.InterfaceBidCosRF))
			if err != nil {
				cm.err = fmt.Errorf("list gateways: %w", err)
				out = append(out, cm)
				continue
			}
			cm.gateways = jsonrpc.DecodeBidcosInterfaces(raw)
		}
		cm.matrix = matrix
		out = append(out, cm)
	}
	return out, nil
}

// RSSIMatrix implements [interfaces.RSSIMatrixService]. A central whose
// read failed is listed with its error and no rows.
func (d *RSSIMatrixDomain) RSSIMatrix(ctx context.Context) ([]interfaces.RSSIMatrixCentral, error) {
	reads, err := d.readMatrices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]interfaces.RSSIMatrixCentral, 0, len(reads))
	for _, cm := range reads {
		if cm.err != nil {
			out = append(out, interfaces.RSSIMatrixCentral{
				Central:     cm.unit.Name(),
				InterfaceID: cm.wireID.String(),
				Interfaces:  []interfaces.RSSIMatrixInterface{},
				Devices:     []interfaces.RSSIMatrixDevice{},
				Error:       cm.err.Error(),
			})
			continue
		}
		out = append(out, mapCentralMatrix(cm))
	}
	return out, nil
}

// mapCentralMatrix converts one central's raw reading into the service
// shape: the no-information marker becomes nil, devices and partners are
// sorted by address, and model devices carry their display name.
func mapCentralMatrix(cm centralMatrix) interfaces.RSSIMatrixCentral {
	res := interfaces.RSSIMatrixCentral{
		Central:     cm.unit.Name(),
		InterfaceID: cm.wireID.String(),
		Interfaces:  make([]interfaces.RSSIMatrixInterface, 0, len(cm.gateways)),
		Devices:     make([]interfaces.RSSIMatrixDevice, 0, len(cm.matrix)),
	}
	for _, g := range cm.gateways {
		res.Interfaces = append(res.Interfaces, interfaces.RSSIMatrixInterface{
			Address:     g.Address,
			Description: g.Description,
			Connected:   g.Connected,
			Default:     g.Default,
			DutyCycle:   g.DutyCycle,
		})
	}
	sort.Slice(res.Interfaces, func(i, j int) bool { return res.Interfaces[i].Address < res.Interfaces[j].Address })

	for _, addr := range slices.Sorted(maps.Keys(cm.matrix)) {
		row := cm.matrix[addr]
		dev := interfaces.RSSIMatrixDevice{
			Address:  addr,
			Partners: make([]interfaces.RSSIMatrixPartner, 0, len(row)),
		}
		if cm.unit.ModelRegistry != nil {
			if md, ok := cm.unit.ModelRegistry.Get(addr); ok {
				dev.Name = md.Name()
			}
		}
		for _, partner := range slices.Sorted(maps.Keys(row)) {
			pair := row[partner]
			dev.Partners = append(dev.Partners, interfaces.RSSIMatrixPartner{
				Address: partner,
				RxDBm:   rssiReading(pair[0]),
				TxDBm:   rssiReading(pair[1]),
			})
		}
		res.Devices = append(res.Devices, dev)
	}
	return res
}

// ReceiverProposal implements [interfaces.RSSIMatrixService].
//
// For every BidCos-RF device in the model of a central with a matrix, the
// current gateway serial and roaming flag are taken from the device's own
// description (INTERFACE / ROAMING) as the BidCos-RF daemon reported it.
func (d *RSSIMatrixDomain) ReceiverProposal(ctx context.Context, marginDB int) ([]interfaces.ReceiverProposal, error) {
	if marginDB < 0 {
		marginDB = DefaultReceiverMarginDB
	}
	reads, err := d.readMatrices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]interfaces.ReceiverProposal, 0)
	for _, cm := range reads {
		// A central whose matrix could not be read yields no verdicts:
		// "unmeasured" would claim a measurement that never happened.
		if cm.err != nil || cm.unit.ModelRegistry == nil {
			continue
		}
		gateways := make([]string, 0, len(cm.gateways))
		isGateway := make(map[string]bool, len(cm.gateways))
		for _, g := range cm.gateways {
			gateways = append(gateways, g.Address)
			isGateway[g.Address] = true
		}
		sort.Strings(gateways)
		for _, dev := range cm.unit.ModelRegistry.List() {
			if dev == nil || dev.Interface != hmenum.InterfaceBidCosRF || isGateway[dev.Address] {
				continue
			}
			var current string
			var roaming bool
			if cm.unit.DescRegistry != nil {
				if desc, ok := cm.unit.DescRegistry.Get(hmtypes.ParseWireInterfaceID(dev.InterfaceID), dev.Address); ok {
					current = desc.Interface
					roaming = desc.Roaming != nil && *desc.Roaming
				}
			}
			row, hasRow := cm.matrix[dev.Address]
			v := receiverVerdict(roaming, row, hasRow, gateways, current, marginDB)
			out = append(out, interfaces.ReceiverProposal{
				Address:          dev.Address,
				Name:             dev.Name(),
				Central:          cm.unit.Name(),
				CurrentInterface: current,
				BestInterface:    v.best,
				CurrentRxDBm:     v.currentRx,
				BestRxDBm:        v.bestRx,
				Roaming:          roaming,
				Verdict:          v.verdict,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Central != out[j].Central {
			return out[i].Central < out[j].Central
		}
		return out[i].Address < out[j].Address
	})
	return out, nil
}

// verdictResult is the outcome of [receiverVerdict].
type verdictResult struct {
	verdict   string
	best      string
	currentRx *int
	bestRx    *int
}

// receiverVerdict decides one device's proposal from its matrix row.
//
// The quantity compared is how strongly each gateway hears the device —
// the uplink the device's transmissions depend on. In a device's row the
// entry for gateway G carries it at index 1 (G hears the device).
//
// The checks run in a fixed order: a roaming device is left to the daemon;
// a device without a row was never measured; a row without any gateway
// reading means no gateway hears the device. The best gateway is the one
// with the strongest reading, a tie going to the current gateway so an
// equal reading never proposes a move. A current gateway with no reading
// counts as minus infinity: any gateway that does hear the device then
// proposes a switch regardless of the margin. Otherwise an advantage of at
// least marginDB proposes a switch, a smaller one is marginal.
func receiverVerdict(
	roaming bool, row map[string][2]int, hasRow bool, gateways []string, current string, marginDB int,
) verdictResult {
	var res verdictResult
	if hasRow {
		for _, g := range gateways {
			pair, ok := row[g]
			if !ok || pair[1] == backends.RSSINoInformation {
				continue
			}
			rx := pair[1]
			if g == current {
				res.currentRx = &rx
			}
			if res.bestRx == nil || rx > *res.bestRx || (rx == *res.bestRx && g == current) {
				res.best, res.bestRx = g, &rx
			}
		}
	}
	switch {
	case roaming:
		res.verdict = interfaces.ReceiverRoaming
	case !hasRow:
		res.verdict = interfaces.ReceiverUnmeasured
	case res.bestRx == nil:
		res.verdict = interfaces.ReceiverUnheard
	case res.best == current:
		res.verdict = interfaces.ReceiverKeep
	case res.currentRx == nil, *res.bestRx-*res.currentRx >= marginDB:
		res.verdict = interfaces.ReceiverSwitch
	default:
		res.verdict = interfaces.ReceiverMarginal
	}
	return res
}

// rssiReading maps one matrix value to the service shape: nil for the
// daemon's no-information marker, the dBm value otherwise.
func rssiReading(v int) *int {
	if v == backends.RSSINoInformation {
		return nil
	}
	return &v
}
