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
      const idx = hookCursor++
      if (!(idx in hookStates)) {
        hookStates[idx] = typeof initial === 'function' ? initial() : initial
      }
      const setState = (updater: any) => {
        const next = typeof updater === 'function' ? updater(hookStates[idx]) : updater
        if (!Object.is(hookStates[idx], next)) {
          hookStates[idx] = next
          renderTree()
        }
      }
      return [hookStates[idx], setState]
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

import { ClearCacheModal } from './ClearCacheModal'
import { SlimModal } from './SlimModal'

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
      label: '剧情与支线素材',
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

describe('ClearCacheModal & SlimModal concurrency and state regressions', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    unmountComponent()
    globalThis.fetch = originalFetch
    vi.useRealTimers()
  })

  it('resets searching loading state when an in-flight search or completed search is invalidated by rescan', async () => {
    let currentSnapId = 'snap-1'
    let resolvePendingSearch: ((val: any) => void) | null = null
    let searchCalls = 0

    globalThis.fetch = vi.fn().mockImplementation(async (url: string) => {
      if (url.startsWith('/api/cache/search')) {
        searchCalls++
        if (searchCalls === 1) {
          return {
            ok: true,
            json: async () => ({
              ok: true,
              snapshot_id: 'snap-1',
              completed_at_ms: 1700000001000,
              query: 'scene/1.png',
              total_matches: 1,
              truncated: false,
              items: [
                {
                  ns: 'gbf',
                  key: 'assets/img/sp/quest/scene/1.png',
                  category: 'story',
                  category_label: '剧情与支线素材',
                  asset_bytes: 1024,
                  metadata_bytes: 128,
                  estimated_delete_bytes: 1152,
                  has_metadata: true,
                  file_modified_ms: 1700000000000,
                  file_modified_iso: '2023-11-14T22:13:20Z',
                },
              ],
            }),
          }
        }
        if (searchCalls === 2) {
          // Second search stays in-flight when user clicks rescan
          return new Promise((resolve) => {
            resolvePendingSearch = resolve
          })
        }
        return {
          ok: true,
          json: async () => ({
            ok: true,
            snapshot_id: 'snap-2',
            completed_at_ms: 1700000005000,
            query: 'scene/1.png',
            total_matches: 1,
            truncated: false,
            items: [
              {
                ns: 'gbf',
                key: 'assets/img/sp/quest/scene/2.png',
                category: 'story',
                category_label: '剧情与支线素材',
                asset_bytes: 2048,
                metadata_bytes: 128,
                estimated_delete_bytes: 2176,
                has_metadata: true,
                file_modified_ms: 1700000005000,
                file_modified_iso: '2023-11-14T22:13:25Z',
              },
            ],
          }),
        }
      }
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
            snapshot: sampleSnapshot(currentSnapId),
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

    // Switch to search tab
    const searchTabBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('定点搜索与删除本地缓存')
    )[0]
    expect(searchTabBtn).toBeDefined()
    searchTabBtn.props.onClick()

    // Enter query
    const searchInput = findElements(lastTree, (el) => el.type === 'input' && el.props.type === 'text')[0]
    expect(searchInput).toBeDefined()
    searchInput.props.onChange({ target: { value: 'scene/1.png' } })

    // 1. Normal search completes
    const searchForm = findElements(lastTree, (el) => el.type === 'form')[0]
    await searchForm.props.onSubmit({ preventDefault: vi.fn() })
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('assets/img/sp/quest/scene/1.png')

    // 2. Start a second search that remains in-flight, then trigger rescan
    const secondSearchPromise = searchForm.props.onSubmit({ preventDefault: vi.fn() })
    await flushMicrotasks(2)

    const searchBtnDuringFlight = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('搜索索引')
    )[0]
    expect(searchBtnDuringFlight.props.disabled).toBe(true)

    // Trigger rescan while search #2 is in-flight
    const rescanBtn = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('重新扫描磁盘')
    )[0]
    const rescanPromise = rescanBtn.props.onClick()
    await flushMicrotasks()
    await rescanPromise
    await flushMicrotasks()

    // Resolve the invalidated search #2 late
    ;(resolvePendingSearch as any)?.({
      ok: true,
      json: async () => ({
        ok: true,
        snapshot_id: 'snap-1',
        completed_at_ms: 1700000001000,
        query: 'scene/1.png',
        total_matches: 1,
        truncated: false,
        items: [],
      }),
    })
    await secondSearchPromise
    await flushMicrotasks()

    // Search button must NOT be stuck disabled/loading after rescan + new valid snapshot
    const searchBtnAfterRescan = findElements(
      lastTree,
      (el) => el.type === 'button' && el.text.includes('搜索索引')
    )[0]
    expect(searchBtnAfterRescan.props.disabled).toBeFalsy()

    // 3. Subsequent search #3 against snap-2 succeeds
    const latestForm = findElements(lastTree, (el) => el.type === 'form')[0]
    await latestForm.props.onSubmit({ preventDefault: vi.fn() })
    await flushMicrotasks()
    expect(searchCalls).toBe(3)
    expect(collectText(lastTree)).toContain('assets/img/sp/quest/scene/2.png')
  })

  it('does not starve or discard slow (>1s) status poll responses in ClearCacheModal and SlimModal', async () => {
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

    // Verify SlimModal slow polling (>1s) as well
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
      SlimModal({
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
    expect(collectText(lastTree)).toContain('正在清理历史版本目录...')
    expect(collectText(lastTree)).toContain('3 / 5')

    await vi.advanceTimersByTimeAsync(500)
    expect(statusCallCount).toBe(2)

    await vi.advanceTimersByTimeAsync(1500)
    await flushMicrotasks()
    expect(collectText(lastTree)).toContain('最近一次版本清理完成')
    expect(onSlimRefresh).toHaveBeenCalledTimes(1)
  })

  it('displays scan failure banner when background scan fails in ClearCacheModal and SlimModal', async () => {
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

    unmountComponent()

    mountComponent(() =>
      SlimModal({
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
})
