import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  fetchHealth,
  repairHealth,
  applyConfig,
  cancelAndroidPatch,
  formatBytesBinary,
  fetchCacheBreakdown,
  startCacheBreakdownScan,
  clearCacheCategories,
  slimCache,
  cancelCacheTask,
} from './api'
import { HealthResponse, RepairResponse } from './types'

describe('fetchHealth API', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('successfully fetches health check data from /api/health', async () => {
    const mockHealth: Partial<HealthResponse> = {
      status: 'ok',
      timestamp: '2026-10-02T19:30:00Z',
      summary: {
        total_checks: 8,
        passed: 8,
        warnings: 0,
        errors: 0,
      },
    }

    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => mockHealth,
    }) as any

    const res = await fetchHealth()
    expect(res.status).toBe('ok')
    expect(res.summary.total_checks).toBe(8)
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/health')
  })

  it('throws an error when Control Plane is unreachable or returns non-200', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
    }) as any

    await expect(fetchHealth()).rejects.toThrow('HTTP 502')
  })

  it('throws when network connection fails (Control Plane down)', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('Failed to fetch')) as any

    await expect(fetchHealth()).rejects.toThrow('Failed to fetch')
  })
})

describe('repairHealth API', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('successfully sends batch repair request to /api/health/repair', async () => {
    const mockRepairRes: Partial<RepairResponse> = {
      success: true,
      message: '修复完成',
      results: [],
    }

    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => mockRepairRes,
    }) as any

    const res = await repairHealth({ all: true })
    expect(res.success).toBe(true)
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/health/repair', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ all: true }),
    })
  })

  it('successfully sends single code repair request', async () => {
    const mockRepairRes: Partial<RepairResponse> = {
      success: true,
      message: '数据面已启动',
      results: [
        {
          code: 'DATA_PLANE_STOPPED',
          action: 'start_data_plane',
          success: true,
          message: '数据面已成功启动',
        },
      ],
    }

    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => mockRepairRes,
    }) as any

    const res = await repairHealth({ code: 'DATA_PLANE_STOPPED' })
    expect(res.success).toBe(true)
    expect(res.results.length).toBe(1)
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/health/repair', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code: 'DATA_PLANE_STOPPED' }),
    })
  })

  it('throws custom error message from server when repair fails', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      json: async () => ({ ok: false, error: 'repair_in_progress', message: '已有修复任务正在执行中' }),
    }) as any

    await expect(repairHealth({ code: 'DATA_PLANE_STOPPED' })).rejects.toThrow('已有修复任务正在执行中')
  })

  it('throws TypeError when connection is reset or network dropped during repair', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch')) as any

    await expect(repairHealth({ code: 'CONTROL_PLANE_NON_LOOPBACK' })).rejects.toThrow(TypeError)
  })

  it('verifies reconnection recovery pattern succeeds once Control Plane settles', async () => {
    // 1. Initial repair POST gets connection reset / TypeError
    // 2. Subsequent GET /api/health succeeds and verifies loopback status
    let callCount = 0
    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => {
      callCount++
      if (url === '/api/health/repair') {
        throw new TypeError('Failed to fetch')
      }
      if (url === '/api/health') {
        return {
          ok: true,
          json: async () => ({
            status: 'ok',
            core_health: {
              control_plane: {
                status: 'ok',
                code: 'CONTROL_PLANE_OK',
                name: '控制面 (Control Plane)',
              },
            },
          }),
        }
      }
      throw new Error(`Unexpected url: ${url}`)
    }) as any

    // First attempt to repair fails due to socket rebind drop
    await expect(repairHealth({ code: 'CONTROL_PLANE_NON_LOOPBACK' })).rejects.toThrow(TypeError)

    // Reconnection poll to /api/health verifies recovery
    const health = await fetchHealth()
    expect(health.status).toBe('ok')
    expect(health.core_health.control_plane.status).toBe('ok')
    expect(callCount).toBe(2)
  })
})

