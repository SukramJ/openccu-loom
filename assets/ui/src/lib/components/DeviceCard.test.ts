// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";

// Mock the maintenance store — the card calls maintenanceStore.bind()
// and maintenanceStore.all() on render; no live WS needed.
let maintenanceByDevice: Record<string, Record<string, unknown>> = {};

vi.mock("$lib/stores/maintenance.svelte", () => ({
  maintenanceStore: {
    bind: vi.fn(),
    all: () => maintenanceByDevice,
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// Icon is SVG-heavy; stub it out so the test DOM stays simple.
vi.mock("$lib/components/ui/Icon.svelte", () => ({
  default: vi.fn().mockReturnValue({ $$typeof: Symbol("component") }),
}));

import DeviceCard from "./DeviceCard.svelte";
import type { DeviceSummary } from "$lib/api/types";

function makeDevice(overrides: Partial<DeviceSummary> = {}): DeviceSummary {
  return {
    address: "ABC123",
    interface: "HmIP-RF",
    interface_id: "HmIP-RF-ABC123",
    model: "HmIP-PSM",
    name: "Bookshelf lamp",
    available: true,
    channels_count: 5,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  maintenanceByDevice = {};
});

afterEach(() => {
  cleanup();
});

describe("DeviceCard — name and subtitle", () => {
  it("renders the device name as the heading", () => {
    const { getByRole } = render(DeviceCard, {
      props: { device: makeDevice({ name: "Bookshelf lamp" }) },
    });
    expect(getByRole("heading").textContent).toContain("Bookshelf lamp");
  });

  it("falls back to address when name is absent", () => {
    const { getByRole } = render(DeviceCard, {
      props: { device: makeDevice({ name: "" }) },
    });
    expect(getByRole("heading").textContent).toContain("ABC123");
  });

  it("shows model_label as subtitle when set", () => {
    const { getByText } = render(DeviceCard, {
      props: {
        device: makeDevice({
          model: "HmIP-PSM",
          model_label: "Power switch metering",
        }),
      },
    });
    expect(getByText("Power switch metering")).toBeTruthy();
  });

  it("falls back to model when model_label is absent", () => {
    const { getByText } = render(DeviceCard, {
      props: { device: makeDevice({ model: "HmIP-PSM" }) },
    });
    expect(getByText("HmIP-PSM")).toBeTruthy();
  });

  it("renders the channel count alongside the i18n key", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice({ channels_count: 7 }) },
    });
    // Find the metadata <p> among the card's paragraphs — it holds
    // "interface · central · N device.list.channels".
    const paragraphs = Array.from(container.querySelectorAll("p"));
    const metaP = paragraphs.find((p) => p.textContent?.includes("device.list.channels"));
    expect(metaP?.textContent).toContain("7");
  });
});

describe("DeviceCard — availability dot", () => {
  it("renders emerald dot for available device", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice({ available: true }) },
    });
    const dot = container.querySelector(".bg-emerald-500");
    expect(dot).not.toBeNull();
  });

  it("renders slate dot for unavailable device", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice({ available: false }) },
    });
    const dot = container.querySelector(".bg-slate-400");
    expect(dot).not.toBeNull();
  });
});

describe("DeviceCard — rooms", () => {
  it("renders rooms list when provided", () => {
    const { getByText } = render(DeviceCard, {
      props: { device: makeDevice({ rooms: ["Living room", "Kitchen"] }) },
    });
    expect(getByText("Living room, Kitchen")).toBeTruthy();
  });

  it("does not render a room paragraph when rooms is empty", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice({ rooms: [] }) },
    });
    // Living room paragraph should not be present
    const allP = Array.from(container.querySelectorAll("p"));
    const roomP = allP.find((p) => p.textContent?.includes("Living room"));
    expect(roomP).toBeUndefined();
  });
});

describe("DeviceCard — firmware update badge", () => {
  it("renders FW badge when update_available is true", () => {
    const { getByText } = render(DeviceCard, {
      props: { device: makeDevice({ update_available: true }) },
    });
    expect(getByText("FW")).toBeTruthy();
  });

  it("does not render FW badge when no update is available", () => {
    const { queryByText } = render(DeviceCard, {
      props: { device: makeDevice({ update_available: false }) },
    });
    expect(queryByText("FW")).toBeNull();
  });
});

describe("DeviceCard — selection checkbox", () => {
  it("renders a checkbox when onToggleSelect is provided", () => {
    const { container } = render(DeviceCard, {
      props: {
        device: makeDevice(),
        onToggleSelect: vi.fn(),
      },
    });
    const checkbox = container.querySelector('input[type="checkbox"]');
    expect(checkbox).not.toBeNull();
  });

  it("does not render a checkbox when onToggleSelect is absent", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice() },
    });
    const checkbox = container.querySelector('input[type="checkbox"]');
    expect(checkbox).toBeNull();
  });

  it("calls onToggleSelect(true) when checkbox is checked", async () => {
    const onToggleSelect = vi.fn();
    const { container } = render(DeviceCard, {
      props: { device: makeDevice(), selected: false, onToggleSelect },
    });
    const checkbox = container.querySelector(
      'input[type="checkbox"]',
    ) as HTMLInputElement;
    await fireEvent.change(checkbox, { target: { checked: true } });
    expect(onToggleSelect).toHaveBeenCalledWith(true);
  });

  it("calls onToggleSelect(false) when checkbox is unchecked", async () => {
    const onToggleSelect = vi.fn();
    const { container } = render(DeviceCard, {
      props: { device: makeDevice(), selected: true, onToggleSelect },
    });
    const checkbox = container.querySelector(
      'input[type="checkbox"]',
    ) as HTMLInputElement;
    await fireEvent.change(checkbox, { target: { checked: false } });
    expect(onToggleSelect).toHaveBeenCalledWith(false);
  });

  it("reflects the selected prop as ring-2 class on the container", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice(), selected: true, onToggleSelect: vi.fn() },
    });
    const card = container.querySelector(".ring-2");
    expect(card).not.toBeNull();
  });

  it("has no ring-2 when not selected", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice(), selected: false, onToggleSelect: vi.fn() },
    });
    const card = container.querySelector(".ring-2");
    expect(card).toBeNull();
  });
});

describe("DeviceCard — central name", () => {
  it("renders the central name when set", () => {
    const { container } = render(DeviceCard, {
      props: {
        device: makeDevice({ central: "ccu-main", interface: "HmIP-RF" }),
      },
    });
    // The metadata <p> renders "HmIP-RF · ccu-main · N channels"
    const paragraphs = Array.from(container.querySelectorAll("p"));
    const metaP = paragraphs.find((p) => p.textContent?.includes("ccu-main"));
    expect(metaP).not.toBeUndefined();
  });

  it("does not render the · central fragment when central is absent", () => {
    const { container } = render(DeviceCard, {
      props: { device: makeDevice({ central: undefined }) },
    });
    expect(container.textContent).not.toContain("ccu-main");
  });
});
