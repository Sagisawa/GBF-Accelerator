import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fetchHealth, repairHealth } from './api'
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

