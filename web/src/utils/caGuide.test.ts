import { describe, it, expect } from 'vitest'
import { shouldAutoOpenCaGuide } from './caGuide'

describe('shouldAutoOpenCaGuide (first-launch Root CA guide)', () => {
  it('triggers the guide on first authoritative not-installed status', () => {
    expect(shouldAutoOpenCaGuide({ ca_installed: false }, false)).toBe(true)
  })

  it('does not trigger when the CA is already installed/trusted', () => {
    expect(shouldAutoOpenCaGuide({ ca_installed: true }, false)).toBe(false)
  })

  it('does not re-trigger after the guide was already prompted once', () => {
    expect(shouldAutoOpenCaGuide({ ca_installed: false }, true)).toBe(false)
  })

  it('does not trigger while the CA status is still unknown', () => {
    expect(shouldAutoOpenCaGuide(null, false)).toBe(false)
    expect(shouldAutoOpenCaGuide(undefined, false)).toBe(false)
    expect(shouldAutoOpenCaGuide({}, false)).toBe(false)
    expect(shouldAutoOpenCaGuide({ ca_installed: undefined }, false)).toBe(false)
  })

  it('stays silent on the installed path even across repeated status updates', () => {
    // Simulates repeated SSE/polling updates with the CA already trusted.
    let prompted = false
    for (let i = 0; i < 3; i++) {
      if (shouldAutoOpenCaGuide({ ca_installed: true }, prompted)) prompted = true
    }
    expect(prompted).toBe(false)
  })

  it('prompts only once across repeated not-installed status updates', () => {
    // Simulates repeated SSE/polling updates with the CA not installed.
    let prompted = false
    let opens = 0
    for (let i = 0; i < 3; i++) {
      if (shouldAutoOpenCaGuide({ ca_installed: false }, prompted)) {
        prompted = true
        opens++
      }
    }
    expect(opens).toBe(1)
  })
})
