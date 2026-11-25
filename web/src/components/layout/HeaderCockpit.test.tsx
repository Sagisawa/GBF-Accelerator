import { describe, it, expect, vi } from 'vitest'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { HeaderCockpit, HeaderCockpitProps } from './HeaderCockpit'
import {
  computeHeaderScrolledState,
  HEADER_COLLAPSE_SCROLL_TOP,
  HEADER_EXPAND_SCROLL_TOP,
} from '../../utils/headerScroll'
import { RuntimeStatus } from '../../types'

describe('HeaderCockpit Component', () => {
  const mockStatus: RuntimeStatus = {
    running: true,
    version: '2.6.0',
    platform: 'windows',
    requests: {
      total_hits: 1234,
      ram_hits: 567,
      cache_misses: 89,
      total_apis: 42,
    },
  } as any

  const defaultProps: HeaderCockpitProps = {
    status: mockStatus,
    isRunning: true,
    isScrolled: false,
    isTerminated: false,
    loadingProxy: false,
    currentListenPort: 8124,
    uptimeSec: 3600,
    updateInfo: null,
    onToggleProxy: vi.fn(),
    onOpenUpdate: vi.fn(),
    onOpenQuit: vi.fn(),
  }

  it('renders expanded state with 1fr grid strips and without compact top-bar HUD', () => {
    const html = renderToStaticMarkup(
      React.createElement(HeaderCockpit, {
        ...defaultProps,
        isScrolled: false,
        updateInfo: { available: true, version: '2.7.0' },
      })
    )

    expect(html).toContain('grid-rows-[1fr]')
    expect(html).toContain('aria-hidden="false"')
    expect(html).not.toContain('grid-rows-[0fr]')
    expect(html).not.toContain('hidden lg:flex items-center justify-center flex-1')
    expect(html).toContain('v2.6.0')
    expect(html).toContain('2.7.0')
  })

  it('renders compact state with 0fr grid collapse, compact HUD, and sticky overflow-anchor protection', () => {
    const html = renderToStaticMarkup(
      React.createElement(HeaderCockpit, {
        ...defaultProps,
        isScrolled: true,
      })
    )

    expect(html).toContain('grid-rows-[0fr]')
    expect(html).toContain('aria-hidden="true"')
    expect(html).not.toContain('grid-rows-[1fr]')
    expect(html).toContain('hidden lg:flex items-center justify-center flex-1')
    expect(html).toContain('8124')
    expect(html).toContain('sticky')
    expect(html).toContain('pointer-events-none')
    expect(html).toContain('overflow-anchor:none')
  })
})

describe('computeHeaderScrolledState (production scroll hysteresis)', () => {
  it('stays expanded at and below HEADER_COLLAPSE_SCROLL_TOP', () => {
    expect(HEADER_COLLAPSE_SCROLL_TOP).toBe(84)
    expect(computeHeaderScrolledState(0, false)).toBe(false)
    expect(computeHeaderScrolledState(28, false)).toBe(false)
    expect(computeHeaderScrolledState(72, false)).toBe(false)
    expect(computeHeaderScrolledState(84, false)).toBe(false)
  })

  it('collapses when scrolling past HEADER_COLLAPSE_SCROLL_TOP', () => {
    expect(computeHeaderScrolledState(85, false)).toBe(true)
    expect(computeHeaderScrolledState(120, false)).toBe(true)
  })

  it('remains collapsed inside the hysteresis deadband (41..84)', () => {
    expect(HEADER_EXPAND_SCROLL_TOP).toBe(40)
    expect(computeHeaderScrolledState(84, true)).toBe(true)
    expect(computeHeaderScrolledState(60, true)).toBe(true)
    expect(computeHeaderScrolledState(41, true)).toBe(true)
  })

  it('expands only when scrolling back to or below HEADER_EXPAND_SCROLL_TOP', () => {
    expect(computeHeaderScrolledState(40, true)).toBe(false)
    expect(computeHeaderScrolledState(14, true)).toBe(false)
    expect(computeHeaderScrolledState(0, true)).toBe(false)
  })

  it('does not bounce at bottom on tall viewports (maxScroll = 86px with constant slot height)', () => {
    let scrolled = false
    const maxScroll = 86

    // Scroll to bottom: scrollHeight remains constant because slot height is locked
    scrolled = computeHeaderScrolledState(maxScroll, scrolled)
    expect(scrolled).toBe(true)

    // Subsequent scroll event at unchanged bottom scrollTop stays collapsed
    scrolled = computeHeaderScrolledState(maxScroll, scrolled)
    expect(scrolled).toBe(true)
  })
})
