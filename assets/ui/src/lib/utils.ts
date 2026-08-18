import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * Tailwind class composition helper — pattern lifted from shadcn.
 * Allows conditional classes with `clsx` and resolves conflicts
 * (e.g. `bg-red-500 bg-blue-500` → `bg-blue-500`) via tailwind-merge.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/**
 * Build a case-insensitive text predicate over a free-text search term.
 *
 * When the term is a valid regular expression it matches by regex, which
 * lets power users search device lists by patterns like `BidCos-RF\.MEQ`
 * or `MEQ|HEQ`. An invalid pattern (e.g. an unbalanced paren mid-typing)
 * falls back to a plain case-insensitive substring match, so the input is
 * never "broken" while it is being typed. An empty term matches everything.
 */
/**
 * Read a string from localStorage, returning `fallback` when the key is
 * absent or storage is unavailable. Pair with {@link saveLS} to persist a
 * per-view filter across reloads.
 */
export function loadLS(key: string, fallback = ""): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

/** Best-effort write to localStorage; ignores a disabled or full store. */
export function saveLS(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // storage unavailable — the value simply does not persist.
  }
}

export function makeTextMatcher(term: string): (text: string) => boolean {
  const q = term.trim();
  if (!q) return () => true;
  let re: RegExp | null = null;
  try {
    re = new RegExp(q, "i");
  } catch {
    re = null;
  }
  const lower = q.toLowerCase();
  // The literal substring match is an additional pass, never a fallback
  // that a valid-but-different-meaning regex disables. A CCU-style name
  // like "Wohnzimmer (Decke)" compiles as a regex (the parens become a
  // capture group) and then matches nothing containing that literal
  // text, which would otherwise empty the whole list for an exact,
  // pasted-in name — the common case, not the power-user one.
  return (text: string) => {
    const t = text.toLowerCase();
    if (t.includes(lower)) return true;
    return re ? re.test(text) : false;
  };
}
