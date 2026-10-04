// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";

const { mockList, mockSet } = vi.hoisted(() => ({
  mockList: vi.fn(),
  mockSet: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listInstallModeInterfaces: (...args: unknown[]) => mockList(...args),
    setInstallModeInterface: (...args: unknown[]) => mockSet(...args),
  },
  ApiError: class ApiError extends Error {
    status = 0;
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${Object.values(params).join(",")}` : key,
}));

import { installModeStore } from "./installMode.svelte";

// A mixed fleet: an openccu-lite system and a CCU both expose HmIP-RF, so
// the interface name alone cannot say which radio to open.
const TWO_CENTRALS = [
  { central: "a", interface: "HmIP-RF", active: false, seconds: 0, observed: true },
  { central: "b", interface: "HmIP-RF", active: false, seconds: 0, observed: true },
];

beforeEach(() => {
  vi.clearAllMocks();
});

describe("installModeStore.toggle — central targeting", () => {
  it("opens the install mode on the named central of two same-named interfaces", async () => {
    mockList.mockResolvedValue(TWO_CENTRALS);
    mockSet.mockResolvedValue(TWO_CENTRALS);
    await installModeStore.refresh();

    await installModeStore.toggle({ interface: "HmIP-RF", central: "b" });

    expect(mockSet).toHaveBeenCalledWith(
      "HmIP-RF",
      true,
      expect.objectContaining({ central: "b", seconds: 60 }),
    );
    // With two centrals the banner has to say which one it is about.
    expect(installModeStore.banner).toContain("HmIP-RF · b");
  });

  it("sends the entry's central with a single central too", async () => {
    const one = [TWO_CENTRALS[0]];
    mockList.mockResolvedValue(one);
    mockSet.mockResolvedValue(one);
    await installModeStore.refresh();

    await installModeStore.toggle({ interface: "HmIP-RF" });

    expect(mockSet).toHaveBeenCalledWith(
      "HmIP-RF",
      true,
      expect.objectContaining({ central: "a" }),
    );
    expect(installModeStore.banner).not.toContain("·");
  });

  it("stops the window on the central whose entry is active", async () => {
    const running = [
      TWO_CENTRALS[0],
      { ...TWO_CENTRALS[1], active: true, seconds: 40 },
    ];
    mockList.mockResolvedValue(running);
    mockSet.mockResolvedValue(TWO_CENTRALS);
    await installModeStore.refresh();

    await installModeStore.toggle({ interface: "HmIP-RF", central: "b" });

    // b's entry is the running one, so the toggle is a stop; matching by
    // interface name alone would have read a's idle entry and started.
    expect(mockSet).toHaveBeenCalledWith(
      "HmIP-RF",
      false,
      expect.objectContaining({ central: "b" }),
    );
  });
});
