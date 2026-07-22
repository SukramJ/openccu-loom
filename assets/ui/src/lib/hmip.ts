// HmIP teach-in input helpers (SGTIN + device key) for the keyserver-less
// LOCAL install mode. Client-side validation is UX-only — the daemon
// re-normalises both values authoritatively (pkg/hmproto), including the
// Base32→Base16 key conversion the CCU WebUI performs.

/** The 32-character alphabet printed on HmIP device labels (no D/I/O/V). */
const KEY_LABEL_ALPHABET = /^[0-9ABCEFGHJKLMNPQRSTUWXYZ]+$/;

/** Strips label separators (dashes, spaces) and uppercases the rest. */
export function stripLabelSeparators(s: string): string {
  return s.replace(/[-\s]/g, "").toUpperCase();
}

/** Normalised SGTIN, or null when the input is not a valid 24-hex SGTIN. */
export function normalizeSgtin(s: string): string | null {
  const v = stripLabelSeparators(s);
  return /^[0-9A-F]{24}$/.test(v) ? v : null;
}

/**
 * True when the input is an acceptable HmIP device key: either the full
 * 32-hex form or the shorter Base32 label form (converted server-side).
 */
export function isValidHmIPKeyInput(s: string): boolean {
  const v = stripLabelSeparators(s);
  if (v.length === 0) return false;
  if (v.length >= 32) return /^[0-9A-F]{32}$/.test(v);
  return KEY_LABEL_ALPHABET.test(v);
}
