import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// Stateful mini-React harness for testing modal component hooks & element trees in Node without jsdom
let hookStates: any[] = []
let hookDeps: (any[] | undefined)[] = []
let pendingEffects: { idx: number; fn: () => any }[] = []
let effectCleanups: Record<number, (() => void) | undefined> = {}
let hookCursor = 0
let currentRenderFn: (() => any) | null = null
let lastTree: any = null

function depsChanged(prev: any[] | undefined, next: any[] | undefined): boolean {
  if (!prev || !next) return true
  if (prev.length !== next.length) return true
  for (let i = 0; i < prev.length; i++) {
    if (!Object.is(prev[i], next[i])) return true
  }
  return false
}

function flushEffects() {
  while (pendingEffects.length > 0) {
    const batch = pendingEffects.splice(0, pendingEffects.length)
    for (const eff of batch) {
      if (typeof effectCleanups[eff.idx] === 'function') {
        effectCleanups[eff.idx]!()
        effectCleanups[eff.idx] = undefined
      }
      const cleanup = eff.fn()
      if (typeof cleanup === 'function') {
        effectCleanups[eff.idx] = cleanup
      }
    }
  }
}

function renderTree() {
  if (!currentRenderFn) return null
  hookCursor = 0
  const raw = currentRenderFn()
  lastTree = expandNode(raw)
  flushEffects()
  return lastTree
}

function expandNode(node: any): any {
  if (node == null || typeof node === 'boolean' || typeof node === 'string' || typeof node === 'number') {
    return node
  }
  if (Array.isArray(node)) {
    return node.map(expandNode)
  }
  if (typeof node === 'object' && 'type' in node) {
    if (typeof node.type === 'function') {
      // Expand stateless functional child components (like Modal, Button, icons)
      const rendered = node.type(node.props || {})
      return expandNode(rendered)
    }
    const children = node.props?.children !== undefined ? expandNode(node.props.children) : undefined
    return {
      ...node,
      props: {
        ...node.props,
        children,
      },
    }
  }
  return node
}

function mountComponent(fn: () => any) {
  hookStates = []
  hookDeps = []
  pendingEffects = []
  effectCleanups = {}
  currentRenderFn = fn
  return renderTree()
}

function unmountComponent() {
  for (const k of Object.keys(effectCleanups)) {
    const cleanup = effectCleanups[Number(k)]
    if (typeof cleanup === 'function') cleanup()
  }
  effectCleanups = {}
  currentRenderFn = null
  lastTree = null
}

function collectText(node: any): string {
  if (node == null || typeof node === 'boolean') return ''
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(collectText).join('')
  if (typeof node === 'object' && node.props) {
    return collectText(node.props.children)
  }
  return ''
}

function findElements(
  node: any,
  pred: (el: { type: any; props: any; text: string }) => boolean,
  acc: { type: any; props: any; text: string }[] = []
): { type: any; props: any; text: string }[] {
  if (!node || typeof node !== 'object') return acc
  if (Array.isArray(node)) {
    for (const child of node) findElements(child, pred, acc)
    return acc
  }
  if ('type' in node && node.props) {
    const info = { type: node.type, props: node.props, text: collectText(node) }
    if (pred(info)) acc.push(info)
    if (node.props.children !== undefined) {
      findElements(node.props.children, pred, acc)
    }
  }
  return acc
}

