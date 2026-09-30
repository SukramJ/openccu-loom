// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";

// The sidebar footer is where the SPA offers its logout action and shows
// the signed-in user. A box-shell session (scheme occulite) belongs to the
// openccu-lite box, so the footer must drop the logout button and label the
// user as signed in through the box shell; every other scheme keeps it.

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

const auth = vi.hoisted(() => ({
  identity: null as { subject: string; role: string; scheme?: string } | null,
  get boxShellSession() {
    return this.identity?.scheme === "occulite";
  },
  get canLogout() {
    return this.identity !== null && this.identity.scheme !== "occulite";
  },
}));

vi.mock("$lib/stores/auth.svelte", () => ({ authStore: auth }));
vi.mock("$lib/stores/installMode.svelte", () => ({
  installModeStore: {
    active: false,
    remainingSeconds: 0,
    ensurePoll: vi.fn(),
    release: vi.fn(),
  },
}));
vi.mock("$lib/stores/messages.svelte", () => ({
  messagesStore: { total: 0, ensureStream: vi.fn(), release: vi.fn() },
}));
vi.mock("$lib/stores/matter.svelte", () => ({
  matterStore: { status: null, loadStatus: vi.fn(async () => {}) },
}));
vi.mock("$lib/stores/info.svelte", () => ({
  infoStore: { info: null, ensure: vi.fn(async () => {}) },
}));
vi.mock("$lib/stores/surfaces.svelte", () => ({
  surfacesStore: { visible: () => true, gate: () => undefined },
}));
vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: { featureAvailable: () => true },
}));

import Sidebar from "./Sidebar.svelte";

function mount(scheme: string) {
  auth.identity = { subject: "alice", role: "admin", scheme };
  return render(Sidebar, {
    props: {
      activeKind: "list",
      identitySubject: "alice",
      locale: "en",
      onLocaleToggle: () => {},
      onLogout: () => {},
      onShortcutHelp: () => {},
      mobileOpen: true,
      onMobileClose: () => {},
    },
  });
}

afterEach(() => {
  cleanup();
  auth.identity = null;
});

describe("Sidebar footer — logout gating by auth scheme", () => {
  it("offers logout for a session login", () => {
    const { container, queryByTestId } = mount("session");
    expect(container.querySelector('button[aria-label="nav.logout"]')).not.toBeNull();
    expect(queryByTestId("sidebar-box-shell")).toBeNull();
  });

  it("hides logout and shows the user read-only for a box-shell session", () => {
    const { container, getByTestId } = mount("occulite");
    expect(container.querySelector('button[aria-label="nav.logout"]')).toBeNull();
    const identity = getByTestId("sidebar-identity");
    expect(identity.textContent).toContain("alice");
    expect(identity.getAttribute("title")).toBe("auth.box_shell.signed_in_help");
    expect(getByTestId("sidebar-box-shell").textContent?.trim()).toBe(
      "auth.box_shell.signed_in",
    );
    // Read-only: the user line is plain text, never a control.
    expect(identity.querySelector("button, a, input")).toBeNull();
  });
});
