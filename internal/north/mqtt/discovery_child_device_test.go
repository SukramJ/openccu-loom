// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	hacatalog "sukramj.dev/go-ha-catalog"
	hadiscovery "sukramj.dev/go-hamqtt/discovery"
)

// childDeviceBlock is the `device` block of a per-entity discovery config,
// read for where the entity sits.
type childDeviceBlock struct {
	Identifiers []string `json:"identifiers"`
	ViaDevice   string   `json:"via_device"`
	ChildDevice *struct {
		Identifiers []string `json:"identifiers"`
		Name        string   `json:"name"`
	} `json:"child_device"`
}

func deviceBlockOf(t *testing.T, item DiscoveryItem) childDeviceBlock {
	t.Helper()
	if !item.OK {
		t.Fatal("the builder returned no entity")
	}
	var body struct {
		Device childDeviceBlock `json:"device"`
	}
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if err := ValidateDiscoveryBody(item.Component, item.Payload); err != nil {
		t.Errorf("the payload does not validate: %v", err)
	}
	return body.Device
}

func childDeviceScheduleEvent() ScheduleEntityEvent {
	return ScheduleEntityEvent{
		Central: "ccu-01", Interface: "HmIP-RF", DeviceAddress: "0003CAFE",
		ChannelNo: 1, DeviceName: "Heizung", Model: "HmIP-eTRV-2",
	}
}

func childDeviceScheduleSwitchEvent() ScheduleSwitchEvent {
	return ScheduleSwitchEvent{
		Central: "ccu-01", Interface: "HmIP-RF", DeviceAddress: "0003CAFE",
		ScheduleChannelNo: 1, DeviceName: "Heizung", Model: "HmIP-eTRV-2",
		Key: "1_1", TargetChannelNo: 2, Label: "Zeitplan Kanal 2",
	}
}

// childDeviceCombinedEvent is a combined entity on channel 3 of a device
// that reports channel groups. grouped says whether channel 3 is in one.
func childDeviceCombinedEvent(grouped bool) CombinedEvent {
	ev := CombinedEvent{
		Central: "ccu-01", Interface: "HmIP-RF", DeviceAddress: "0003CAFE",
		ChannelNo: 3, DeviceName: "Heizung", Model: "HmIP-eTRV-2",
		Device: fakeSubDeviceParent{hasSubs: true},
		Kind:   "duration",
		Component: hadiscovery.Component{
			Platform:     hacatalog.PlatformNumber,
			Name:         "Zeitdauer",
			CommandTopic: "gh/set/ccu-01/HmIP-RF/0003CAFE/3/combined/duration",
		},
	}
	if grouped {
		ev.Channel = fakeSubDeviceChannel{groupNo: 2, multiGroup: true, subName: "Kreis 2"}
	} else {
		ev.ChannelNo = 7
	}
	return ev
}

// TestScheduleBuildersFollowTheSubDeviceOption pins the schedule's device
// through the two builders that publish it: with sub-devices on, the block is
// the physical device's and the schedule is its child; off, the schedule is a
// device of its own linked with via_device. The identifier is the same in
// both, which is what Home Assistant converts the existing device by.
func TestScheduleBuildersFollowTheSubDeviceOption(t *testing.T) {
	t.Parallel()

	physical := physicalDeviceIdentifier("ccu-01", "0003CAFE")
	schedule := physical + "_schedule"
	builders := map[string]func(*DefaultDiscoveryBuilder) DiscoveryItem{
		"schedule sensor": func(d *DefaultDiscoveryBuilder) DiscoveryItem {
			return d.BuildScheduleEntityDiscovery("ccu-01", childDeviceScheduleEvent())
		},
		"schedule switch": func(d *DefaultDiscoveryBuilder) DiscoveryItem {
			return d.BuildScheduleSwitchDiscovery("ccu-01", childDeviceScheduleSwitchEvent())
		},
	}
	for name, build := range builders {
		t.Run(name+"/on", func(t *testing.T) {
			t.Parallel()
			d := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01").WithSubDevices(true)
			block := deviceBlockOf(t, build(d))
			if !reflect.DeepEqual(block.Identifiers, []string{physical}) {
				t.Errorf("device identifiers = %v, want the physical device %q", block.Identifiers, physical)
			}
			if block.ChildDevice == nil {
				t.Fatal("no child_device: the schedule is not a child of its device")
			}
			if !reflect.DeepEqual(block.ChildDevice.Identifiers, []string{schedule}) {
				t.Errorf("child identifiers = %v, want %q", block.ChildDevice.Identifiers, schedule)
			}
			if block.ChildDevice.Name != "Schedule" {
				t.Errorf("child name = %q, want the bare label", block.ChildDevice.Name)
			}
		})
		t.Run(name+"/off", func(t *testing.T) {
			t.Parallel()
			d := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
			block := deviceBlockOf(t, build(d))
			if block.ChildDevice != nil {
				t.Errorf("child_device = %+v with the option off", block.ChildDevice)
			}
			if !reflect.DeepEqual(block.Identifiers, []string{schedule}) {
				t.Errorf("device identifiers = %v, want the schedule device %q", block.Identifiers, schedule)
			}
			if block.ViaDevice != physical {
				t.Errorf("via_device = %q, want the physical device %q", block.ViaDevice, physical)
			}
		})
	}
}

