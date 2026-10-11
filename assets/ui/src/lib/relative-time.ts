// Relative-time rendering for "last refreshed" style timestamps.
//
// The daemon marshals Go's zero time as 0001-01-01T00:00:00Z for a device
// that has never been observed; that is "no value", not a date 2000 years
// ago, so it renders as absent rather than as "vor 2025 Jahren".

const ZERO_TIME_PREFIX = "0001-01-01T";

/** True for an absent, unparsable or Go-zero timestamp. */
export function isAbsentTimestamp(iso: string | null | undefined): boolean {
  if (!iso) return true;
  if (iso.startsWith(ZERO_TIME_PREFIX)) return true;
  return Number.isNaN(new Date(iso).getTime());
}

/** Epoch milliseconds for sorting; 0 for an absent timestamp so devices
 *  without an observation sort below every observed one. */
export function timestampSortKey(iso: string | null | undefined): number {
  if (isAbsentTimestamp(iso)) return 0;
  return new Date(iso as string).getTime();
}

type Unit = "second" | "minute" | "hour" | "day" | "month" | "year";

const STEPS: { limit: number; unit: Unit; div: number }[] = [
  { limit: 60, unit: "second", div: 1 },
  { limit: 3600, unit: "minute", div: 60 },
  { limit: 86400, unit: "hour", div: 3600 },
  { limit: 86400 * 30, unit: "day", div: 86400 },
  { limit: 86400 * 365, unit: "month", div: 86400 * 30 },
  { limit: Infinity, unit: "year", div: 86400 * 365 },
];

/**
 * Human-readable distance from `iso` to `now` in the given locale
 * ("vor 3 Minuten", "3 minutes ago"); `null` when the timestamp is absent.
 * A timestamp a few seconds in the future (clock skew between daemon and
 * browser) renders as "now" rather than "in 2 seconds".
 */
export function formatRelativeTime(
  iso: string | null | undefined,
  now: Date,
  locale: string,
): string | null {
  if (isAbsentTimestamp(iso)) return null;
  const then = new Date(iso as string).getTime();
  let seconds = Math.round((now.getTime() - then) / 1000);
  if (seconds < 0) seconds = 0;
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  for (const step of STEPS) {
    if (seconds < step.limit) {
      return rtf.format(-Math.floor(seconds / step.div), step.unit);
    }
  }
  return null;
}

/** Absolute form for a tooltip; "" when absent. */
export function formatAbsoluteTime(iso: string | null | undefined, locale: string): string {
  if (isAbsentTimestamp(iso)) return "";
  return new Date(iso as string).toLocaleString(locale);
}
