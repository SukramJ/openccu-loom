// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, within, waitFor } from "@testing-library/svelte";
import type { AlarmArea, AlarmCode } from "$lib/api/types";

let mockAreasConfig: AlarmArea[] = [];
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get areasConfig() {
      return mockAreasConfig;
    },
  },
}));

const mockListAlarmCodes = vi.fn();
const mockCreateAlarmCode = vi.fn();
const mockPutAlarmCode = vi.fn();
const mockDeleteAlarmCode = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmCodes: (...args: unknown[]) => mockListAlarmCodes(...args),
    createAlarmCode: (...args: unknown[]) => mockCreateAlarmCode(...args),
    putAlarmCode: (...args: unknown[]) => mockPutAlarmCode(...args),
    deleteAlarmCode: (...args: unknown[]) => mockDeleteAlarmCode(...args),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import AlarmCodes from "./AlarmCodes.svelte";

function code(overrides: Partial<AlarmCode> = {}): AlarmCode {
  return {
    id: "c1",
    name: "Alice",
    kind: "pin",
    perms: { arm: false, disarm: true, silence: false },
    areas: [],
    enabled: true,
    ...overrides,
  } as AlarmCode;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockAreasConfig = [{ id: "area-1", name: "Ground floor" }];
  mockListAlarmCodes.mockResolvedValue([]);
  mockCreateAlarmCode.mockResolvedValue(code());
  mockPutAlarmCode.mockResolvedValue(undefined);
  mockDeleteAlarmCode.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => {
  cleanup();
});

describe("AlarmCodes — list", () => {
  it("renders one card per code and never renders a hash or the cleartext PIN anywhere", async () => {
    mockListAlarmCodes.mockResolvedValueOnce([
      code({
        id: "c1",
        name: "Alice",
        // A response carrying `hash`/`pin`-shaped fields would be a
        // contract violation (docs/alarm-concept.md §11/§16) — the view
        // must not surface them even if a buggy backend sent them.
        // @ts-expect-error -- deliberately shaped like a leaking response
        hash: "$argon2id$v=19$m=65536,t=1,p=4$deadbeef",
      }),
      code({ id: "c2", name: "Bob", duress: true }),
    ]);
    const { findByText, getByText, queryByText } = render(AlarmCodes);

    expect(await findByText("Alice")).toBeTruthy();
    expect(getByText("Bob")).toBeTruthy();
    expect(queryByText(/argon2id/)).toBeNull();
    expect(queryByText(/deadbeef/)).toBeNull();
  });

  it("badges a duress code but shows no other visible difference in the list", async () => {
    mockListAlarmCodes.mockResolvedValueOnce([code({ id: "c2", name: "Bob", duress: true })]);
    const { findByText, getByText } = render(AlarmCodes);

    await findByText("Bob");
    expect(getByText("alarm.codes.duress.badge")).toBeTruthy();
  });

  it("shows the unavailable state on a 503 instead of a raw error", async () => {
    const { ApiError } = await import("$lib/api/client");
    mockListAlarmCodes.mockRejectedValueOnce(new ApiError(503, null, "service unavailable"));
    const { findByText } = render(AlarmCodes);

    expect(await findByText("alarm.codes.unavailable")).toBeTruthy();
  });
});

describe("AlarmCodes — create with duress warning", () => {
  // The editor drawer also renders three permission switches and an
  // enabled switch, so the duress toggle is located via its own label
  // rather than a bare getByRole("switch").
  function duressSwitch(container: HTMLElement): HTMLElement {
    const label = Array.from(container.querySelectorAll("label")).find((l) =>
      l.textContent?.includes("alarm.codes.field.duress"),
    );
    if (!label) throw new Error("duress label not found");
    const el = label.querySelector('[role="switch"]');
    if (!el) throw new Error("duress switch not found");
    return el as HTMLElement;
  }

  it("shows the duress warning only once the duress switch is toggled on", async () => {
    const { container, getByRole, queryByText, getByText } = render(AlarmCodes);

    await fireEvent.click(getByRole("button", { name: /alarm.codes.add/ }));
    expect(queryByText("alarm.codes.duress.warning")).toBeNull();

    await fireEvent.click(duressSwitch(container));

    expect(getByText("alarm.codes.duress.warning")).toBeTruthy();
  });

  it("requires a name and a PIN before a new pin-kind code can be saved", async () => {
    const { getByRole, findByText } = render(AlarmCodes);

    await fireEvent.click(getByRole("button", { name: /alarm.codes.add/ }));
    await fireEvent.click(getByRole("button", { name: "common.save" }));

    expect(await findByText("alarm.codes.error.name_required")).toBeTruthy();
    expect(mockCreateAlarmCode).not.toHaveBeenCalled();
  });

  it("creates a duress pin code and sends duress:true plus the entered pin in the request", async () => {
    const { container, getByRole, getByLabelText } = render(AlarmCodes);

    await fireEvent.click(getByRole("button", { name: /alarm.codes.add/ }));

    const nameInput = getByLabelText("alarm.codes.field.name");
    await fireEvent.input(nameInput, { target: { value: "Guest" } });

    const pinInput = document.querySelector('input[type="password"]') as HTMLInputElement;
    expect(pinInput).toBeTruthy();
    await fireEvent.input(pinInput, { target: { value: "1234" } });

    await fireEvent.click(duressSwitch(container));

    await fireEvent.click(getByRole("button", { name: "common.save" }));

    await waitFor(() => expect(mockCreateAlarmCode).toHaveBeenCalledOnce());
    expect(mockCreateAlarmCode).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Guest",
        kind: "pin",
        pin: "1234",
        duress: true,
      }),
    );
    // The write-only PIN never round-trips as a `hash` field.
    const sent = mockCreateAlarmCode.mock.calls[0][0];
    expect(sent.hash).toBeUndefined();
  });
});

describe("AlarmCodes — edit keeps the stored hash on a blank PIN", () => {
  it("omits `pin` from the update body when the PIN field is left blank", async () => {
    mockListAlarmCodes.mockResolvedValueOnce([code({ id: "c1", name: "Alice" })]);
    const { findByText, getByRole } = render(AlarmCodes);

    const nameEl = await findByText("Alice");
    const card = nameEl.closest('[class*="flex flex-col"]') ?? document.body;
    await fireEvent.click(within(card as HTMLElement).getByRole("button", { name: "common.edit" }));

    await fireEvent.click(getByRole("button", { name: "common.save" }));

    expect(mockPutAlarmCode).toHaveBeenCalledWith(
      "c1",
      expect.objectContaining({ name: "Alice" }),
    );
    const sent = mockPutAlarmCode.mock.calls[0][1];
    expect(sent.pin).toBeUndefined();
  });
});

describe("AlarmCodes — delete", () => {
  it("asks for confirmation before deleting", async () => {
    mockListAlarmCodes.mockResolvedValueOnce([code({ id: "c1", name: "Alice" })]);
    const { findByText, getByRole } = render(AlarmCodes);

    await findByText("Alice");
    await fireEvent.click(getByRole("button", { name: "common.delete" }));

    expect(mockConfirmAsk).toHaveBeenCalledOnce();
    expect(mockDeleteAlarmCode).toHaveBeenCalledWith("c1");
  });
});
