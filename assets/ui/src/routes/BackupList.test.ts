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

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listCentralsV2: (...args: unknown[]) => mockListCentralsV2(...args),
    listBackups: (...args: unknown[]) => mockListBackups(...args),
    triggerBackup: (...args: unknown[]) => mockTriggerBackup(...args),
    backupStorageInfo: (...args: unknown[]) => mockBackupStorageInfo(...args),
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

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import BackupList from "./BackupList.svelte";

const ONE_CENTRAL = [{ name: "alpha", host: "172.18.4.29", interfaces: [] }];
const TWO_CENTRALS = [
  { name: "alpha", host: "172.18.4.29", interfaces: [] },
  { name: "beta", host: "172.18.4.30", interfaces: [] },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockListBackups.mockResolvedValue([]);
  mockTriggerBackup.mockResolvedValue({ id: "backup-001" });
  mockBackupStorageInfo.mockResolvedValue({
    dir: "/media/usb0/backup",
    available: true,
    count: 0,
    bytes: 0,
  });
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
