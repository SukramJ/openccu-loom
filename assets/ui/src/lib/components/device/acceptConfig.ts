// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// First-time configuration sent with an inbox accept. Shared by the inbox's
// full accept dialog and the add-device dialog's inline accept, so both
// send the same body for the same input.
export type AcceptConfig = {
  name?: string;
  include_channels?: boolean;
  rooms?: string[];
  functions?: string[];
};

// Carries only the fields the operator set, so an untouched field stays
// untouched on the CCU. Renaming the channels only means something together
// with a name. Undefined means a plain accept.
export function buildAcceptConfig(input: {
  name: string;
  includeChannels: boolean;
  rooms?: string[];
  functions?: string[];
}): AcceptConfig | undefined {
  const config: AcceptConfig = {};
  const name = input.name.trim();
  if (name) {
    config.name = name;
    if (input.includeChannels) config.include_channels = true;
  }
  if (input.rooms && input.rooms.length > 0) config.rooms = input.rooms;
  if (input.functions && input.functions.length > 0) config.functions = input.functions;
  return Object.keys(config).length > 0 ? config : undefined;
}

// The operator's input for one device's first-time configuration, as the
// shared fields (AcceptConfigFields.svelte) edit it.
export type AcceptDraft = {
  name: string;
  includeChannels: boolean;
  rooms: string[];
  functions: string[];
};

export function emptyAcceptDraft(): AcceptDraft {
  return { name: "", includeChannels: false, rooms: [], functions: [] };
}