// TestCombinedEntityOfAGroupedChannelSitsOnItsChildDevice: a combined entity
// belongs to a channel, so it sits where the channel's other entities sit —
// on the channel group's child device. Without a group, or with the option
// off, it stays on the physical device.
func TestCombinedEntityOfAGroupedChannelSitsOnItsChildDevice(t *testing.T) {
	t.Parallel()

	physical := physicalDeviceIdentifier("ccu-01", "0003CAFE")
	on := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01").WithSubDevices(true)
	off := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")

	grouped := deviceBlockOf(t, on.BuildCombinedDiscovery("ccu-01", childDeviceCombinedEvent(true)))
	if !reflect.DeepEqual(grouped.Identifiers, []string{physical}) {
		t.Errorf("device identifiers = %v, want the physical device %q", grouped.Identifiers, physical)
	}
	if grouped.ChildDevice == nil {
		t.Fatal("no child_device for a combined entity of a grouped channel")
	}
	if want := []string{physical + "-2"}; !reflect.DeepEqual(grouped.ChildDevice.Identifiers, want) {
		t.Errorf("child identifiers = %v, want %v", grouped.ChildDevice.Identifiers, want)
	}

	if block := deviceBlockOf(t, on.BuildCombinedDiscovery("ccu-01", childDeviceCombinedEvent(false))); block.ChildDevice != nil {
		t.Errorf("child_device = %+v for a channel outside a group", block.ChildDevice)
	}
	if block := deviceBlockOf(t, off.BuildCombinedDiscovery("ccu-01", childDeviceCombinedEvent(true))); block.ChildDevice != nil {
		t.Errorf("child_device = %+v with the option off", block.ChildDevice)
	}
}

// publishedDeviceDocument is a device document as the broker receives it.
type publishedDeviceDocument struct {
	Device       map[string]json.RawMessage `json:"device"`
	Components   map[string]json.RawMessage `json:"components"`
	ChildDevices []struct {
		Identifiers []string                   `json:"identifiers"`
		Name        string                     `json:"name"`
		Components  map[string]json.RawMessage `json:"components"`
	} `json:"child_devices"`
}

func (d publishedDeviceDocument) identifiers(t *testing.T) []string {
	t.Helper()
	var ids []string
	if err := json.Unmarshal(d.Device["identifiers"], &ids); err != nil {
		t.Fatalf("device identifiers: %v", err)
	}
	return ids
}

// lastBundleDocument is the last device document the bridge published, and
// every document in its store validated against the Home Assistant schema.
func lastBundleDocument(t *testing.T, b *Bridge, pub *mockPublisher) publishedDeviceDocument {
	t.Helper()
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.sent) == 0 {
		t.Fatal("nothing was published")
	}
	var doc publishedDeviceDocument
	if err := json.Unmarshal([]byte(pub.sent[len(pub.sent)-1].payload), &doc); err != nil {
		t.Fatalf("unmarshal document: %v", err)
	}
	for _, node := range b.bundles.Nodes() {
		bundle, ok := b.bundles.Bundle(node)
		if !ok {
			t.Fatalf("no bundle for node %s", node)
		}
		if err := hadiscovery.Validate(bundle); err != nil {
			t.Errorf("the document of %s does not validate: %v", node, err)
		}
	}
	return doc
}

