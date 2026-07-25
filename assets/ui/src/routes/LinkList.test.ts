// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

const mockListAllLinks = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    listAllLinks: (...args: unknown[]) => mockListAllLinks(...args),
  },
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import LinkList from "./LinkList.svelte";

const LINKS = [
  {
    sender_address: "DEVA:1",
    receiver_address: "PEERA:3",
    sender_device_name: "Taster Flur",
    receiver_device_name: "Deckenlampe",
    name: "Flur → Licht",
    central_name: "ccu-a",
    interface_id: "ccu-a-HmIP-RF",
    direction: "",
    peer_address: "",
  },
  {
    sender_address: "DEVB:1",
    receiver_address: "PEERB:2",
    central_name: "ccu-b",
    interface_id: "ccu-b-BidCos-RF",
    direction: "",
    peer_address: "",
  },
];

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => cleanup());

describe("LinkList — rendering", () => {
  it("renders every link with its party names and a device-edit deep link", async () => {
    mockListAllLinks.mockResolvedValue(LINKS);

    render(LinkList, { props: { locale: "en" } });

    await waitFor(() => {
      expect(screen.getByText("Taster Flur")).toBeInTheDocument();
    });

    expect(mockListAllLinks).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Deckenlampe")).toBeInTheDocument();
    // The sender's device (address before the ':') deep-links straight to its
    // Direct-links tab, not just the device detail.
    const edit = screen.getAllByRole("link").find(
      (a) => a.getAttribute("href") === "#/devices/DEVA?tab=links",
    );
    expect(edit).toBeTruthy();
    // Both centrals are shown as badges when more than one is present.
    expect(screen.getByText("ccu-a-HmIP-RF")).toBeInTheDocument();
    expect(screen.getByText("ccu-b-BidCos-RF")).toBeInTheDocument();
  });
});

describe("LinkList — empty state", () => {
  it("shows the empty-state message when no links exist", async () => {
    mockListAllLinks.mockResolvedValue([]);

    render(LinkList, { props: { locale: "en" } });

    await waitFor(() => {
      expect(screen.getByText("links.empty")).toBeInTheDocument();
    });
  });
});

describe("LinkList — error state", () => {
  it("shows the error state when the load fails", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockListAllLinks.mockRejectedValue(new ApiError(502, null, "ccu unreachable"));

    render(LinkList, { props: { locale: "en" } });

    await waitFor(() => {
      expect(screen.getByText(/ccu unreachable/)).toBeInTheDocument();
    });
  });
});
