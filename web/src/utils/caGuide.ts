export interface CaGuideStatusLike {
  ca_installed?: boolean
}

/**
 * First-launch Root CA install guide decision (restores the v1.6 behavior).
 *
 * Returns true exactly when the runtime status has authoritatively reported
 * the Root CA as NOT installed/trusted and the guide has not been auto
 * prompted yet in this app launch. Never prompts when the CA is already
 * trusted, when the status is still unknown, or when it was already shown.
 */
export function shouldAutoOpenCaGuide(
  status: CaGuideStatusLike | null | undefined,
  alreadyPrompted: boolean,
): boolean {
  if (alreadyPrompted) return false
  if (!status || typeof status.ca_installed !== 'boolean') return false
  return status.ca_installed === false
}
