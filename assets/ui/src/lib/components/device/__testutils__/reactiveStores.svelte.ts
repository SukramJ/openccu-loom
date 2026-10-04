// Test-only stand-ins for the device and install-mode stores, backed by
// runes so a test can change them after render and watch the component
// follow — a plain-object mock is read once and never re-renders.
import { vi } from "vitest";
import type { DeviceSummary, InstallModeInterfaceEntry } from "$lib/api/types";

let items = $state<DeviceSummary[]>([]);
let lastLoaded = $state<Date | null>(new Date());

export const deviceStoreMock = {
  get items() {
    return items;
  },
  set items(v: DeviceSummary[]) {
    items = v;
  },
  get lastLoaded() {
    return lastLoaded;
  },
  set lastLoaded(v: Date | null) {
    lastLoaded = v;
  },
  loading: false,
  error: null,
  refresh: vi.fn().mockResolvedValue(undefined),
  ensureStream: vi.fn(),
  close: vi.fn(),
};

let active = $state(false);
let interfaces = $state<InstallModeInterfaceEntry[]>([]);

export const installModeStoreMock = {
  get active() {
    return active;
  },
  set active(v: boolean) {
    active = v;
  },
  get interfaces() {
    return interfaces;
  },
  set interfaces(v: InstallModeInterfaceEntry[]) {
    interfaces = v;
  },
  remainingSeconds: null,
  busy: false,
  banner: null,
  refresh: vi.fn(),
  toggle: vi.fn(),
  ensurePoll: vi.fn(),
  release: vi.fn(),
};
