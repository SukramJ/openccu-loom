//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package integration

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// skippedPrimaries names the primary channels of the simulated fleet that the
// materializer skips on purpose, with the wire fact behind each. The guard reports
// an entry that no longer applies, so one cannot outlive its reason.
var skippedPrimaries = map[string]string{
	"HB-LC-Bl1-Velux/RfCover/2": "the entry hm-lc-bl1-velux, which the model normalises to, names " +
		"channels 1 and 2 and also serves HB-LC-Bl1-Velux-2ch; this description has channel 1 only",
}

// TestCatalogueFitsEveryFixture walks the whole simulated fleet through the
// production ingest and asserts that every primary channel the catalogue names
// exists on the device with a type the role accepts.
//
// The materializer skips a candidate that does not fit, which is right for a model
// variant and silent for a wrong catalogue entry: a typo in an accepted type, or a
// model reporting a type nobody declared, would make the device lose its custom
// data point without an error. This is the check that keeps that from going
// unnoticed (ADR 0087).
func TestCatalogueFitsEveryFixture(t *testing.T) {
	srv := startMockCCUWithDevices(t, snapshotDevices(t))
	xmlClient := newXMLRPCClient(t, srv.URL())
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: xmlClient}, nil, nil)

	c, err := central.New(central.Config{Name: "catalogue-fit-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("translations: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).
		WithTranslations(translations, snapshotLocale()).
		WithVisibility(visibility.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := pipeline.IngestFromBackend(
		ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, slog.New(slog.DiscardHandler),
	); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("the fleet hydrated no devices — the walk is broken and this test would pass vacuously")
	}

	registry := custom.DefaultRegistry()
	used := map[string]bool{}
	checked := 0
	for _, dev := range devices {
		types := map[int]string{}
		for _, ch := range dev.Channels() {
			types[ch.Number] = ch.Type
		}
		for _, profile := range registry.GetConfigs(dev.Model) {
			if profile.Config == nil || !profile.Config.ChannelGroup.PrimaryChannelSet {
				continue
			}
			cg := profile.Config.ChannelGroup
			for _, base := range profile.Channels {
				checked++
				primary := base.Channel + cg.PrimaryChannel
				channelType, carried := types[primary]
				if carried && slices.Contains(cg.PrimaryChannelTypes, channelType) {
					continue
				}
				key := fmt.Sprintf("%s/%s/%d", dev.Model, profile.Name, primary)
				if _, declared := skippedPrimaries[key]; declared {
					used[key] = true
					continue
				}
				t.Errorf("%s: primary channel %d of %s does not fit: carried=%v type=%q accepted=%v",
					dev.Model, primary, profile.Name, carried, channelType, cg.PrimaryChannelTypes)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no primary channel checked — the walk is broken and this test would pass vacuously")
	}
	if len(snapshotDevices(t)) == 0 {
		for key := range skippedPrimaries {
			if !used[key] {
				t.Errorf("skippedPrimaries names %s, which no longer applies", key)
			}
		}
	}
	t.Logf("checked %d primary channels across %d devices", checked, len(devices))
}
