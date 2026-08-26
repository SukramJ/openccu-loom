// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.
//
// The device-profile catalogue. Hand-maintained — see ADR 0063.
//
// It was derived from the reference implementation's registry when this
// project forked it (attribution in docs/attribution.md) and is owned
// here from that point on: a new device is added by hand, and a
// deviation from what the reference states is recorded in
// notes/parity/by_design.md together with the wire fact that justifies
// it — the channel and parameter the CCU's own device description
// reports.

package custom

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// intPtr returns a pointer to v. The profile literals use it to
// avoid the awkward `var x = 1; ... &x` pattern at every call site.
func intPtr(v int) *int { return &v }

// RegisterProfiles installs the whole device-profile catalogue onto r.
// Called from [DefaultRegistry] at init() time.
func RegisterProfiles(r *Registry) { //nolint:funlen // one registration literal per device: the catalogue is data, and splitting it would only move the data around
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "263 132",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "263 133",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "263 134",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "263 146",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "263 147",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "alpha-ip-rbg",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "alpha-ip-rbg",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfThermostat"),
		DeviceType:        "bc-rt-trx-cyg",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfThermostat"),
		DeviceType:        "bc-rt-trx-cyn",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfThermostat"),
		DeviceType:        "bc-tc-c-wm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "elv-sh-bs2",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "elv-sh-psmci",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "elv-sh-sw1-bat",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPIrrigationValve"),
		DeviceType:        "elv-sh-wsm",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryValve,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPIrrigationValve")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hbw-lc-rgbww-in6-dr",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 7, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}, {Channel: 9, Role: ChannelRolePrimary}, {Channel: 10, Role: ChannelRolePrimary}, {Channel: 11, Role: ChannelRolePrimary}, {Channel: 12, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				1: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				2: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				3: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				4: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				5: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				6: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer_Color_Fixed"),
		DeviceType:        "hbw-lc-rgbww-in6-dr",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 13, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: map[int]map[hmenum.Field]hmenum.Parameter{
				15: {
					hmenum.FieldColor: hmenum.ParameterColor,
				},
			},
			AdditionalDataPoints: nil,
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("RfDimmer_Color_Fixed")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer_Color_Fixed"),
		DeviceType:        "hbw-lc-rgbww-in6-dr",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 14, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: map[int]map[hmenum.Field]hmenum.Parameter{
				16: {
					hmenum.FieldColor: hmenum.ParameterColor,
				},
			},
			AdditionalDataPoints: nil,
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("RfDimmer_Color_Fixed")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hbw-lc4-in4-dr",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 5, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}, {Channel: 7, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				1: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				2: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				3: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
				4: {
					hmenum.ParameterPressLong,
					hmenum.ParameterPressShort,
					hmenum.ParameterSensor,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfThermostat"),
		DeviceType:        "hm-cc-rt-dn",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("SimpleRfThermostat"),
		DeviceType:        "hm-cc-tc",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("SimpleRfThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfThermostatGroup"),
		DeviceType:        "hm-cc-vg-1",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(999),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfThermostatGroup")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-dw-wm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-ao-sm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-bl1-fm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-bl1-fm-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-bl1-pb-fm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-bl1-sm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-bl1-sm-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-bl1-velux",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-bl1pbu-fm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-blx",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1l-cv",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1l-cv-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1l-pl",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim1l-pl-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1l-pl-3",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1pwm-cv",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1pwm-cv-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1t-cv",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1t-cv-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim1t-dr",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1t-fm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1t-fm-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim1t-fm-lf",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1t-pl",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim1t-pl-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1t-pl-3",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1tpbu-fm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
		DeviceType:        "hm-lc-dim1tpbu-fm-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmerWithVirtChannel")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim2l-cv",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim2l-sm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim2l-sm-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 4, Role: ChannelRolePrimary}, {Channel: 5, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim2t-sm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hm-lc-dim2t-sm-2",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 4, Role: ChannelRolePrimary}, {Channel: 5, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer_Color_Temp"),
		DeviceType:        "hm-lc-dw-wm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 5, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer_Color_Temp")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-ja1pbu-fm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-lc-jax",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer_Color"),
		DeviceType:        "hm-lc-rgbw-wm",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer_Color")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfLock"),
		DeviceType:        "hm-sec-key",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				1: {
					hmenum.ParameterDirection,
					hmenum.ParameterError,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("RfLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hm-sec-win",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				1: {
					hmenum.ParameterDirection,
					hmenum.ParameterWorking,
					hmenum.ParameterError,
				},
				2: {
					hmenum.ParameterLevel,
					hmenum.ParameterStatusValue,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfThermostat"),
		DeviceType:        "hm-tc-it-wm-w-eu",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(999),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RFButtonLock"),
		DeviceType:        "hm-tc-it-wm-w-eu",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          nil,
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RFButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSiren"),
		DeviceType:        "hmip-asir",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySiren,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSiren")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPCover"),
		DeviceType:        "hmip-bbl",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmip-bdt",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPCover"),
		DeviceType:        "hmip-broll",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-bs2",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPFixedColorLight"),
		DeviceType:        "hmip-bsl",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 8, Role: ChannelRolePrimary}, {Channel: 12, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPFixedColorLight")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-bsl",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-bsm",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmip-bwth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmip-bwth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmip-dld",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPLock"),
		DeviceType:        "hmip-dld",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(10),
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				0: {
					hmenum.ParameterErrorJammed,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("IPLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPAccessPermission"),
		DeviceType:        "hmip-dld",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 4, Role: ChannelRolePrimary}, {Channel: 5, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}, {Channel: 7, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}, {Channel: 9, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPAccessPermission")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmip-dlp",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPLock"),
		DeviceType:        "hmip-dlp",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 12, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(14),
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				0: {
					hmenum.ParameterErrorJammed,
					hmenum.ParameterSabotageAcceleration,
					hmenum.ParameterSabotageBattery,
					hmenum.ParameterSabotageMagneticField,
					hmenum.ParameterSabotageVertical,
				},
				3: {
					hmenum.ParameterState,
				},
				4: {
					hmenum.ParameterPermissionState,
				},
				5: {
					hmenum.ParameterPermissionState,
				},
				6: {
					hmenum.ParameterPermissionState,
				},
				7: {
					hmenum.ParameterPermissionState,
				},
				8: {
					hmenum.ParameterPermissionState,
				},
				9: {
					hmenum.ParameterPermissionState,
				},
				10: {
					hmenum.ParameterPermissionState,
				},
				11: {
					hmenum.ParameterPermissionState,
				},
				12: {
					hmenum.ParameterLockStateReason,
				},
				13: {
					hmenum.ParameterAutoRelockState,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("IPLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPCover"),
		DeviceType:        "hmip-drbli4",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 10, Role: ChannelRolePrimary}, {Channel: 14, Role: ChannelRolePrimary}, {Channel: 18, Role: ChannelRolePrimary}, {Channel: 22, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmip-drdi3",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 5, Role: ChannelRolePrimary}, {Channel: 9, Role: ChannelRolePrimary}, {Channel: 13, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDRGDALI"),
		DeviceType:        "hmip-drg-dali",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 4, Role: ChannelRolePrimary}, {Channel: 5, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}, {Channel: 7, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}, {Channel: 9, Role: ChannelRolePrimary}, {Channel: 10, Role: ChannelRolePrimary}, {Channel: 11, Role: ChannelRolePrimary}, {Channel: 12, Role: ChannelRolePrimary}, {Channel: 13, Role: ChannelRolePrimary}, {Channel: 14, Role: ChannelRolePrimary}, {Channel: 15, Role: ChannelRolePrimary}, {Channel: 16, Role: ChannelRolePrimary}, {Channel: 17, Role: ChannelRolePrimary}, {Channel: 18, Role: ChannelRolePrimary}, {Channel: 19, Role: ChannelRolePrimary}, {Channel: 20, Role: ChannelRolePrimary}, {Channel: 21, Role: ChannelRolePrimary}, {Channel: 22, Role: ChannelRolePrimary}, {Channel: 23, Role: ChannelRolePrimary}, {Channel: 24, Role: ChannelRolePrimary}, {Channel: 25, Role: ChannelRolePrimary}, {Channel: 26, Role: ChannelRolePrimary}, {Channel: 27, Role: ChannelRolePrimary}, {Channel: 28, Role: ChannelRolePrimary}, {Channel: 29, Role: ChannelRolePrimary}, {Channel: 30, Role: ChannelRolePrimary}, {Channel: 31, Role: ChannelRolePrimary}, {Channel: 32, Role: ChannelRolePrimary}, {Channel: 33, Role: ChannelRolePrimary}, {Channel: 34, Role: ChannelRolePrimary}, {Channel: 35, Role: ChannelRolePrimary}, {Channel: 36, Role: ChannelRolePrimary}, {Channel: 37, Role: ChannelRolePrimary}, {Channel: 38, Role: ChannelRolePrimary}, {Channel: 39, Role: ChannelRolePrimary}, {Channel: 40, Role: ChannelRolePrimary}, {Channel: 41, Role: ChannelRolePrimary}, {Channel: 42, Role: ChannelRolePrimary}, {Channel: 43, Role: ChannelRolePrimary}, {Channel: 44, Role: ChannelRolePrimary}, {Channel: 45, Role: ChannelRolePrimary}, {Channel: 46, Role: ChannelRolePrimary}, {Channel: 47, Role: ChannelRolePrimary}, {Channel: 48, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPDRGDALI")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-drsi1",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-drsi4",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 6, Role: ChannelRolePrimary}, {Channel: 10, Role: ChannelRolePrimary}, {Channel: 14, Role: ChannelRolePrimary}, {Channel: 18, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmip-etrv",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmip-etrv",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmip-fal",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPCover"),
		DeviceType:        "hmip-fbl",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmip-fdt",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPCover"),
		DeviceType:        "hmip-froll",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-fs6",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-fsi",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-fsm",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPAccessPermission"),
		DeviceType:        "hmip-fwi",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 4, Role: ChannelRolePrimary}, {Channel: 5, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}, {Channel: 7, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPAccessPermission")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPHdm"),
		DeviceType:        "hmip-hdm",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPHdm")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostatGroup"),
		DeviceType:        "hmip-heating",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostatGroup")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPRGBW"),
		DeviceType:        "hmip-lsc",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPRGBW")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPGarage"),
		DeviceType:        "hmip-mod-ho",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPGarage")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-mod-oc8",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 10, Role: ChannelRolePrimary}, {Channel: 14, Role: ChannelRolePrimary}, {Channel: 18, Role: ChannelRolePrimary}, {Channel: 22, Role: ChannelRolePrimary}, {Channel: 26, Role: ChannelRolePrimary}, {Channel: 30, Role: ChannelRolePrimary}, {Channel: 34, Role: ChannelRolePrimary}, {Channel: 38, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPGarage"),
		DeviceType:        "hmip-mod-tm",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPGarage")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSoundPlayerLed"),
		DeviceType:        "hmip-mp3p",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 6, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSoundPlayerLed")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSoundPlayer"),
		DeviceType:        "hmip-mp3p",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySiren,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSoundPlayer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-pcbs",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-pcbs-bat",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-pcbs2",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmip-pdt",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-ps",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPRGBW"),
		DeviceType:        "hmip-rgbw",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPRGBW")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmip-scth230",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 12, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				1: {
					hmenum.ParameterConcentration,
				},
				4: {
					hmenum.ParameterHumidity,
					hmenum.ParameterActualTemperature,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-scth230",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 8, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-smo230",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 10, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				1: {
					hmenum.ParameterIllumination,
					hmenum.ParameterMotion,
					hmenum.ParameterMotionDetectionActive,
					hmenum.ParameterResetMotion,
				},
				2: {
					hmenum.ParameterIllumination,
					hmenum.ParameterMotion,
					hmenum.ParameterMotionDetectionActive,
					hmenum.ParameterResetMotion,
				},
				3: {
					hmenum.ParameterIllumination,
					hmenum.ParameterMotion,
					hmenum.ParameterMotionDetectionActive,
					hmenum.ParameterResetMotion,
				},
				4: {
					hmenum.ParameterIllumination,
					hmenum.ParameterMotion,
					hmenum.ParameterMotionDetectionActive,
					hmenum.ParameterResetMotion,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmip-sth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSirenSmoke"),
		DeviceType:        "hmip-swsd",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySiren,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSirenSmoke")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmip-udi-smi55",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 7, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(10),
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: nil,
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				4: {
					hmenum.ParameterCurrentIllumination,
					hmenum.ParameterIllumination,
					hmenum.ParameterMotion,
					hmenum.ParameterMotionDetectionActive,
					hmenum.ParameterResetMotion,
				},
			},
		},
		Config: ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-usbsm",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-wgc",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmip-wgt",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 8, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmip-wgt",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmip-wgt",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-wgt",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-whs2",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSimpleFixedColorLightWired"),
		DeviceType:        "hmip-wrc6-230",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 12, Role: ChannelRolePrimary}, {Channel: 13, Role: ChannelRolePrimary}, {Channel: 14, Role: ChannelRolePrimary}, {Channel: 15, Role: ChannelRolePrimary}, {Channel: 16, Role: ChannelRolePrimary}, {Channel: 17, Role: ChannelRolePrimary}, {Channel: 18, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSimpleFixedColorLightWired")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmip-wrc6-230",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 9, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPTextDisplay"),
		DeviceType:        "hmip-wrcd",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryTextDisplay,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPTextDisplay")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPIrrigationValve"),
		DeviceType:        "hmip-wsm",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryValve,
		Channels:          []ChannelRoleAssignment{{Channel: 4, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPIrrigationValve")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmip-wth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmip-wth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPCover"),
		DeviceType:        "hmipw-drbl4",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}, {Channel: 10, Role: ChannelRolePrimary}, {Channel: 14, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPDimmer"),
		DeviceType:        "hmipw-drd3",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}, {Channel: 10, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmipw-drs",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 2, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}, {Channel: 10, Role: ChannelRolePrimary}, {Channel: 14, Role: ChannelRolePrimary}, {Channel: 18, Role: ChannelRolePrimary}, {Channel: 22, Role: ChannelRolePrimary}, {Channel: 26, Role: ChannelRolePrimary}, {Channel: 30, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmipw-fal",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSwitch"),
		DeviceType:        "hmipw-fio6",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategorySwitch,
		Channels:          []ChannelRoleAssignment{{Channel: 8, Role: ChannelRolePrimary}, {Channel: 12, Role: ChannelRolePrimary}, {Channel: 16, Role: ChannelRolePrimary}, {Channel: 20, Role: ChannelRolePrimary}, {Channel: 24, Role: ChannelRolePrimary}, {Channel: 28, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSwitch")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmipw-scthd",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmipw-sth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPSimpleFixedColorLightWired"),
		DeviceType:        "hmipw-wrc6",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 7, Role: ChannelRolePrimary}, {Channel: 8, Role: ChannelRolePrimary}, {Channel: 9, Role: ChannelRolePrimary}, {Channel: 10, Role: ChannelRolePrimary}, {Channel: 11, Role: ChannelRolePrimary}, {Channel: 12, Role: ChannelRolePrimary}, {Channel: 13, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPSimpleFixedColorLightWired")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "hmipw-wth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: intPtr(1),
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPButtonLock"),
		DeviceType:        "hmipw-wth",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPButtonLock")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "hmw-lc-bl1",
		ProductGroup:      hmenum.ProductGroupHmW,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hmw-lc-dim1l-dr",
		ProductGroup:      hmenum.ProductGroupHmW,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "hss-dx",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfDimmer"),
		DeviceType:        "oligo.smart.iq.hm",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryLight,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}, {Channel: 2, Role: ChannelRolePrimary}, {Channel: 3, Role: ChannelRolePrimary}, {Channel: 4, Role: ChannelRolePrimary}, {Channel: 5, Role: ChannelRolePrimary}, {Channel: 6, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfDimmer")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("IPThermostat"),
		DeviceType:        "thermostat aa",
		ProductGroup:      hmenum.ProductGroupUnknown,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("IPThermostat")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("RfCover"),
		DeviceType:        "zel stg rm fep 230v",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryCover,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("RfCover")],
	})
	r.MustRegister(Profile{
		Name:              hmenum.DeviceProfile("SimpleRfThermostat"),
		DeviceType:        "zel stg rm fwt",
		ProductGroup:      hmenum.ProductGroupHM,
		Category:          hmenum.DataPointCategoryClimate,
		Channels:          []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		ScheduleChannelNo: nil,
		Extended:          nil,
		Config:            ProfileConfigs[hmenum.DeviceProfile("SimpleRfThermostat")],
	})
}
