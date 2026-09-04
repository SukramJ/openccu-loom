// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestForcedSensorIdentityIsOneStringAcrossPlanes pins that the
// "_sensor" disambiguation suffix a demoted parameter carries is
// applied on every plane that spells the data point's identity.
//
// Three sites append it independently — the model's internal
// identity ([datapoint.BaseDataPointFields.UniqueID]), the external
// key the north boundary emits
// ([generic.DataPoint.CanonicalUniqueID]) and the MQTT discovery
// unique_id — and the MQTT one derives it from the (model, parameter)
// predicate rather than from the mark the model carries. Nothing
// compared the three, so a plane that stopped appending the suffix
// would keep publishing: HA would simply see a second entity beside
// the one it already adopted under the suffixed key.
//
// The device is an HmIP-eTRV, whose writable LEVEL is the surface
// [generic.IsForceSensorParameter] demotes; the mark is applied the way
// production applies it, through [visibility.ApplyForceSensorMarks]
// over the whole device.
func TestForcedSensorIdentityIsOneStringAcrossPlanes(t *testing.T) {
	t.Parallel()

	const (
		iface       = "HmIP-RF"
		deviceAddr  = "0001ETRV"
		channelAddr = deviceAddr + ":1"
		model       = "HmIP-eTRV-2"
	)

	dev := device.New(device.Config{
		InterfaceID:  iface,
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      deviceAddr,
		Model:        model,
		Name:         "Heizung",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	ch := dev.AddChannel(channelAddr, 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    iface,
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	ch.Put(level)

	visibility.ApplyForceSensorMarks(dev)

	if !level.IsForcedSensor() {
		t.Fatal("ApplyForceSensorMarks left the eTRV LEVEL unmarked; the rest of this test measures nothing")
	}

	// The model's two identities. CanonicalUniqueID is called with an
	// empty serial suffix, the same value the MQTT builder resolves for
	// a central it has no serial for.
	externalID := level.CanonicalUniqueID("")
	internalID := level.UniqueID()

	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	_, _, _, buf, ok := db.Build(Event{
		Central:       "ccu",
		Interface:     iface,
		DeviceAddress: deviceAddr,
		ChannelNo:     1,
		Model:         model,
		Parameter:     string(hmenum.ParameterLevel),
		Category:      hmenum.DataPointCategorySensor,
		Device:        dev,
	})
	if !ok {
		t.Fatal("discovery builder refused the forced-sensor LEVEL event")
	}
	var body map[string]any
	if err := json.Unmarshal(buf, &body); err != nil {
		t.Fatalf("discovery payload is not JSON: %v", err)
	}
	mqttID, _ := body["unique_id"].(string)

	for _, id := range []struct {
		plane string
		value string
	}{
		{"model internal UniqueID", internalID},
		{"model CanonicalUniqueID", externalID},
		{"MQTT discovery unique_id", mqttID},
	} {
		if !strings.HasSuffix(id.value, datapoint.ForcedSensorSuffix) {
			t.Errorf("%s = %q, want the %q suffix a forced sensor carries",
				id.plane, id.value, datapoint.ForcedSensorSuffix)
		}
	}
	if mqttID != externalID {
		t.Errorf("MQTT discovery unique_id = %q, external model identity = %q; the two planes spell different entities",
			mqttID, externalID)
	}
}