describe('applyConfig port migration & cancelAndroidPatch API', () => {
  const originalFetch = globalThis.fetch
  const originalWindow = (globalThis as any).window

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    ;(globalThis as any).window = originalWindow
  })

  it('dispatches gbf-control-port-migrating and preserves path/search/hash on control_port migration', async () => {
    vi.useFakeTimers()
    try {
      const dispatchedEvents: string[] = []
      const mockLocation = {
        port: '8125',
        protocol: 'http:',
        hostname: '127.0.0.1',
        pathname: '/console',
        search: '?standalone=1',
        hash: '#logs',
        href: 'http://127.0.0.1:8125/console?standalone=1#logs',
      }
      ;(globalThis as any).window = {
        location: mockLocation,
        dispatchEvent: (ev: Event) => {
          dispatchedEvents.push(ev.type)
          return true
        },
      }

      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({
          ok: true,
          control_url: 'http://127.0.0.1:8126',
          config: { control_port: 8126 },
        }),
      }) as any

      await applyConfig({ control_port: 8126 })
      expect(dispatchedEvents).toContain('gbf-control-port-migrating')
      vi.advanceTimersByTime(600)
      expect(mockLocation.href).toBe('http://127.0.0.1:8126/console?standalone=1#logs')
    } finally {
      vi.useRealTimers()
    }
  })

  it('returns warnings array and reconciled config when Post-Commit sync encounters warnings', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        ok: true,
        message: 'Configuration updated',
        warnings: ['系统代理设置失败: simulated PAC error'],
        config: { listen_port: 8130, auto_system_proxy: false },
        control_url: 'http://127.0.0.1:8125',
      }),
    }) as any

    const res = await applyConfig({ listen_port: 8130, auto_system_proxy: true })
    expect(res.ok).toBe(true)
    expect(res.warnings).toEqual(['系统代理设置失败: simulated PAC error'])
    expect(res.config?.listen_port).toBe(8130)
    expect(res.config?.auto_system_proxy).toBe(false)
  })

  it('calls POST /api/android/patch/cancel in cancelAndroidPatch', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ ok: true, message: 'cancelled' }),
    }) as any

    const res = await cancelAndroidPatch()
    expect(res.ok).toBe(true)
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/android/patch/cancel', {
      method: 'POST',
    })
  })
})

describe('cache breakdown & targeted management APIs', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('formats binary byte units accurately in B, KiB, MiB, and GiB', () => {
    expect(formatBytesBinary(0)).toBe('0 B')
    expect(formatBytesBinary(512)).toBe('512 B')
    expect(formatBytesBinary(2048)).toBe('2.0 KiB')
    expect(formatBytesBinary(1048576 * 3.5)).toBe('3.50 MiB')
    expect(formatBytesBinary(1073741824 * 1.25)).toBe('1.25 GiB')
  })

  it('fetches breakdown snapshot and starts background scan', async () => {
    globalThis.fetch = vi.fn().mockResolvedValueOnce({
      ok: true,
      json: async () => ({ ok: true, has_snapshot: false, snapshot: null, is_scanning: false }),
    }).mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ ok: true, task_id: 'scan-100-1', task: 'scan', status: 'running' }),
    }) as any

    const b = await fetchCacheBreakdown()
    expect(b.has_snapshot).toBe(false)
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/cache/breakdown', { cache: 'no-store' })

    const s = await startCacheBreakdownScan()
    expect(s.task_id).toBe('scan-100-1')
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/cache/breakdown/scan', { method: 'POST' })
  })

  it('posts category cleanup with optional slim_versioned flag', async () => {
    globalThis.fetch = vi.fn().mockResolvedValueOnce({
      ok: true,
      status: 202,
      json: async () => ({ ok: true, task_id: 'clear_category-100-3', task: 'clear_category', status: 'running', categories: ['story'] }),
    }) as any

    const cat = await clearCacheCategories('snap-1-1', ['story'], ['gbf'], true)
    expect(cat.task_id).toBe('clear_category-100-3')
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/cache/clear-category', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        snapshot_id: 'snap-1-1',
        categories: ['story'],
        namespaces: ['gbf'],
        slim_versioned: true,
      }),
    })
  })

  it('passes snapshot_id in slimCache and task_id in cancelCacheTask', async () => {
    globalThis.fetch = vi.fn().mockResolvedValueOnce({
      ok: true,
      json: async () => ({ ok: true, task: 'slim', task_id: 'slim-1', keep: 8 }),
    }).mockResolvedValueOnce({
      ok: true,
      json: async () => ({ ok: true, task_id: 'slim-1', cancelled: true }),
    }) as any

    await slimCache(8, 'snap-1-1')
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/cache/slim?keep=8', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ keep: 8, snapshot_id: 'snap-1-1' }),
    })

    await cancelCacheTask('slim-1')
    expect(globalThis.fetch).toHaveBeenCalledWith('/api/cache/cancel-task', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ task_id: 'slim-1' }),
    })
  })
})


