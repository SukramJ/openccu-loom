// @vitest-environment happy-dom
import { describe, it, expect } from "vitest";

// Guards the web-storage shim in test-setup.ts. Without it the suite's
// outcome depends on the Node version the developer happens to run:
// bare `localStorage` is undefined from Node 22 on, because Node's own
// empty global getter shadows the one the DOM environment provides.
// Every store test that persists a preference reaches for the bare
// name, so this is the seam that decides whether they run at all.
describe("test environment: web storage", () => {
  it("exposes localStorage and sessionStorage under the bare global name", () => {
    expect(typeof localStorage).toBe("object");
    expect(typeof sessionStorage).toBe("object");
  });

  it("stores and reads back through the bare global", () => {
    localStorage.setItem("shim-probe", "value");
    expect(localStorage.getItem("shim-probe")).toBe("value");
    localStorage.clear();
    expect(localStorage.getItem("shim-probe")).toBeNull();
  });

  it("resolves the bare global to the DOM window's storage", () => {
    // Not merely "some storage exists": a second, parallel store would
    // let a test write through one name and read a stale value from the
    // other.
    window.localStorage.setItem("shim-identity", "from-window");
    expect(localStorage.getItem("shim-identity")).toBe("from-window");
    localStorage.clear();
  });
});
