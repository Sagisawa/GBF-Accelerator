export const HEADER_COLLAPSE_SCROLL_TOP = 84
export const HEADER_EXPAND_SCROLL_TOP = 40

/**
 * Computes the sticky header collapsed state from scrollTop with a geometric hysteresis deadband.
 * Combined with HeaderCockpit's constant in-flow slot height (delta scrollHeight = 0),
 * scrollTop is never mutated by header transitions.
 */
export function computeHeaderScrolledState(
  scrollTop: number,
  currentlyScrolled: boolean
): boolean {
  if (!currentlyScrolled && scrollTop > HEADER_COLLAPSE_SCROLL_TOP) {
    return true
  }
  if (currentlyScrolled && scrollTop <= HEADER_EXPAND_SCROLL_TOP) {
    return false
  }
  return currentlyScrolled
}
