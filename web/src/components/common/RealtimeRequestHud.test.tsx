import { describe, it, expect } from 'vitest'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { RealtimeRequestHud } from '../../App'
import { RuntimeStatus } from '../../types'

describe('RealtimeRequestHud Component', () => {
  const mockStatus: RuntimeStatus = {
    running: true,
    requests: {
      total_hits: 1234,
      ram_hits: 567,
      cache_misses: 89,
      total_apis: 42,
    },
  } as any

  it('renders standard 3-card grid in expanded mode', () => {
    const html = renderToStaticMarkup(
      React.createElement(RealtimeRequestHud, {
        status: mockStatus,
        running: true,
        compact: false,
      })
    )

    expect(html).toContain('grid-cols-3')
    expect(html).toContain('1,234')
    expect(html).toContain('567')
    expect(html).toContain('89')
    expect(html).toContain('42')
  })

  it('renders compact inline pills in compact mode', () => {
    const html = renderToStaticMarkup(
      React.createElement(RealtimeRequestHud, {
        status: mockStatus,
        running: true,
        compact: true,
      })
    )

    expect(html).toContain('flex items-center')
    expect(html).not.toContain('grid-cols-3')
    expect(html).toContain('1,234')
    expect(html).toContain('89')
    expect(html).toContain('42')
    expect(html).toContain('rounded-full')
  })
})
