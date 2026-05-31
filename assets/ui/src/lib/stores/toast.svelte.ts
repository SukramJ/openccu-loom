// Global toast/notification store. Replaces per-component banner
// state when callers want a top-of-screen confirmation that does
// not steal focus and auto-dismisses. Inline banners stay valid for
// in-place context (form errors, "schedule loading…").
//
// Design notes:
//   - Single module-level store; the <Toaster/> component subscribes
//     and renders into a fixed viewport corner.
//   - Severity drives colour (info / success / warn / error).
//   - Each toast has a numeric id so React-style key removal works
//     reliably even when the same message fires twice in a row.
//   - Default lifetime is 4 s (info/success) or sticky (warn/error)
//     until the user dismisses — mirrors the Home Assistant defaults
//     aiohomematic-config inherits.

export type ToastSeverity = "info" | "success" | "warn" | "error";

export type Toast = {
  id: number;
  message: string;
  severity: ToastSeverity;
  /** Optional sub-text (one extra line). */
  detail?: string;
  /** ms until auto-dismiss; null = sticky. */
  ttl: number | null;
};

let nextId = 1;

class ToastStore {
  items = $state<Toast[]>([]);

  push(severity: ToastSeverity, message: string, detail?: string, ttl?: number | null): number {
    const id = nextId++;
    const finalTtl =
      ttl === undefined
        ? severity === "warn" || severity === "error"
          ? null
          : 4000
        : ttl;
    const toast: Toast = { id, message, severity, detail, ttl: finalTtl };
    this.items = [...this.items, toast];
    if (finalTtl !== null) {
      setTimeout(() => this.dismiss(id), finalTtl);
    }
    return id;
  }

  info(message: string, detail?: string): number {
    return this.push("info", message, detail);
  }
  success(message: string, detail?: string): number {
    return this.push("success", message, detail);
  }
  warn(message: string, detail?: string): number {
    return this.push("warn", message, detail);
  }
  error(message: string, detail?: string): number {
    return this.push("error", message, detail);
  }

  dismiss(id: number) {
    this.items = this.items.filter((t) => t.id !== id);
  }
  dismissAll() {
    this.items = [];
  }
}

export const toastStore = new ToastStore();
