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
// unaffected; the restore-gating cases fill `fleet.items`. centralsLacking
// applies the same lasting-reason rule as the real store: a feature that is
// only "not_ready" does not count as lacking.
type MockFeature = { available: boolean; reason?: string };
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
    featureOf: () => undefined,
    byName: () => undefined,
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

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
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

describe("BackupList — restore gating", () => {
  it("hides restore on an archive whose central lacks the restore scope, keeping download", async () => {
    // An openccu-lite credential can carry the create scope without the
    // restore scope; a restore button there could only fail.
    fleet.items = [central("alpha", { available: false, reason: "missing_scope" })];
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.download")).toBeInTheDocument();
    });
    expect(screen.queryByText("common.restore")).toBeNull();
    expect(screen.getByText("backup.delete")).toBeInTheDocument();
  });

  it("shows restore on an archive whose central offers it", async () => {
    fleet.items = [central("alpha", { available: true })];
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("common.restore")).toBeInTheDocument();
    });
  });

  it("keeps restore on an archive whose central is only booting", async () => {
    // "not_ready" is transient: hiding the button would make it vanish
    // every time the CCU restarts.
    fleet.items = [central("alpha", { available: false, reason: "not_ready" })];
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    mockListBackups.mockResolvedValue(ONE_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("common.restore")).toBeInTheDocument();
    });
  });

  it("hides restore on an uploaded archive and the upload button when no central can restore", async () => {
    // An uploaded archive has no central of its own and is useful only for
    // a restore, so with no restoring central both actions are dead ends.
    fleet.items = [
      central("alpha", { available: false, reason: "missing_scope" }),
      central("beta", { available: false, reason: "unsupported" }),
    ];
    mockListCentralsV2.mockResolvedValue(TWO_CENTRALS);
    mockListBackups.mockResolvedValue(UPLOADED_BACKUP);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.download")).toBeInTheDocument();
    });
    expect(screen.queryByText("common.restore")).toBeNull();
    expect(screen.queryByText("backup.upload")).toBeNull();
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
      expect(screen.getByText("common.restore")).toBeInTheDocument();
    });
    expect(screen.getByText("backup.upload")).toBeInTheDocument();
  });

  it("offers the upload button before the fleet has loaded", async () => {
    mockListCentralsV2.mockResolvedValue(ONE_CENTRAL);
    render(BackupList);

    await waitFor(() => {
      expect(screen.getByText("backup.upload")).toBeInTheDocument();
    });
  });
});
