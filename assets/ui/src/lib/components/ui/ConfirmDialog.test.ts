// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";
import { tick } from "svelte";

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

import ConfirmDialog from "./ConfirmDialog.svelte";
import { confirmStore } from "$lib/stores/confirm.svelte";

afterEach(() => {
  cleanup();
  // Resolve any dialog left pending by a failing assertion so state
  // doesn't leak between tests.
  confirmStore.resolve(false);
});

describe("ConfirmDialog — focus trap", () => {
  it("moves focus into the dialog (cancel button) when it opens", async () => {
    const trigger = document.createElement("button");
    trigger.textContent = "open";
    document.body.appendChild(trigger);
    trigger.focus();
    expect(document.activeElement).toBe(trigger);

    render(ConfirmDialog);
    void confirmStore.ask({ title: "Delete?" });
    await tick();
    // The focus move is queued past the current microtask.
    await Promise.resolve();
    await tick();

    const buttons = document.querySelectorAll('[role="dialog"] button');
    expect(buttons.length).toBe(2);
    expect(document.activeElement).toBe(buttons[0]);

    trigger.remove();
  });

  it("renders the optional checkbox and reflects its toggled value", async () => {
    render(ConfirmDialog);
    void confirmStore.ask({
      title: "Run?",
      checkbox: { label: "Only when condition met", checked: false },
    });
    await tick();
    await Promise.resolve();
    await tick();

    expect(confirmStore.checkboxChecked).toBe(false);
    // The Switch renders as a role="switch" button inside the dialog.
    const toggle = document.querySelector<HTMLElement>(
      '[role="dialog"] [role="switch"]',
    );
    expect(toggle).not.toBeNull();
    expect(document.body.textContent).toContain("Only when condition met");

    toggle?.click();
    await tick();
    expect(confirmStore.checkboxChecked).toBe(true);

    confirmStore.resolve(true);
  });

  it("omits the checkbox when no checkbox option is given", async () => {
    render(ConfirmDialog);
    void confirmStore.ask({ title: "Plain?" });
    await tick();
    await Promise.resolve();
    await tick();

    expect(
      document.querySelector('[role="dialog"] [role="switch"]'),
    ).toBeNull();
    confirmStore.resolve(false);
  });

  it("restores focus to the triggering element on close", async () => {
    const trigger = document.createElement("button");
    trigger.textContent = "open";
    document.body.appendChild(trigger);
    trigger.focus();

    render(ConfirmDialog);
    const promise = confirmStore.ask({ title: "Delete?" });
    await tick();
    await Promise.resolve();
    await tick();

    confirmStore.resolve(false);
    await promise;
    await tick();

    expect(document.activeElement).toBe(trigger);

    trigger.remove();
  });
});