// TestBundleDocumentListsTheBuildersChildDevices runs the path a device
// document takes in production — builder, bridge, bundle store — with
// sub-devices on. A per-entity config says "on that child" inside its device
// block; the document must say it by listing the component under the child
// and must not carry `child_device` in its own block, which Home Assistant
// refuses together with the whole document. The negative control is the same
// three entities with the option off.
func TestBundleDocumentListsTheBuildersChildDevices(t *testing.T) {
	t.Parallel()

	physical := physicalDeviceIdentifier("ccu-01", "0003CAFE")
	publish := func(t *testing.T, subDevices bool) publishedDeviceDocument {
		t.Helper()
		b, pub := newTestBridge(t, func(c *BridgeConfig) {
			c.HADiscoveryBundles = true
			c.SubDevicesEnabled = subDevices
		})
		ctx := context.Background()
		// A child's entity first: the store keeps the first device block it
		// sees, so this is the order in which a block that arrived with
		// child_device in it becomes the document's.
		if err := b.PublishCombinedDiscovery(ctx, "ccu-01", childDeviceCombinedEvent(true)); err != nil {
			t.Fatalf("publish the grouped entity: %v", err)
		}
		if err := b.PublishScheduleEntityDiscovery(ctx, "ccu-01", childDeviceScheduleEvent()); err != nil {
			t.Fatalf("publish the schedule: %v", err)
		}
		if err := b.PublishCombinedDiscovery(ctx, "ccu-01", childDeviceCombinedEvent(false)); err != nil {
			t.Fatalf("publish the ungrouped entity: %v", err)
		}
		return lastBundleDocument(t, b, pub)
	}

	t.Run("on", func(t *testing.T) {
		t.Parallel()
		doc := publish(t, true)
		if _, kept := doc.Device["child_device"]; kept {
			t.Error("the document's device block carries child_device")
		}
		if ids := doc.identifiers(t); !reflect.DeepEqual(ids, []string{physical}) {
			t.Errorf("device identifiers = %v, want the physical device %q", ids, physical)
		}
		if len(doc.Components) != 1 {
			t.Errorf("%d components on the device itself, want the one outside a group", len(doc.Components))
		}
		children := map[string]int{}
		for _, child := range doc.ChildDevices {
			if len(child.Identifiers) != 1 {
				t.Fatalf("child identifiers = %v, want exactly one", child.Identifiers)
			}
			children[child.Identifiers[0]] = len(child.Components)
		}
		want := map[string]int{physical + "-2": 1, physical + "_schedule": 1}
		if !reflect.DeepEqual(children, want) {
			t.Errorf("child devices and their component counts = %v, want %v", children, want)
		}
	})

	t.Run("off", func(t *testing.T) {
		t.Parallel()
		doc := publish(t, false)
		if len(doc.ChildDevices) != 0 {
			t.Errorf("child_devices = %+v with the option off", doc.ChildDevices)
		}
		if len(doc.Components) != 3 {
			t.Errorf("%d components, want all three on the document's device", len(doc.Components))
		}
	})
}

// TestBundleDeviceIsThePhysicalDeviceWhicheverEntityComesFirst: a document
// has room for one device. With sub-devices off the schedule's per-entity
// block describes a device of its own, linked to the physical one with
// via_device; were that block kept because it arrived first, every entity of
// the device would move onto "<device> Schedule". The device a block hangs
// on wins over the block that hangs on it, in either order.
func TestBundleDeviceIsThePhysicalDeviceWhicheverEntityComesFirst(t *testing.T) {
	t.Parallel()

	physical := physicalDeviceIdentifier("ccu-01", "0003CAFE")
	schedule := func(ctx context.Context, b *Bridge) error {
		return b.PublishScheduleEntityDiscovery(ctx, "ccu-01", childDeviceScheduleEvent())
	}
	entity := func(ctx context.Context, b *Bridge) error {
		return b.PublishCombinedDiscovery(ctx, "ccu-01", childDeviceCombinedEvent(false))
	}
	orders := map[string][]func(context.Context, *Bridge) error{
		"schedule first": {schedule, entity},
		"entity first":   {entity, schedule},
	}
	for name, steps := range orders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, pub := newTestBridge(t, func(c *BridgeConfig) { c.HADiscoveryBundles = true })
			for _, step := range steps {
				if err := step(context.Background(), b); err != nil {
					t.Fatalf("publish: %v", err)
				}
			}
			doc := lastBundleDocument(t, b, pub)
			if ids := doc.identifiers(t); !reflect.DeepEqual(ids, []string{physical}) {
				t.Errorf("device identifiers = %v, want the physical device %q", ids, physical)
			}
			if len(doc.Components) != 2 {
				t.Errorf("%d components, want both entities", len(doc.Components))
			}
		})
	}
}