vi.mock('react', async () => {
  const actual = await vi.importActual<typeof import('react')>('react')
  return {
    ...actual,
    default: {
      ...actual,
      createElement: actual.createElement,
    },
    useState: (initial: any) => {
      const states = hookStates
      const render = currentRenderFn
      const idx = hookCursor++
      if (!(idx in states)) {
        states[idx] = typeof initial === 'function' ? initial() : initial
      }
      const setState = (updater: any) => {
        if (hookStates !== states || currentRenderFn !== render) return
        const next = typeof updater === 'function' ? updater(states[idx]) : updater
        if (!Object.is(states[idx], next)) {
          states[idx] = next
          renderTree()
        }
      }
      return [states[idx], setState]
    },
    useRef: (initial: any) => {
      const idx = hookCursor++
      if (!(idx in hookStates)) {
        hookStates[idx] = { current: initial }
      }
      return hookStates[idx]
    },
    useCallback: (fn: any, deps?: any[]) => {
      const idx = hookCursor++
      if (!(idx in hookStates) || depsChanged(hookDeps[idx], deps)) {
        hookStates[idx] = fn
        hookDeps[idx] = deps
      }
      return hookStates[idx]
    },
    useMemo: (fn: () => any, deps?: any[]) => {
      const idx = hookCursor++
      if (!(idx in hookStates) || depsChanged(hookDeps[idx], deps)) {
        hookStates[idx] = fn()
        hookDeps[idx] = deps
      }
      return hookStates[idx]
    },
    useEffect: (fn: () => any, deps?: any[]) => {
      const idx = hookCursor++
      if (!(idx in hookDeps) || depsChanged(hookDeps[idx], deps)) {
        hookDeps[idx] = deps
        pendingEffects.push({ idx, fn })
      }
    },
  }
})

// Mock Modal to render its title and children directly without DOM portals
vi.mock('../common/Modal', () => ({
  Modal: ({ isOpen, title, subtitle, children }: any) => {
    if (!isOpen) return null
    return React.createElement('div', { 'data-modal': true }, title, subtitle, children)
  },
}))

import { ClearCacheModal, resolveDefaultNamespaces } from './ClearCacheModal'
import { AuditModal } from './AuditModal'
import { BoostModal } from './BoostModal'
import { LatencyTestModal, summarizeWarmLatency } from './LatencyTestModal'

const sampleSnapshot = (id: string) => ({
  snapshot_id: id,
  status: 'completed' as const,
  valid: true,
  stale: false,
  generation: 1,
  cache_base: 'D:\\cache',
  started_at_ms: 1700000000000,
  completed_at_ms: 1700000001000,
  scanned_files: 10,
  asset_files: 5,
  asset_bytes: 5120,
  metadata_files: 5,
  metadata_bytes: 512,
  orphan_metadata_files: 0,
  orphan_metadata_bytes: 0,
  excluded_files: 0,
  excluded_bytes: 0,
  categories: [
    {
      category: 'story',
      label: '剧情与立绘漫画',
      description: '剧情背景与立绘',
      selectable: true,
      asset_files: 5,
      asset_bytes: 5120,
      metadata_files: 5,
      metadata_bytes: 512,
      estimated_delete_bytes: 5632,
      percentage: 100,
    },
  ],
  namespaces: [
    {
      namespace: 'gbf',
      asset_files: 5,
      asset_bytes: 5120,
      metadata_files: 5,
      metadata_bytes: 512,
      estimated_delete_bytes: 5632,
      categories: {
        story: {
          asset_files: 5,
          asset_bytes: 5120,
          metadata_files: 5,
          metadata_bytes: 512,
          estimated_delete_bytes: 5632,
        },
      },
    },
  ],
  retention_previews: {
    '8': {
      keep: 8,
      total_groups: 1,
      total_version_dirs: 2,
      kept_dirs: 2,
      prune_dirs: 0,
      prune_asset_files: 0,
      prune_asset_bytes: 0,
      prune_metadata_files: 0,
      prune_metadata_bytes: 0,
      estimated_delete_bytes: 0,
      groups: [],
    },
  },
})

async function flushMicrotasks(ticks = 6) {
  for (let i = 0; i < ticks; i++) {
    await Promise.resolve()
  }
}

