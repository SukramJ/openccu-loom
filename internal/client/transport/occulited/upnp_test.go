// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

func TestUPnPFromTheFake(t *testing.T) {
	f := startFake(t, litefake.Options{Serial: "3014F711A0001F", Hostname: "box"})
	d, err := newClient(t, f.URL(), "").UPnP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.SerialNumber != "3014F711A0001F" || d.ModelName != "openccu-lite" || d.FriendlyName != "box" ||
		d.UDN != "uuid:upnp-BasicDevice-1_0-3014F711A0001F" {
		t.Errorf("device %+v", d)
	}
}

func TestParseUPnPRefusesBrokenDocuments(t *testing.T) {
	for _, doc := range []string{
		"<html>not xml",
		`<root xmlns="urn:schemas-upnp-org:device-1-0"><device><modelName>openccu-lite</modelName></device></root>`,
	} {
		if _, err := occulited.ParseUPnP(strings.NewReader(doc)); !errors.Is(err, occulited.ErrProtocol) {
			t.Errorf("%q: %v", doc, err)
		}
	}
}
