// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListCentralsV2 = vi.fn();
const mockListBackups = vi.fn();
const mockTriggerBackup = vi.fn();
const mockBackupStorageInfo = vi.fn();
const mockDeleteBackup = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

// The real store would pull in the auth store. By default the fleet is
// empty and offers every feature, so cases not about feature gating are
// unaffected; the restore-gating cases fill `fleet.items`. featureOf and
// byName read the same fleet the way the real store does.
type MockFeature = { available: boolean; reason?: string; scope?: string };
type MockCentral = { name: string; features?: Record<string, MockFeature> };
const fleet = vi.hoisted(() => ({ items: [] as MockCentral[] }));

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    get items() {
      return fleet.items;
    },
    offers: () => true,
    featureAvailable: () => true,
    centralsLacking: (key: string) =>
      fleet.items.filter((c) => {
        const f = c.features?.[key];
        return f !== undefined && !f.available && f.reason !== "not_ready";
      }),
    featureOf: (central: string, key: string) =>
      fleet.items.find((c) => c.name === central)?.features?.[key],
    byName: (central: string) => fleet.items.find((c) => c.name === central),
  },
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listCentralsV2: (...args: unknown[]) => mockListCentralsV2(...args),
    listBackups: (...args: unknown[]) => mockListBackups(...args),
    triggerBackup: (...args: unknown[]) => mockTriggerBackup(...args),
    backupStorageInfo: (...args: unknown[]) => mockBackupStorageInfo(...args),
    deleteBackup: (...args: unknown[]) => mockDeleteBackup(...args),
    backupDownloadUrl: (id: string) => `/api/v1/backups/${id}/download`,
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

