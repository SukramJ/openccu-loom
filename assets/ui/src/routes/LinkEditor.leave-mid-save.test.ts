// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent, screen } from "@testing-library/svelte";
import type { Link, UISchema } from "$lib/api/types";

const { mockUiSchema, mockPutLinkParamset, mockToastError, LINK } = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockPutLinkParamset: vi.fn(),
  mockToastError: vi.fn(),
  LINK: {
    sender_address: "SEND:1",
    receiver_address: "RECV:4",
    name: "Flur",
    peer_address: "RECV:4",
    direction: "outgoing",
  } as Link,
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listLinks: vi.fn().mockResolvedValue([LINK]),
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: vi.fn().mockResolvedValue([]),
    openEditSession: vi.fn().mockRejectedValue(new Error("sessions not wired")),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    putLinkParamset: (...args: unknown[]) => mockPutLinkParamset(...args),
    getDevice: vi.fn().mockResolvedValue({ rx_mode: { always: true } }),
  },
  ApiError: class ApiError extends Error {
    status = 500;
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: vi.fn(),
    error: (...a: unknown[]) => mockToastError(...a),
    warn: vi.fn(),
    push: vi.fn(),
  },
}));

vi.mock("$lib/stores/surfaces.svelte", () => ({
  surfacesStore: { opensVisible: () => true, visible: () => true },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

import LinkEditor from "./LinkEditor.svelte";

// A LINK paramset without profile data: the panel shows it in the
// expert view, one number field per side.
function linkSchema(addr: string, ch: number): UISchema {
  return {
    channel: { address: `${addr}:${ch}`, number: ch, type: "X", device_address: addr },
    parameters: [
      {
        name: "SHORT_ON_LEVEL",
        label: `level ${addr}`,
        type: "INTEGER",
        min: 0,
        max: 200,
        operations: { read: true, write: true, event: false },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: 50,
      },
    ],
  } as UISchema;
}

async function flush() {
  for (let i = 0; i < 3; i++) await Promise.resolve();
}

beforeEach(() => {
  vi.clearAllMocks();
  mockUiSchema.mockImplementation((addr: string, ch: number) => Promise.resolve(linkSchema(addr, ch)));
});

afterEach(() => cleanup());

describe("LinkEditor — the operator leaves while a save is in flight", () => {
  // The write reaches the CCU; the page is gone before the PUT returns.
  // Each panel reads its props again after the await. They are strings
  // taken from the route, so nothing they read can have been nulled, and
  // a successful write must not come back as "save failed".
  it("does not report the successful write as failed", async () => {
    let resolvePut: () => void = () => {};
    mockPutLinkParamset.mockImplementation(
      () => new Promise<void>((resolve) => (resolvePut = () => resolve())),
    );

    const { unmount } = render(LinkEditor, {
      props: { sender: "SEND:1", receiver: "RECV:4", locale: "de" },
    });
    const receiverInput = await waitFor(() => {
      const el = document.querySelector(
        '[aria-labelledby="link-receiver-heading"] input[type="number"]',
      ) as HTMLInputElement | null;
      expect(el).toBeTruthy();
      return el as HTMLInputElement;
    });
    await fireEvent.input(receiverInput, { target: { value: "75" } });
    await fireEvent.click(await waitFor(() => screen.getByText("links.editor.apply")));
    // A LINK write is previewed first (the preference is on by default).
    await fireEvent.click(await waitFor(() => screen.getByText("channel.preview.write")));
    await waitFor(() => expect(mockPutLinkParamset).toHaveBeenCalled());

    unmount();
    await flush();
    resolvePut();
    await flush();
    await flush();

    const failures = mockToastError.mock.calls.filter(([title]) => title === "channel.save_failed");
    expect(failures).toHaveLength(0);
  });
});
