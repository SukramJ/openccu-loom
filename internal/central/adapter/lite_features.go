// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// liteRequirement is what an openccu-lite system needs for one feature.
type liteRequirement struct {
	// never marks a feature the system does not have at all.
	never bool
	// scope is the credential scope that grants the feature; empty means
	// every credential has it.
	scope string
}

// liteFeatureTable is what openccu-lite offers and which scope grants it.
// It is data taken from the box's documented scope and method-tier
// tables: the lite-rpc tiers for device operations, the metadata scopes
// for names and rooms, the system scopes for management. Features that
// need ReGa (system variables, programs, the inbox, alarm messages, the
// com-test, the astro position, safe mode) or an acknowledge the box does
// not offer are never available. Every hmenum.Feature has a row
// (TestLiteFeatureTableCoversEveryKey).
var liteFeatureTable = map[hmenum.Feature]liteRequirement{
	hmenum.FeatureHubSysvars:              {never: true},
	hmenum.FeatureHubPrograms:             {never: true},
	hmenum.FeatureHubAlarmMessages:        {never: true},
	hmenum.FeatureHubInbox:                {never: true},
	hmenum.FeatureHubServiceMessages:      {scope: occulited.ScopeSystemRead},
	hmenum.FeatureHubServiceMessagesAck:   {never: true},
	hmenum.FeatureHubServiceMessagesMute:  {scope: occulited.ScopeRPCAdmin},
	hmenum.FeatureHubSystemUpdate:         {scope: occulited.ScopeSystemRead},
	hmenum.FeatureHubSystemUpdateInstall:  {scope: occulited.ScopePower},
	hmenum.FeatureSystemBackupCreate:      {scope: occulited.ScopeBackup},
	hmenum.FeatureSystemBackupRestore:     {scope: occulited.ScopePower},
	hmenum.FeatureSystemReboot:            {scope: occulited.ScopePower},
	hmenum.FeatureSystemPowerOff:          {scope: occulited.ScopePower},
	hmenum.FeatureSystemRecoveryMode:      {scope: occulited.ScopePower},
	hmenum.FeatureSystemSafeMode:          {never: true},
	hmenum.FeatureSystemPosition:          {never: true},
	hmenum.FeatureSystemAuthDelegation:    {}, // uses the operator's own account credentials
	hmenum.FeatureDeviceControl:           {scope: occulited.ScopeRPCOperate},
	hmenum.FeatureDeviceConfigure:         {scope: occulited.ScopeRPCConfigure},
	hmenum.FeatureDeviceAdmin:             {scope: occulited.ScopeRPCAdmin},
	hmenum.FeatureDeviceFirmwareUpdate:    {scope: occulited.ScopeRPCAdmin},
	hmenum.FeatureDeviceCommunicationTest: {never: true},
	hmenum.FeatureDeviceRename:            {scope: occulited.ScopeMetaWrite},
	hmenum.FeatureTaxonomyRead:            {scope: occulited.ScopeMetaRead},
	hmenum.FeatureTaxonomyAssign:          {scope: occulited.ScopeMetaWrite},
	hmenum.FeatureTaxonomyEdit:            {scope: occulited.ScopeMetaWrite},
	hmenum.FeatureTaxonomyTree:            {scope: occulited.ScopeMetaWrite},
	hmenum.FeatureHeatingGroupsRead:       {scope: occulited.ScopeSystemRead},
	hmenum.FeatureHeatingGroupsWrite:      {scope: occulited.ScopeSystemWrite},
	hmenum.FeatureInstallMode:             {scope: occulited.ScopeRPCConfigure},
	hmenum.FeatureInstallModeLocal:        {scope: occulited.ScopeRPCAdmin},
	hmenum.FeatureRadioDutyCycle:          {scope: occulited.ScopeRPCRead},
	hmenum.FeatureConnectivity:            {scope: occulited.ScopeRPCRead},
}

// liteFeatures derives an openccu-lite central's feature set from the
// scopes its credential is granted (already expanded).
func liteFeatures(granted map[string]bool) central.Features {
	states := make(map[hmenum.Feature]central.FeatureState, len(liteFeatureTable))
	for k, req := range liteFeatureTable {
		switch {
		case req.never:
			states[k] = central.FeatureState{Reason: hmenum.FeatureReasonNotSupported}
		case req.scope == "" || granted[req.scope]:
			states[k] = central.FeatureState{Available: true}
		default:
			states[k] = central.FeatureState{Reason: hmenum.FeatureReasonMissingScope, Scope: req.scope}
		}
	}
	return central.NewFeatures(hmenum.SystemTypeOpenCCULite, states)
}