describe('ClearCacheModal concurrency and state regressions', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    unmountComponent()
    globalThis.fetch = originalFetch
    vi.useRealTimers()
  })

  it('places 推荐不选 categories at the bottom, omits search tab, and resets category selection on rescan', async () => {
    let currentSnapId = 'snap-1'

    const multiCategorySnapshot = (id: string) => ({
      ...sampleSnapshot(id),
      categories: [
        {
          category: 'versioned',
          label: '游戏历史版本文件',
          description: '版本目录资源',
          selectable: false,
          asset_files: 2,
          asset_bytes: 1024,
          metadata_files: 2,
          metadata_bytes: 128,
          estimated_delete_bytes: 1152,
          percentage: 20,
        },
        {
          category: 'battle_cjs',
          label: '战斗动画与特效',
          description: '战斗帧动画',
          selectable: true,
          asset_files: 2,
          asset_bytes: 1024,
          metadata_files: 2,
          metadata_bytes: 128,
          estimated_delete_bytes: 1152,
          percentage: 20,
        },
        {
          category: 'story',
          label: '剧情与立绘漫画',
          description: '剧情背景与立绘',
          selectable: true,
          asset_files: 5,
          asset_bytes: 5120,
          metadata_files: 5,
          metadata_bytes: 512,
          estimated_delete_bytes: 5632,
          percentage: 20,
        },
        {
          category: 'core_ui',
          label: '常用界面与图标',
          description: '高频界面素材',
          selectable: true,
          asset_files: 2,
          asset_bytes: 1024,
          metadata_files: 2,
          metadata_bytes: 128,
          estimated_delete_bytes: 1152,
          percentage: 20,
        },
        {
          category: 'event_gacha',
          label: '活动与卡池宣传图',
          description: '活动与卡池',
          selectable: true,
          asset_files: 2,
          asset_bytes: 1024,
          metadata_files: 2,
          metadata_bytes: 128,
          estimated_delete_bytes: 1152,
          percentage: 10,
        },
        {
          category: 'audio',
          label: '语音与音乐音效',
          description: '语音与音效',
          selectable: true,
          asset_files: 2,
          asset_bytes: 1024,
          metadata_files: 2,
          metadata_bytes: 128,
          estimated_delete_bytes: 1152,
          percentage: 5,
        },
        {
          category: 'other',
          label: '其它基础资源',
          description: '未归类文件',
          selectable: true,
          asset_files: 1,
          asset_bytes: 512,
          metadata_files: 1,
          metadata_bytes: 64,
          estimated_delete_bytes: 576,
          percentage: 5,
        },
      ],
      namespaces: [
        {
          namespace: 'gbf',
          asset_files: 16,
          asset_bytes: 10752,
          metadata_files: 16,
          metadata_bytes: 1216,
          estimated_delete_bytes: 11968,
          categories: {},
        },
      ],
      retention_previews: {
        '8': {
          keep: 8,
          total_groups: 1,
          total_version_dirs: 10,
          kept_dirs: 8,
          prune_dirs: 2,
          prune_asset_files: 4,
          prune_asset_bytes: 2048,
          prune_metadata_files: 4,
          prune_metadata_bytes: 256,
          estimated_delete_bytes: 2304,
          groups: [],
        },
      },
    })

    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => {
      if (url === '/api/cache/breakdown/scan') {
        currentSnapId = 'snap-2'
        return {
          ok: true,
          status: 202,
          json: async () => ({ ok: true, task_id: 'scan-2', task: 'scan', status: 'running' }),
        }
      }
      if (url.startsWith('/api/cache/breakdown')) {
        return {
          ok: true,
          json: async () => ({
            ok: true,
            cache_base: 'D:\\cache',
            has_snapshot: true,
            is_scanning: false,
            snapshot: multiCategorySnapshot(currentSnapId),
          }),
        }
      }
      if (url === '/api/cache/task-status') {
        return {
          ok: true,
          json: async () => ({
            is_scanning: false,
            is_invalidating: false,
            is_clearing_category: false,
            is_slimming: false,
            is_auditing: false,
          }),
        }
      }
      throw new Error(`Unexpected URL: ${url}`)
    }) as any

    const onToast = vi.fn()
    const onRefresh = vi.fn()
    const onClose = vi.fn()

    mountComponent(() =>
      ClearCacheModal({
        isOpen: true,
        onClose,
        onRefresh,
        onToast,
      })
    )
    await flushMicrotasks()

    const fullText = collectText(lastTree)
    // 1. Search tab is removed
    expect(fullText).not.toContain('定点搜索与删除本地缓存')
    // 2. Wording uses 推荐不选 instead of 默认不选
    expect(fullText).toContain('推荐不选')
    expect(fullText).not.toContain('默认不选')

    // 3. Plain-language labels are rendered and 推荐不选 categories (battle_cjs, core_ui, other) are placed at the bottom
    const versionedIdx = fullText.indexOf('游戏历史版本文件')
    const storyIdx = fullText.indexOf('剧情与立绘漫画')
    const eventIdx = fullText.indexOf('活动与卡池宣传图')
    const audioIdx = fullText.indexOf('语音与音乐音效')
    const battleIdx = fullText.indexOf('战斗动画与特效')
    const coreUiIdx = fullText.indexOf('常用界面与图标')
    const otherIdx = fullText.indexOf('其它基础资源')

    expect(versionedIdx).toBeGreaterThan(0)
    expect(storyIdx).toBeGreaterThan(versionedIdx)
    expect(eventIdx).toBeGreaterThan(storyIdx)
    expect(audioIdx).toBeGreaterThan(eventIdx)
    expect(battleIdx).toBeGreaterThan(audioIdx)
    expect(coreUiIdx).toBeGreaterThan(battleIdx)
    expect(otherIdx).toBeGreaterThan(coreUiIdx)

    // 4. All 7 categories use checkboxes uniformly; selecting a category and rescanning resets selection
    const checkboxes = findElements(lastTree, (el) => el.type === 'input' && el.props.type === 'checkbox')
    expect(checkboxes.length).toBe(7)
    checkboxes[0].props.onChange()
    expect(collectText(lastTree)).toContain('已选 1 个类别')

    const rescanBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('重新扫描磁盘')
    )[0]
    await rescanBtn.props.onClick()
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('已选 0 个类别')
  })

  it('does not starve or discard slow (>1s) status poll responses in ClearCacheModal', async () => {
    vi.useFakeTimers()

    let statusCallCount = 0
    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith('/api/cache/breakdown')) {
        return Promise.resolve({
          ok: true,
          json: async () => ({
            ok: true,
            cache_base: 'D:\\cache',
            has_snapshot: true,
            is_scanning: false,
            snapshot: sampleSnapshot('snap-slow'),
          }),
        })
      }
      if (url === '/api/cache/task-status') {
        statusCallCount++
        const callNum = statusCallCount
        // Each status call takes 1500ms (> 1000ms interval)
        return new Promise((resolve) => {
          setTimeout(() => {
            if (callNum === 1) {
              resolve({
                ok: true,
                json: async () => ({
                  is_scanning: true,
                  active_task_id: 'scan-slow-1',
                  scan_progress: {
                    phase: 'scanning',
                    scanned_files: 77,
                    scanned_bytes: 4096,
                    done: false,
                    status: 'running',
                  },
                }),
              })
            } else {
              resolve({
                ok: true,
                json: async () => ({
                  is_scanning: false,
                  active_task_id: '',
                  scan_progress: {
                    phase: 'completed',
                    scanned_files: 120,
                    scanned_bytes: 8192,
                    done: true,
                    status: 'completed',
                  },
                }),
              })
            }
          }, 1500)
        })
      }
      throw new Error(`Unexpected URL: ${url}`)
    }) as any

    const onRefresh = vi.fn()
    mountComponent(() =>
      ClearCacheModal({
        isOpen: true,
        onClose: vi.fn(),
        onRefresh,
        onToast: vi.fn(),
      })
    )
    await flushMicrotasks()
    expect(statusCallCount).toBe(1)

    // At 1000ms, interval fires while call #1 (1500ms) is still in flight; single-flight guard must skip starting call #2
    await vi.advanceTimersByTimeAsync(1000)
    expect(statusCallCount).toBe(1)

    // At 1500ms, call #1 resolves and MUST be applied (not discarded by a newer interval seq)
    await vi.advanceTimersByTimeAsync(500)
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('77')
    expect(collectText(lastTree)).toContain('后台扫描中...')

    // At 2000ms, interval fires when no status request is in flight -> starts call #2
    await vi.advanceTimersByTimeAsync(500)
    expect(statusCallCount).toBe(2)

    // At 3000ms, interval skips while call #2 is still in flight
    await vi.advanceTimersByTimeAsync(1000)
    expect(statusCallCount).toBe(2)

    // At 3500ms, call #2 resolves -> terminal state is applied and onRefresh is called
    await vi.advanceTimersByTimeAsync(500)
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('有效快照')
    expect(onRefresh).toHaveBeenCalledTimes(1)

    unmountComponent()

    // Verify slimming slow polling (>1s) inside ClearCacheModal as well
    statusCallCount = 0
    globalThis.fetch = vi.fn().mockImplementation((url: string) => {
      if (url.startsWith('/api/cache/breakdown')) {
        return Promise.resolve({
          ok: true,
          json: async () => ({
            ok: true,
            cache_base: 'D:\\cache',
            has_snapshot: true,
            is_scanning: false,
            snapshot: sampleSnapshot('snap-slim'),
          }),
        })
      }
      if (url === '/api/cache/task-status') {
        statusCallCount++
        const callNum = statusCallCount
        return new Promise((resolve) => {
          setTimeout(() => {
            resolve({
              ok: true,
              json: async () =>
                callNum === 1
                  ? {
                      is_slimming: true,
                      active_task_id: 'slim-1',
                      slim_progress: { current_idx: 3, total_dirs: 5, deleted_files: 12, freed_bytes: 2048 },
                    }
                  : {
                      is_slimming: false,
                      active_task_id: '',
                      last_slim_result: {
                        status: 'completed',
                        deleted_dirs: 2,
                        deleted_asset_files: 20,
                        deleted_meta_files: 20,
                        freed_bytes: 4096,
                      },
                    },
            })
          }, 1500)
        })
      }
      throw new Error(`Unexpected URL: ${url}`)
    }) as any

    const onSlimRefresh = vi.fn()
    mountComponent(() =>
      ClearCacheModal({
        isOpen: true,
        onClose: vi.fn(),
        onRefresh: onSlimRefresh,
        onToast: vi.fn(),
      })
    )
    await flushMicrotasks()
    expect(statusCallCount).toBe(1)

    await vi.advanceTimersByTimeAsync(1000)
    expect(statusCallCount).toBe(1)

    await vi.advanceTimersByTimeAsync(500)
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('正在清理历史版本目录:')
    expect(collectText(lastTree)).toContain('3 / 5')

    await vi.advanceTimersByTimeAsync(500)
    expect(statusCallCount).toBe(2)

    await vi.advanceTimersByTimeAsync(1500)
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('最近一次版本清理完成')
    expect(onSlimRefresh).toHaveBeenCalledTimes(1)
  })

  it('displays scan failure banner when background scan fails in ClearCacheModal', async () => {
    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => {
      if (url.startsWith('/api/cache/breakdown')) {
        return {
          ok: true,
          json: async () => ({
            ok: true,
            cache_base: 'D:\\cache',
            has_snapshot: false,
            is_scanning: false,
            scan_progress: {
              phase: 'failed',
              done: true,
              status: 'failed',
              message: '缓存根目录不可访问',
            },
          }),
        }
      }
      if (url === '/api/cache/task-status') {
        return {
          ok: true,
          json: async () => ({
            is_scanning: false,
            scan_progress: {
              phase: 'failed',
              done: true,
              status: 'failed',
              message: '缓存根目录不可访问',
            },
          }),
        }
      }
      throw new Error(`Unexpected URL: ${url}`)
    }) as any

    mountComponent(() =>
      ClearCacheModal({
        isOpen: true,
        onClose: vi.fn(),
        onRefresh: vi.fn(),
        onToast: vi.fn(),
      })
    )
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('分类扫描失败：')
    expect(collectText(lastTree)).toContain('缓存根目录不可访问')
  })

  it('derives default namespaces from current snapshot when gbf is absent or empty', async () => {
    expect(
      resolveDefaultNamespaces({
        ...sampleSnapshot('snap-ns-only-other'),
        namespaces: [
          {
            namespace: 'mbga',
            asset_files: 7,
            asset_bytes: 7168,
            metadata_files: 7,
            metadata_bytes: 700,
            estimated_delete_bytes: 7868,
          },
        ],
      })
    ).toEqual(['mbga'])

    expect(
      resolveDefaultNamespaces({
        ...sampleSnapshot('snap-ns-gbf-empty'),
        namespaces: [
          {
            namespace: 'gbf',
            asset_files: 0,
            asset_bytes: 0,
            metadata_files: 0,
            metadata_bytes: 0,
            estimated_delete_bytes: 0,
          },
          {
            namespace: 'mbga',
            asset_files: 4,
            asset_bytes: 4096,
            metadata_files: 4,
            metadata_bytes: 400,
            estimated_delete_bytes: 4496,
          },
        ],
      })
    ).toEqual(['mbga'])

    let clearCategoryPayload: any = null
    const nonGbfSnap = {
      ...sampleSnapshot('snap-non-gbf'),
      namespaces: [
        {
          namespace: 'mbga',
          asset_files: 5,
          asset_bytes: 5120,
          metadata_files: 5,
          metadata_bytes: 512,
          estimated_delete_bytes: 5632,
          categories: {
            story: {
              category: 'story',
              label: '剧情与立绘漫画',
              description: '剧情背景与立绘',
              selectable: true,
              asset_files: 5,
              asset_bytes: 5120,
              metadata_files: 5,
              metadata_bytes: 512,
              estimated_delete_bytes: 5632,
              percentage: 100,
            },
          },
        },
      ],
    }

    globalThis.fetch = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.startsWith('/api/cache/breakdown')) {
        return {
          ok: true,
          json: async () => ({
            ok: true,
            cache_base: 'D:\\cache',
            has_snapshot: true,
            is_scanning: false,
            snapshot: nonGbfSnap,
          }),
        }
      }
      if (url === '/api/cache/task-status') {
        return {
          ok: true,
          json: async () => ({
            is_scanning: false,
            is_clearing_category: false,
            is_slimming: false,
            is_auditing: false,
          }),
        }
      }
      if (url === '/api/cache/clear-category') {
        clearCategoryPayload = init?.body ? JSON.parse(String(init.body)) : null
        return {
          ok: true,
          json: async () => ({
            ok: true,
            task_id: 'clean-mbga-1',
            task: 'clear_category',
            status: 'running',
            categories: ['story'],
          }),
        }
      }
      throw new Error(`Unexpected URL: ${url}`)
    }) as any

    const onClose = vi.fn()
    const onRefresh = vi.fn()
    const onToast = vi.fn()

    mountComponent(() =>
      ClearCacheModal({
        isOpen: true,
        onClose,
        onRefresh,
        onToast,
      })
    )
    await flushMicrotasks()

    const text = collectText(lastTree)
    expect(text).toContain('共 5 个文件')
    expect(text).toContain('mbga')

    const checkboxes = findElements(
      lastTree,
      (el) => el.type === 'input' && el.props.type === 'checkbox'
    )
    // First checkbox is the namespace filter ('mbga'), second is category ('story')
    expect(checkboxes.length).toBe(2)
    checkboxes[1].props.onChange()
    expect(collectText(lastTree)).toContain('已选 1 个类别 · 预计清理 5 个文件')

    const cleanBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('清理所选缓存')
    )[0]
    cleanBtn.props.onClick()

    const confirmBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('确认清理')
    )[0]
    await confirmBtn.props.onClick()
    await flushMicrotasks()

    expect(clearCategoryPayload).toEqual({
      snapshot_id: 'snap-non-gbf',
      categories: ['story'],
      namespaces: ['mbga'],
    })
  })

  it('sorts warm latency samples and uses min/median/max instead of temporal sample order', async () => {
    expect(
      summarizeWarmLatency({
        warm_samples: [320.4, 95.2, 180.1],
      })
    ).toEqual({
      samples: [95, 180, 320],
      min: 95,
      mid: 180,
      max: 320,
    })

    expect(
      summarizeWarmLatency({
        warm_samples: [320, 95, 180],
        warm_min_ms: 95,
        warm_mid_ms: 180,
        warm_max_ms: 320,
      })
    ).toEqual({
      samples: [95, 180, 320],
      min: 95,
      mid: 180,
      max: 320,
    })

    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => {
      if (url.startsWith('/api/latency-test')) {
        return {
          ok: true,
          json: async () => ({
            ok: true,
            target: 'https://game.granbluefantasy.jp/',
            route_desc: '直连模式',
            cold_ms: 410,
            warm_samples: [320, 95, 180],
          }),
        }
      }
      throw new Error(`Unexpected URL: ${url}`)
    }) as any

    mountComponent(() =>
      LatencyTestModal({
        isOpen: true,
        onClose: vi.fn(),
        isDirect: true,
      })
    )
    await flushMicrotasks()

    const text = collectText(lastTree)
    expect(text).toContain('最快 95 ms ｜ 中位 180 ms ｜ 最慢 320 ms')
  })

  it('enforces task_id cancellation and mutual exclusion in AuditModal and BoostModal', async () => {
    const cancelBodies: any[] = []
    let currentTaskStatus: any = {
      active_task_id: 'scan-99',
      active_task_type: 'scan',
      is_scanning: true,
      is_auditing: false,
      is_slimming: false,
      is_boosting: false,
      is_clearing_category: false,
    }

    ;(globalThis as any).window = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }

    globalThis.fetch = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/cache/task-status') {
        return {
          ok: true,
          json: async () => currentTaskStatus,
        }
      }
      if (url === '/api/cache/cancel-task') {
        cancelBodies.push(init?.body ? JSON.parse(String(init.body)) : null)
        return {
          ok: true,
          json: async () => ({ ok: true, cancelled: true }),
        }
      }
      throw new Error(`Unexpected URL: ${url}`)
    }) as any

    // 1. AuditModal disables start button when scan task is active
    mountComponent(() =>
      AuditModal({
        isOpen: true,
        onClose: vi.fn(),
        onRefresh: vi.fn(),
        onToast: vi.fn(),
      })
    )
    await flushMicrotasks()

    const auditStartBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('其他缓存任务执行中...')
    )[0]
    expect(auditStartBtn).toBeDefined()
    expect(auditStartBtn.props.disabled).toBe(true)
    unmountComponent()

    // 2. AuditModal passes active audit task_id when cancelling an active audit
    currentTaskStatus = {
      active_task_id: 'audit-42',
      active_task_type: 'audit',
      is_auditing: true,
      audit_progress: { scanned: 10, healthy: 9, corrupted: 1 },
    }
    mountComponent(() =>
      AuditModal({
        isOpen: true,
        onClose: vi.fn(),
        onRefresh: vi.fn(),
        onToast: vi.fn(),
      })
    )
    await flushMicrotasks()

    const cancelAuditBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('终止体检')
    )[0]
    await cancelAuditBtn.props.onClick()
    await flushMicrotasks()
    expect(cancelBodies).toEqual([{ task_id: 'audit-42' }])
    unmountComponent()

    // 3. BoostModal disables start button when another task is active, and passes boost task_id on cancel
    currentTaskStatus = {
      active_task_id: 'scan-99',
      active_task_type: 'scan',
      is_scanning: true,
      is_boosting: false,
    }
    mountComponent(() =>
      BoostModal({
        isOpen: true,
        onClose: vi.fn(),
        onRefresh: vi.fn(),
        onToast: vi.fn(),
        cacheStats: null,
      })
    )
    await flushMicrotasks()

    const boostStartBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('其他缓存任务执行中...')
    )[0]
    expect(boostStartBtn).toBeDefined()
    expect(boostStartBtn.props.disabled).toBe(true)
    unmountComponent()

    currentTaskStatus = {
      active_task_id: 'boost-77',
      active_task_type: 'boost',
      is_boosting: true,
      boost_progress: {
        state: 'loading',
        loaded_files: 5,
        total_files: 10,
        loaded_bytes: 1024,
        total_bytes: 2048,
        speed_bytes_sec: 512,
        resident_bytes: 1024,
        pending_bytes: 1024,
        is_paused_by_fg: false,
        done: false,
      },
    }
    mountComponent(() =>
      BoostModal({
        isOpen: true,
        onClose: vi.fn(),
        onRefresh: vi.fn(),
        onToast: vi.fn(),
        cacheStats: null,
      })
    )
    await flushMicrotasks()

    const cancelBoostBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('停止后续载入')
    )[0]
    await cancelBoostBtn.props.onClick()
    await flushMicrotasks()
    expect(cancelBodies).toEqual([{ task_id: 'audit-42' }, { task_id: 'boost-77' }])
  })
})
