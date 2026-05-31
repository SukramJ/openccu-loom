// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// HubValueType names the value kinds the CCU's hub (sysvars, programs)
// can carry.
type HubValueType string

// HubValueType values.
const (
	HubValueTypeAlarm   HubValueType = "ALARM"
	HubValueTypeFloat   HubValueType = "FLOAT"
	HubValueTypeInteger HubValueType = "INTEGER"
	HubValueTypeList    HubValueType = "LIST"
	HubValueTypeLogic   HubValueType = "LOGIC"
	HubValueTypeNumber  HubValueType = "NUMBER"
	HubValueTypeString  HubValueType = "STRING"
)

// String returns the wire representation.
func (t HubValueType) String() string { return string(t) }

// ProgramTrigger tags what caused a program to run.
type ProgramTrigger string

// ProgramTrigger values.
const (
	ProgramTriggerAPI        ProgramTrigger = "api"
	ProgramTriggerUser       ProgramTrigger = "user"
	ProgramTriggerScheduler  ProgramTrigger = "scheduler"
	ProgramTriggerAutomation ProgramTrigger = "automation"
)

// String returns the wire representation.
func (t ProgramTrigger) String() string { return string(t) }

// InternalCustomID tags synthetic CCU objects the library creates.
type InternalCustomID string

// InternalCustomID values.
const (
	InternalCustomIDDefault  InternalCustomID = "cid_default"
	InternalCustomIDLinkPeer InternalCustomID = "cid_link_peer"
	InternalCustomIDManuTemp InternalCustomID = "cid_manu_temp"
)

// String returns the wire representation.
func (i InternalCustomID) String() string { return string(i) }
