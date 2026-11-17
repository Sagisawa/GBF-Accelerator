import { describe, it, expect, vi } from 'vitest'
import { Sidebar, SidebarProps } from './Sidebar'
import { translate } from '../../i18n'

function t(key: string): string {
  return translate('zh', key)
}

// Helper to find nodes by prop or attribute
function findNodes(node: any, predicate: (n: any) => boolean): any[] {
  if (!node || typeof node !== 'object') return []
  const results: any[] = []
  if (predicate(node)) results.push(node)
  if (node.props?.children) {
    const children = Array.isArray(node.props.children)
      ? node.props.children
      : [node.props.children]
    for (const child of children) {
      results.push(...findNodes(child, predicate))
    }
  }
  return results
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

describe('Sidebar Navigation Component', () => {
  const defaultProps: SidebarProps = {
    activeTab: 'core',
    onTabChange: vi.fn(),
    showAndroidPatch: true,
    isRunning: true,
    hasUpdate: false,
    loadingOpenFolder: false,
    onOpenLogs: vi.fn(),
    onOpenHealth: vi.fn(),
    onOpenLatency: vi.fn(),
    onOpenFolder: vi.fn(),
    onOpenClearCache: vi.fn(),
    onOpenRoutingGuide: vi.fn(),
    onCheckUpdate: vi.fn(),
    onOpenShortcuts: vi.fn(),
  }

  it('renders core and android tabs with text labels when showAndroidPatch is true', () => {
    const onTabChange = vi.fn()
    const el = Sidebar({
      ...defaultProps,
      activeTab: 'core',
      showAndroidPatch: true,
      onTabChange,
    }) as any

    expect(el).toBeDefined()
    expect(el.type).toBe('aside')

    // Find tab buttons
    const buttons = findNodes(el, (n) => n.type === 'button')
    expect(buttons.length).toBeGreaterThanOrEqual(8)

    // Check Home (首页) button
    const homeBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.overview'))
    expect(homeBtn).toBeDefined()
    expect(collectText(homeBtn)).toContain(t('sidebar.overview'))

    // Trigger android tab click
    const androidBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.android'))
    expect(androidBtn).toBeDefined()
    expect(collectText(androidBtn)).toContain(t('sidebar.android'))
    androidBtn.props.onClick()
    expect(onTabChange).toHaveBeenCalledWith('android')
  })

  it('hides android tab button when showAndroidPatch is false', () => {
    const el = Sidebar({
      ...defaultProps,
      showAndroidPatch: false,
    }) as any

    const buttons = findNodes(el, (n) => n.type === 'button')
    const androidBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.android'))
    expect(androidBtn).toBeUndefined()
  })

  it('wires tool action callbacks and renders their descriptive text labels', () => {
    const onOpenLogs = vi.fn()
    const onOpenHealth = vi.fn()
    const onOpenLatency = vi.fn()
    const onOpenFolder = vi.fn()
    const onOpenClearCache = vi.fn()
    const onOpenRoutingGuide = vi.fn()
    const onCheckUpdate = vi.fn()
    const onOpenShortcuts = vi.fn()

    const el = Sidebar({
      ...defaultProps,
      onOpenLogs,
      onOpenHealth,
      onOpenLatency,
      onOpenFolder,
      onOpenClearCache,
      onOpenRoutingGuide,
      onCheckUpdate,
      onOpenShortcuts,
    }) as any

    const buttons = findNodes(el, (n) => n.type === 'button')

    const logsBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.liveLogs'))
    expect(collectText(logsBtn)).toContain(t('sidebar.liveLogs'))
    logsBtn?.props.onClick()
    expect(onOpenLogs).toHaveBeenCalledTimes(1)

    const healthBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.diagnostics'))
    expect(collectText(healthBtn)).toContain(t('sidebar.diagnostics'))
    healthBtn?.props.onClick()
    expect(onOpenHealth).toHaveBeenCalledTimes(1)

    const latencyBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.latencyTest'))
    expect(collectText(latencyBtn)).toContain(t('sidebar.latencyTest'))
    latencyBtn?.props.onClick()
    expect(onOpenLatency).toHaveBeenCalledTimes(1)

    const folderBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.openFolder'))
    expect(collectText(folderBtn)).toContain(t('sidebar.openFolder'))
    folderBtn?.props.onClick()
    expect(onOpenFolder).toHaveBeenCalledTimes(1)

    const clearBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.clearCache'))
    expect(collectText(clearBtn)).toContain(t('sidebar.clearCache'))
    clearBtn?.props.onClick()
    expect(onOpenClearCache).toHaveBeenCalledTimes(1)

    const routingBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.routingTips'))
    expect(collectText(routingBtn)).toContain(t('sidebar.routingTips'))
    routingBtn?.props.onClick()
    expect(onOpenRoutingGuide).toHaveBeenCalledTimes(1)

    const updateBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.checkUpdate'))
    expect(collectText(updateBtn)).toContain(t('sidebar.checkUpdate'))
    updateBtn?.props.onClick()
    expect(onCheckUpdate).toHaveBeenCalledTimes(1)

    const shortcutsBtn = buttons.find((b) => b.props['aria-label'] === t('sidebar.shortcuts'))
    expect(collectText(shortcutsBtn)).toContain(t('sidebar.shortcuts'))
    shortcutsBtn?.props.onClick()
    expect(onOpenShortcuts).toHaveBeenCalledTimes(1)
  })

  it('renders update notification dot when hasUpdate is true', () => {
    const elWithUpdate = Sidebar({
      ...defaultProps,
      hasUpdate: true,
    }) as any

    const dotsWithUpdate = findNodes(elWithUpdate, (n) =>
      typeof n.props?.className === 'string' && n.props.className.includes('bg-amber-500')
    )
    expect(dotsWithUpdate.length).toBeGreaterThanOrEqual(1)

    const elNoUpdate = Sidebar({
      ...defaultProps,
      hasUpdate: false,
    }) as any

    const dotsNoUpdate = findNodes(elNoUpdate, (n) =>
      typeof n.props?.className === 'string' && n.props.className.includes('bg-amber-500')
    )
    expect(dotsNoUpdate.length).toBe(0)
  })
})
