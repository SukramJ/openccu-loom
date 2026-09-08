// @vitest-environment happy-dom
import type { ComponentProps } from "svelte";
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, fireEvent, cleanup, screen, within } from "@testing-library/svelte";

// i18n is mocked to echo keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

vi.mock("$lib/components/ui/Icon.svelte", () => ({
  default: vi.fn().mockReturnValue(null),
}));

import ChannelTable from "./ChannelTable.svelte";
import type { ChannelSummary } from "$lib/api/types";

function channel(overrides: Partial<ChannelSummary> = {}): ChannelSummary {
  return {
    address: "0001ABCD:1",
    number: 1,
    paramset_key: "VALUES",
    data_points_count: 0,
    ...overrides,
  } as ChannelSummary;
}

function props(over: Record<string, unknown> = {}) {
  return {
    channels: [channel()],
    selected: null,
    onSelect: vi.fn(),
    ...over,
  } as unknown as ComponentProps<typeof ChannelTable>;
}

function bodyRows() {
  return within(screen.getByRole("table")).getAllByRole("row").slice(1);
}

afterEach(cleanup);

describe("ChannelTable", () => {
  // The table sorts by channel number itself rather than trusting whatever
  // order the caller hands it: a caller that stops pre-sorting, or an
  // operator who sorts by another column and back, must still get 1, 2, 10 —
  // never the string order that puts 10 between 1 and 2.
  it("sorts out-of-order channels numerically on its own", () => {
    render(
      ChannelTable,
      props({
        channels: [
          channel({ address: "0001ABCD:10", number: 10 }),
          channel({ address: "0001ABCD:2", number: 2 }),
          channel({ address: "0001ABCD:1", number: 1 }),
        ],
      }),
    );
    expect(
      bodyRows().map((r) => within(r).getByText(/^0001ABCD:/).textContent),
    ).toEqual(["0001ABCD:1", "0001ABCD:2", "0001ABCD:10"]);
  });

  it("hands the clicked channel to onSelect", async () => {
    const onSelect = vi.fn();
    render(
      ChannelTable,
      props({
        channels: [
          channel({ address: "0001ABCD:1", number: 1 }),
          channel({ address: "0001ABCD:2", number: 2 }),
        ],
        onSelect,
      }),
    );
    await fireEvent.click(bodyRows()[1]);
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect.mock.calls[0][0].address).toBe("0001ABCD:2");
  });

  // Role is derived from the two raw CCU token lists, and the tokens
  // themselves are never shown — only which side is non-empty.
  it("derives the direct-link role from the raw role token lists", () => {
    render(
      ChannelTable,
      props({
        channels: [
          channel({ address: "0001ABCD:1", number: 1, link_source_roles: ["SWITCH"] }),
          channel({ address: "0001ABCD:2", number: 2, link_target_roles: ["SWITCH"] }),
          channel({
            address: "0001ABCD:3",
            number: 3,
            link_source_roles: ["SWITCH"],
            link_target_roles: ["WEATHER"],
          }),
          channel({ address: "0001ABCD:4", number: 4 }),
        ],
      }),
    );
    const rows = bodyRows();
    expect(within(rows[0]).getByText("device.channel.role.sender")).toBeTruthy();
    expect(within(rows[1]).getByText("device.channel.role.receiver")).toBeTruthy();
    expect(within(rows[2]).getByText("device.channel.role.both")).toBeTruthy();
    expect(within(rows[3]).queryByText("device.channel.role.sender")).toBeNull();
  });

  it("badges a hidden, locked, virtual, week-profile or grouped channel", () => {
    render(
      ChannelTable,
      props({
        channels: [
          channel({
            address: "0001ABCD:52",
            number: 52,
            type: "CLIMATECONTROL_WEEK_PROFILE",
            hidden: true,
            locked: true,
            group_no: 3,
          }),
        ],
      }),
    );
    const row = bodyRows()[0];
    for (const chip of [
      "device.channel.chip.hidden",
      "device.channel.chip.locked",
      "device.channel.chip.virtual",
      "device.channel.chip.week_profile",
      "device.channel.chip.group",
    ]) {
      expect(within(row).getByText(chip), chip).toBeTruthy();
    }
  });

  // "Not loaded yet" and "no links" are different answers; a zero would claim
  // the second while the request is still on the wire.
  it("shows an em dash for a channel the link counts do not cover", () => {
    render(
      ChannelTable,
      props({
        channels: [
          channel({ address: "0001ABCD:1", number: 1 }),
          channel({ address: "0001ABCD:2", number: 2 }),
        ],
        linkCounts: new Map([["0001ABCD:1", 2]]),
      }),
    );
    const rows = bodyRows();
    expect(within(rows[0]).getByText("2")).toBeTruthy();
    expect(within(rows[1]).getAllByText("—").length).toBeGreaterThan(0);
  });

  // The device list reuses the table read-only inside an expanded row, where
  // there is no width for the raw type, the link count or the status chips.
  it("drops the wide columns in compact mode", () => {
    render(ChannelTable, props({ compact: true }));
    // A sortable header wraps its label in a button next to a sort arrow, so
    // the header cell's text is matched by substring rather than equality.
    const headers = within(screen.getByRole("table"))
      .getAllByRole("columnheader")
      .map((h) => h.textContent ?? "");
    const has = (key: string) => headers.some((text) => text.includes(key));
    expect(has("device.channels.col.number")).toBe(true);
    expect(has("device.channels.col.type")).toBe(false);
    expect(has("device.channels.col.links")).toBe(false);
    expect(has("device.channels.col.status")).toBe(false);
  });
});
