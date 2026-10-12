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

  it('places 推荐不选 categories at the bottom, omits search tab, and resets category selection on rescan', async () => {
    let currentSnapId = 'snap-1'

    const multiCategorySnapshot = (id: string) => ({
      ...sampleSnapshot(id),
      categories: [
        {
          category: 'versioned',
          label: '版本化静态资源',
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
          label: '战斗动画与特效 (CJS)',
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
          label: '剧情与支线素材',
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
          label: '常驻 UI 与基础素材',
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
          label: '活动与宣发素材',
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
          label: '音频素材',
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
          label: '其它未归类素材',
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
