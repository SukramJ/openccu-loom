/**
 * What the onboarding form settled about a system: its type, how it is
 * reached, and — for openccu-lite — how the daemon authenticates. The
 * central form and the setup wizard turn it into their payloads.
 */
export type OnboardingValue = {
  /** "" until probed (or known from a stored central). */
  systemType: "" | "ccu" | "openccu-lite" | "unknown";
  tls: boolean;
  tlsFingerprint: string;
  /** The operator compared the fingerprint with the box. */
  fingerprintConfirmed: boolean;
  mode: "pair" | "token";
  /** An approved pairing's id; "" otherwise. */
  pairingId: string;
  apiToken: string;
};

export function emptyOnboarding(): OnboardingValue {
  return {
    systemType: "",
    tls: false,
    tlsFingerprint: "",
    fingerprintConfirmed: false,
    mode: "pair",
    pairingId: "",
    apiToken: "",
  };
}

/**
 * Whether an openccu-lite system can be saved: a fingerprint to pin must
 * have been compared, and a credential must be at hand — an approved
 * pairing, a pasted token, or (when editing) the token already stored.
 */
export function liteReady(v: OnboardingValue, tokenStored = false): boolean {
  if (v.tls && v.tlsFingerprint !== "" && !v.fingerprintConfirmed) return false;
  if (v.mode === "pair") return v.pairingId !== "" || tokenStored;
  return v.apiToken.trim() !== "" || tokenStored;
}

/** The credential fields a central write carries for an openccu-lite system. */
export function liteCredential(v: OnboardingValue): { pairing_id?: string; api_token?: string } {
  if (v.mode === "pair" && v.pairingId) return { pairing_id: v.pairingId };
  if (v.mode === "token" && v.apiToken.trim()) return { api_token: v.apiToken.trim() };
  return {};
}
