// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent } from "@testing-library/svelte";
import type { DataPointSummary } from "$lib/api/types";
import type { ResolvedChannel } from "../resolver";

// `t()` mock follows the repo-wide convention (e.g.
// ChannelPanel.determine.test.ts): interpolated calls render the key plus
// its vars so per-title aria-labels stay distinguishable in assertions.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import ButtonEvent from "./ButtonEvent.svelte";

const TITLE = "Front Door Bell";

function dp(parameter: string, overrides: Partial<DataPointSummary> = {}): DataPointSummary {
  return {
    parameter,
    value: null,
    observed: false,
    operations: { read: false, write: true, event: true },
    unique_id: `dp-${parameter}`,
    usage: "data_point",
    ...overrides,
  };
}

function resolved(slots: Record<string, DataPointSummary>): ResolvedChannel {
  return { family: "BUTTON", slots, siblings: {} };
}

function shortLabel() {
  return `remote.press_short_title::${JSON.stringify({ title: TITLE })}`;
}

function longLabel() {
  return `remote.press_long_title::${JSON.stringify({ title: TITLE })}`;
}

afterEach(() => cleanup());

describe("ButtonEvent — pressable slots render a button", () => {
  it("renders both short and long buttons when both slots are writable + data_point", async () => {
    const onSetSlot = vi.fn();
    render(ButtonEvent, {
      props: {
        resolved: resolved({
          SHORT: dp("PRESS_SHORT"),
          LONG: dp("PRESS_LONG"),
        }),
        title: TITLE,
        onSetSlot,
      },
    });

    const shortBtn = screen.getByRole("button", { name: shortLabel() });
    const longBtn = screen.getByRole("button", { name: longLabel() });

    await fireEvent.click(shortBtn);
    expect(onSetSlot).toHaveBeenCalledWith("SHORT", true);

    await fireEvent.click(longBtn);
    expect(onSetSlot).toHaveBeenCalledWith("LONG", true);
  });
});

describe("ButtonEvent — non-writable slots render no button", () => {
  it("omits the long button when its DP is not writable", () => {
    render(ButtonEvent, {
      props: {
        resolved: resolved({
          SHORT: dp("PRESS_SHORT"),
          LONG: dp("PRESS_LONG", { operations: { read: false, write: false, event: true } }),
        }),
        title: TITLE,
        onSetSlot: vi.fn(),
      },
    });

    expect(screen.getByRole("button", { name: shortLabel() })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: longLabel() })).not.toBeInTheDocument();
  });

  it("omits the short button when its usage is not data_point, even if writable", () => {
    render(ButtonEvent, {
      props: {
        resolved: resolved({
          SHORT: dp("PRESS_SHORT", { usage: "no_create" }),
          LONG: dp("PRESS_LONG"),
        }),
        title: TITLE,
        onSetSlot: vi.fn(),
      },
    });

    expect(screen.queryByRole("button", { name: shortLabel() })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: longLabel() })).toBeInTheDocument();
  });

  it("renders no press buttons at all when neither slot is pressable", () => {
    render(ButtonEvent, {
      props: {
        resolved: resolved({
          SHORT: dp("PRESS_SHORT", { operations: { read: false, write: false, event: true } }),
          LONG: dp("PRESS_LONG", { operations: { read: false, write: false, event: true } }),
        }),
        title: TITLE,
        onSetSlot: vi.fn(),
      },
    });

    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("ButtonEvent — BTN_SHORT_ONLY (no LONG slot at all)", () => {
  it("renders only the short button when the channel has no LONG slot", () => {
    render(ButtonEvent, {
      props: {
        resolved: resolved({ SHORT: dp("PRESS_SHORT") }),
        title: TITLE,
        onSetSlot: vi.fn(),
      },
    });

    expect(screen.getByRole("button", { name: shortLabel() })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: longLabel() })).not.toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(1);
  });
});
