// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// CacheType identifies one of the daemon's internal caches.
type CacheType string

// CacheType values.
const (
	CacheTypeDeviceDescription   CacheType = "device_description"
	CacheTypeParamsetDescription CacheType = "paramset_description"
	CacheTypeData                CacheType = "data"
	CacheTypeDetails             CacheType = "details"
	CacheTypeVisibility          CacheType = "visibility"
)

// String returns the wire representation.
func (c CacheType) String() string { return string(c) }

// CacheInvalidationReason explains why a cache was invalidated.
type CacheInvalidationReason string

// CacheInvalidationReason values.
const (
	CacheInvalidationReasonDeviceAdded   CacheInvalidationReason = "device_added"
	CacheInvalidationReasonDeviceRemoved CacheInvalidationReason = "device_removed"
	CacheInvalidationReasonDeviceUpdated CacheInvalidationReason = "device_updated"
	CacheInvalidationReasonRefresh       CacheInvalidationReason = "refresh"
	CacheInvalidationReasonManual        CacheInvalidationReason = "manual"
	CacheInvalidationReasonStartup       CacheInvalidationReason = "startup"
	CacheInvalidationReasonShutdown      CacheInvalidationReason = "shutdown"
)

// String returns the wire representation.
func (r CacheInvalidationReason) String() string { return string(r) }

// DataOperationResult reports the outcome of a persistent-store load or
// save.
type DataOperationResult int

// DataOperationResult values.
const (
	DataOperationResultLoadFail        DataOperationResult = 0
	DataOperationResultLoadSuccess     DataOperationResult = 1
	DataOperationResultVersionMismatch DataOperationResult = 2
	DataOperationResultSaveFail        DataOperationResult = 10
	DataOperationResultSaveSuccess     DataOperationResult = 11
	DataOperationResultNoLoad          DataOperationResult = 20
	DataOperationResultNoSave          DataOperationResult = 21
)

// DataRefreshType categorises a coordinator-driven refresh operation.
type DataRefreshType string

// DataRefreshType values.
const (
	DataRefreshTypeAlarmMessages   DataRefreshType = "alarm_messages"
	DataRefreshTypeClientData      DataRefreshType = "client_data"
	DataRefreshTypeConnectivity    DataRefreshType = "connectivity"
	DataRefreshTypeInbox           DataRefreshType = "inbox"
	DataRefreshTypeServiceMessages DataRefreshType = "service_messages"
	DataRefreshTypeMetrics         DataRefreshType = "metrics"
	DataRefreshTypeProgram         DataRefreshType = "program"
	DataRefreshTypeSystemUpdate    DataRefreshType = "system_update"
	DataRefreshTypeSysvar          DataRefreshType = "sysvar"
)

// String returns the wire representation.
func (r DataRefreshType) String() string { return string(r) }

// DataFetchOperation names the type of data-fetch phase that completed.
// Carried in [hmevent.DataFetchCompletedEvent].
type DataFetchOperation string

// DataFetchOperation values.
const (
	// DataFetchOperationFetchDeviceDescriptions is emitted when device
	// descriptions have been fetched and added to the description cache.
	DataFetchOperationFetchDeviceDescriptions DataFetchOperation = "fetch_device_descriptions"
	// DataFetchOperationFetchParamsetDescriptions is emitted when paramset
	// descriptions have been fetched and added to the paramset cache.
	DataFetchOperationFetchParamsetDescriptions DataFetchOperation = "fetch_paramset_descriptions"
)

// String returns the wire representation.
func (o DataFetchOperation) String() string { return string(o) }
