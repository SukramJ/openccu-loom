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
  return (text: string) =>
    re ? re.test(text) : text.toLowerCase().includes(lower);
}
