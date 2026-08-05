// Jest-dom matchers (toBeInTheDocument, toHaveTextContent, …)
// are registered here so every test file that runs in the
// happy-dom environment can use them without a per-file import.
import "@testing-library/jest-dom/vitest";
import { Storage } from "happy-dom";

// Node 22 and newer define `localStorage` and `sessionStorage` as own
// getters on globalThis. They answer `undefined` unless the process was
// started with `--localstorage-file`, and only emit an ExperimentalWarning
// on stderr when read. Vitest's DOM environments copy their window's keys
// onto globalThis but leave existing own properties alone, so those empty
// getters win and every bare `localStorage.…` in a DOM test fails with
// "Cannot read properties of undefined (reading 'clear')".
//
// Whether this bites depends purely on the Node version: it has no such
// globals on 24, which is what CI pins, and does have them on 26. The
// suite therefore passes in CI and fails on a current local toolchain —
// and would start failing in CI the day NODE_VERSION moves.
//
// The environment's own storage cannot be borrowed instead: vitest makes
// `window` the very same object as globalThis, so window.localStorage is
// shadowed by the identical empty getter. A fresh Storage from the same
// implementation the environment would have used is the closest thing to
// what should have been there. A no-op in the `node` environment (no
// window) and on Node versions whose globals the environment populated.
if (typeof window !== "undefined") {
  const global = globalThis as Record<string, unknown>;
  for (const key of ["localStorage", "sessionStorage"] as const) {
    if (global[key] === undefined) {
      Object.defineProperty(globalThis, key, {
        value: new Storage(),
        configurable: true,
        writable: true,
      });
    }
  }
}
