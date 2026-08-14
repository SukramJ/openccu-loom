// @vitest-environment happy-dom
//
// GroupEditor.svelte's dialog previously only wired Escape on its own root
// div, so a keyboard user whose focus was still on the button that opened
// the dialog (nothing inside the dialog ever received focus) could not
// close it with Escape. Mirrors ConfirmDialog.svelte's focus-trap pattern:
// a global <svelte:window onkeydown> handler plus focus-in-on-open /
// focus-restore-on-close.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent } from "@testing-library/svelte";

const mockGroupTypes = vi.fn();
const mockSuitable = vi.fn();
const mockCreate = vi.fn();
const mockUpdate = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    groupTypes: (...a: unknown[]) => mockGroupTypes(...a),
    groupSuitableMembers: (...a: unknown[]) => mockSuitable(...a),
    createGroup: (...a: unknown[]) => mockCreate(...a),
    updateGroup: (...a: unknown[]) => mockUpdate(...a),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/areas.svelte", () => ({
  areasStore: {
    areas: [] as { id: string; name: string }[],
    ensureLoaded: vi.fn(),
    areaIdOf: vi.fn(() => undefined),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import GroupEditor from "./GroupEditor.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockGroupTypes.mockResolvedValue([{ id: "hmip.heating.group", label_key: "" }]);
  mockSuitable.mockResolvedValue({
    assignable: [{ address: "AAA:1", type: "SWITCH_ACTUATOR" }],
    leftover: [],
  });
});

afterEach(() => cleanup());

describe("GroupEditor — focus handling", () => {
  it("closes on Escape even when focus never moved off the trigger that opened it", async () => {
    // Simulates the trigger button outside the dialog retaining DOM focus —
    // the exact scenario the missing focus-in-on-open left broken.
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();

    const onClose = vi.fn();
    render(GroupEditor, {
      props: { central: "ccu-a", onClose, onSaved: vi.fn() },
    });
    await waitFor(() => expect(screen.getByLabelText("AAA")).toBeInTheDocument());

    // Move focus back to the trigger explicitly — the initial focus-in
    // effect already ran once on mount, so this exercises "focus is
    // outside the dialog subtree when Escape is pressed", not merely
    // "focus never left the trigger in the first place".
    trigger.focus();
    expect(document.activeElement).toBe(trigger);

    await fireEvent.keyDown(window, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
    document.body.removeChild(trigger);
  });

  it("moves focus into the dialog once it has content, and restores it to the trigger on close", async () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();

    const { container, unmount } = render(GroupEditor, {
      props: { central: "ccu-a", onClose: vi.fn(), onSaved: vi.fn() },
    });
    await waitFor(() => expect(screen.getByLabelText("AAA")).toBeInTheDocument());

    await waitFor(() => expect(document.activeElement).not.toBe(trigger));
    expect(container.contains(document.activeElement)).toBe(true);

    unmount();
    expect(document.activeElement).toBe(trigger);
    document.body.removeChild(trigger);
  });
});
