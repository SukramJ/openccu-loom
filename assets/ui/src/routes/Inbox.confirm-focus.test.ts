// @vitest-environment happy-dom
//
// The replace flow stacks two modals: the hand-rolled replace dialog stays
// mounted while the shared confirm dialog opens on top of it. Both trap
// focus on `window`, so without a yield the lower one drags every Tab back
// into itself and the destructive confirmation cannot be reached by
// keyboard. The real confirmStore and the real ConfirmDialog are used here
// on purpose — the defect lives in how the two handlers interact.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent } from "@testing-library/svelte";

const { mockListInbox, mockListReplaceCandidates } = vi.hoisted(() => ({
  mockListInbox: vi.fn(),
  mockListReplaceCandidates: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listInbox: (...args: unknown[]) => mockListInbox(...args),
    listRooms: vi.fn().mockResolvedValue([]),
    listFunctions: vi.fn().mockResolvedValue([]),
    acceptInboxDevice: vi.fn(),
    listReplaceCandidates: (...args: unknown[]) => mockListReplaceCandidates(...args),
    replaceDevice: vi.fn().mockResolvedValue(undefined),
    searchWiredDevices: vi.fn(),
    getGroups: vi.fn().mockResolvedValue([]),
    groupSuitableMembers: vi.fn().mockResolvedValue({ assignable: [], leftover: [] }),
    updateGroup: vi.fn(),
    createRoom: vi.fn(),
    createFunction: vi.fn(),
    listInstallModeInterfaces: vi.fn().mockResolvedValue([]),
    setInstallModeInterface: vi.fn(),
    pairDeviceInstallMode: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params
      ? Object.entries(params).reduce((s, [k, v]) => s.replace(`{${k}}`, String(v)), key)
      : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en", expertMode: false },
}));

vi.mock("$lib/stores/installMode.svelte", () => ({
  installModeStore: {
    active: false,
    remainingSeconds: null,
    interfaces: [],
    busy: false,
    banner: null,
    refresh: vi.fn(),
    toggle: vi.fn(),
    ensurePoll: vi.fn(),
    release: vi.fn(),
  },
}));

import InboxWithConfirm from "./__testutils__/InboxWithConfirm.svelte";

const REPLACEABLE_DEVICE = [
  { address: "0009ABCD", model: "HM-Sec-SC", central: "", interface: "BidCos-RF" },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockListInbox.mockResolvedValue(REPLACEABLE_DEVICE);
  mockListReplaceCandidates.mockResolvedValue([
    { address: "OLD001", name: "Fenster", model: "HM-Sec-SC", model_matches: true },
  ]);
});

afterEach(() => cleanup());

function confirmDialogEl(): HTMLElement {
  return screen.getByRole("dialog", { name: "inbox.replace.confirm_title" });
}

describe("Inbox — replace confirmation focus", () => {
  it("leaves the keyboard to the confirm dialog stacked on the replace dialog", async () => {
    render(InboxWithConfirm);
    await waitFor(() =>
      expect(screen.getByText("inbox.replace.button")).toBeInTheDocument(),
    );
    await fireEvent.click(screen.getByText("inbox.replace.button"));
    await screen.findByText("Fenster");
    await fireEvent.click(screen.getByText("Fenster"));

    // The confirm dialog is open on top; the replace dialog stays mounted.
    await screen.findByText("inbox.replace.confirm_title");
    expect(screen.getByText("inbox.replace.title")).toBeInTheDocument();
    await waitFor(() =>
      expect(confirmDialogEl().contains(document.activeElement)).toBe(true),
    );

    // Tab must stay inside the confirm dialog: the replace dialog's trap
    // would otherwise pull focus back and hide both action buttons from
    // the keyboard.
    await fireEvent.keyDown(window, { key: "Tab" });
    expect(confirmDialogEl().contains(document.activeElement)).toBe(true);

    await fireEvent.keyDown(window, { key: "Tab", shiftKey: true });
    expect(confirmDialogEl().contains(document.activeElement)).toBe(true);
  });
});