// Parameters are rendered so a case can see which reason a message carries.
vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, string>) =>
    params ? `${key} ${JSON.stringify(params)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

const mockConfirmAsk = vi.fn().mockResolvedValue(false);

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import BackupList from "./BackupList.svelte";

const ONE_CENTRAL = [{ name: "alpha", host: "192.0.2.29", interfaces: [] }];
const TWO_CENTRALS = [
  { name: "alpha", host: "192.0.2.29", interfaces: [] },
  { name: "beta", host: "192.0.2.30", interfaces: [] },
];

beforeEach(() => {
  vi.clearAllMocks();
  fleet.items = [];
  mockListBackups.mockResolvedValue([]);
  mockTriggerBackup.mockResolvedValue({ id: "backup-001" });
  mockBackupStorageInfo.mockResolvedValue({
    dir: "/media/usb0/backup",
    available: true,
    count: 0,
    bytes: 0,
  });
  mockDeleteBackup.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(false);
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// Central picker visibility + trigger routing (B2 multi-CCU fix)
// ---------------------------------------------------------------------------

describe("BackupList — trigger-target central picker", () => {
  it("hides the central picker and triggers unscoped with a single registered central", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    render(BackupList);

    await waitFor(() => {
      expect(mockListCentralsV2).toHaveBeenCalled();
    });
    // No picker label rendered for a single central.
    expect(screen.queryByText("backup.trigger_central")).toBeNull();

    const button = screen.getByText("backup.trigger");
    button.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    await waitFor(() => {
      expect(mockTriggerBackup).toHaveBeenCalledWith(undefined);
    });
  });

  it("shows the central picker and triggers the selected central with several registered centrals", async () => {
    mockListCentralsV2.mockResolvedValue(TWO_CENTRALS);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.trigger_central")).toBeInTheDocument();
    });

    const button = screen.getByText("backup.trigger");
    button.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    // Defaults to the first returned central (alpha) until the operator
    // picks a different one.
    await waitFor(() => {
      expect(mockTriggerBackup).toHaveBeenCalledWith("alpha");
    });
  });
});

// ---------------------------------------------------------------------------
// Storage location (#589)
// ---------------------------------------------------------------------------

describe("BackupList — storage location", () => {
  it("renders the directory the daemon actually writes to", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockBackupStorageInfo.mockResolvedValue({
      dir: "/media/usb0/backup",
      available: true,
      count: 3,
      bytes: 4096,
    });
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("/media/usb0/backup")).toBeInTheDocument();
    });
  });

  it("warns instead of showing a path when the daemon has no storage directory", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockBackupStorageInfo.mockResolvedValue({
      dir: "",
      available: false,
      count: 0,
      bytes: 0,
    });
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.storage.unavailable")).toBeInTheDocument();
    });
    expect(screen.queryByText("backup.storage.summary")).toBeNull();
  });

  it("renders the list without a location row when the daemon does not serve one", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockBackupStorageInfo.mockRejectedValue(new Error("404"));
    render(BackupList);

    await waitFor(() => {
      expect(mockListBackups).toHaveBeenCalled();
    });
    expect(screen.queryByTestId("backup-storage")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Delete (#590)
// ---------------------------------------------------------------------------

const ONE_BACKUP = [
  {
    id: "alpha-20260818-140257",
    central: "alpha",
    bytes: 1024,
    created_at: "2026-08-18T14:02:57Z",
    filename: "alpha-3.89.8-2026-08-18-1602.sbk",
  },
];

describe("BackupList — delete", () => {
  it("asks before deleting and does nothing when the operator declines", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    mockConfirmAsk.mockResolvedValue(false);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.delete")).toBeInTheDocument();
    });
    screen
      .getByText("backup.delete")
      .dispatchEvent(new MouseEvent("click", { bubbles: true }));

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalled();
    });
    expect(mockDeleteBackup).not.toHaveBeenCalled();
  });

  it("deletes the addressed archive and reloads the list once confirmed", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    mockConfirmAsk.mockResolvedValue(true);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.delete")).toBeInTheDocument();
    });
    const callsBefore = mockListBackups.mock.calls.length;
    screen
      .getByText("backup.delete")
      .dispatchEvent(new MouseEvent("click", { bubbles: true }));

    await waitFor(() => {
      expect(mockDeleteBackup).toHaveBeenCalledWith("alpha-20260818-140257");
    });
    // The list is re-read, so a deleted archive cannot linger in the table.
    await waitFor(() => {
      expect(mockListBackups.mock.calls.length).toBeGreaterThan(callsBefore);
    });
  });
});

// ---------------------------------------------------------------------------
// Restore and upload are hidden where no central can restore
// ---------------------------------------------------------------------------

const RESTORE = "system.backup.restore";

function central(name: string, restore?: MockFeature): MockCentral {
  return { name, features: restore ? { [RESTORE]: restore } : {} };
}

const UPLOADED_BACKUP = [
  {
    id: "upload-20260818-140257",
    central: "",
    bytes: 1024,
    created_at: "2026-08-18T14:02:57Z",
    filename: "uploaded.sbk",
  },
];

/** The restore button in the row, or null when it is not rendered. */
function restoreButton(): HTMLButtonElement | null {
  return screen.queryByText("common.restore")?.closest("button") ?? null;
}

/** The upload button, or null when it is not rendered. */
function uploadButton(): HTMLButtonElement | null {
  return screen.queryByText("backup.upload")?.closest("button") ?? null;
}

/** The accessible description a disabled button points at. */
function describedBy(button: HTMLElement): string {
  const id = button.getAttribute("aria-describedby");
  expect(id).toBeTruthy();
  return document.getElementById(id!)?.textContent ?? "";
}

describe("BackupList — restore gating", () => {
  it("disables restore with the reason on an archive whose central lacks the restore scope", async () => {
    // An openccu-lite credential can carry the create scope without the
    // restore scope. The operator can grant it, so the action stays visible
    // and says why it is not available.
    fleet.items = [
      central("alpha", { available: false, reason: "missing_scope", scope: "system:restore" }),
    ];
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(restoreButton()).not.toBeNull();
    });
    const button = restoreButton()!;
    expect(button.disabled).toBe(true);
    expect(button.title).toContain("feature.unavailable");
    expect(button.title).toContain("feature.reason.missing_scope");
    expect(button.title).toContain("system:restore");
    expect(describedBy(button)).toBe(button.title);
    button.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(mockConfirmAsk).not.toHaveBeenCalled();
    expect(screen.getByText("backup.download")).toBeInTheDocument();
    expect(screen.getByText("backup.delete")).toBeInTheDocument();
  });

  it("hides restore on an archive whose system does not offer it", async () => {
    fleet.items = [central("alpha", { available: false, reason: "not_supported_by_system" })];
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.download")).toBeInTheDocument();
    });
    expect(restoreButton()).toBeNull();
    expect(uploadButton()).toBeNull();
  });

  it("shows restore on an archive whose central offers it", async () => {
    fleet.items = [central("alpha", { available: true })];
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(restoreButton()).not.toBeNull();
    });
    expect(restoreButton()!.disabled).toBe(false);
  });

  it("keeps restore enabled on an archive whose central is only booting", async () => {
    // "not_ready" is transient: disabling or hiding the button would make
    // it change every time the CCU restarts.
    fleet.items = [central("alpha", { available: false, reason: "not_ready" })];
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(restoreButton()).not.toBeNull();
    });
    expect(restoreButton()!.disabled).toBe(false);
  });

  it("disables restore of an uploaded archive and the upload when the only restoring central lacks the scope", async () => {
    // An uploaded archive has no central of its own: with no central able
    // to restore, both actions say why, as long as a scope would fix it.
    fleet.items = [
      central("alpha", { available: false, reason: "missing_scope", scope: "system:restore" }),
      central("beta", { available: false, reason: "not_supported_by_system" }),
    ];
    mockListCentralsV2.mockResolvedValue(TWO_CENTRALS);
    mockListBackups.mockResolvedValue(UPLOADED_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(restoreButton()).not.toBeNull();
    });
    expect(restoreButton()!.disabled).toBe(true);
    expect(describedBy(restoreButton()!)).toContain("system:restore");
    expect(uploadButton()!.disabled).toBe(true);
    expect(describedBy(uploadButton()!)).toContain("system:restore");
  });

  it("hides restore of an uploaded archive and the upload when no central's system offers restore", async () => {
    fleet.items = [
      central("alpha", { available: false, reason: "not_supported_by_system" }),
      central("beta", { available: false, reason: "not_supported_by_system" }),
    ];
    mockListCentralsV2.mockResolvedValue(TWO_CENTRALS);
    mockListBackups.mockResolvedValue(UPLOADED_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.download")).toBeInTheDocument();
    });
    expect(restoreButton()).toBeNull();
    expect(uploadButton()).toBeNull();
  });

  it("offers the upload button and restore of an uploaded archive when one central can restore", async () => {
    fleet.items = [
      central("alpha", { available: false, reason: "missing_scope" }),
      central("beta", { available: true }),
    ];
    mockListCentralsV2.mockResolvedValue(TWO_CENTRALS);
    mockListBackups.mockResolvedValue(UPLOADED_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(restoreButton()).not.toBeNull();
    });
    expect(restoreButton()!.disabled).toBe(false);
    expect(uploadButton()!.disabled).toBe(false);
  });

  it("offers the upload button before the fleet has loaded", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    render(BackupList);

    await waitFor(() => {
      expect(uploadButton()).not.toBeNull();
    });
    expect(uploadButton()!.disabled).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Encrypted openccu-lite archives
// ---------------------------------------------------------------------------

describe("BackupList — encrypted archives", () => {
  it("marks an archive whose name ends in .age as encrypted", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue([{ ...ONE_BACKUP[0], filename: "x.sbk.age" }]);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.encrypted")).toBeInTheDocument();
    });
    expect(screen.getByText("backup.encrypted").getAttribute("title")).toBe("backup.encrypted.help");
  });

  it("does not mark a plain archive", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue([{ ...ONE_BACKUP[0], filename: "x.sbk" }]);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.download")).toBeInTheDocument();
    });
    expect(screen.queryByText("backup.encrypted")).toBeNull();
  });

  it("lets the file picker take encrypted archives", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    const { container } = render(BackupList);
    await waitFor(() => {
      expect(container.querySelector('input[type="file"]')).not.toBeNull();
    });
    const accept = container.querySelector('input[type="file"]')!.getAttribute("accept") ?? "";
    expect(accept.split(",")).toEqual(expect.arrayContaining([".sbk", ".age"]));
  });
});
