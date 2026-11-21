import { describe, it, expect, vi } from 'vitest'
import { MobileNavDrawer, MobileNavDrawerProps } from './MobileNavDrawer'
import { translate } from '../../i18n'

function t(key: string): string {
  return translate('zh', key)
}

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

describe('MobileNavDrawer Component', () => {
  const defaultProps: MobileNavDrawerProps = {
    isOpen: true,
    onClose: vi.fn(),
    activeTab: 'core',
    onTabChange: vi.fn(),
    showAndroidPatch: true,
    isRunning: true,
    hasUpdate: false,
    loadingOpenFolder: false,
    language: 'zh',
    onLanguageSelect: vi.fn(),
    onOpenLogs: vi.fn(),
    onOpenHealth: vi.fn(),
    onOpenLatency: vi.fn(),
    onOpenFolder: vi.fn(),
    onOpenClearCache: vi.fn(),
    onOpenRoutingGuide: vi.fn(),
    onCheckUpdate: vi.fn(),
    onOpenShortcuts: vi.fn(),
  }

  it('returns null when isOpen is false', () => {
    const el = MobileNavDrawer({ ...defaultProps, isOpen: false })
    expect(el).toBeNull()
  })

  it('renders drawer and tab buttons when isOpen is true', () => {
    const onTabChange = vi.fn()
    const onClose = vi.fn()
    const el = MobileNavDrawer({
      ...defaultProps,
      isOpen: true,
      onTabChange,
      onClose,
    }) as any

    expect(el).toBeDefined()
    const buttons = findNodes(el, (n) => n.type === 'button')
    expect(buttons.length).toBeGreaterThanOrEqual(8)

    // Check overview tab button
    const overviewBtn = buttons.find((b) => collectText(b).includes(t('sidebar.overview')))
    expect(overviewBtn).toBeDefined()
    overviewBtn.props.onClick()
    expect(onTabChange).toHaveBeenCalledWith('core')
    expect(onClose).toHaveBeenCalled()
  })

  it('triggers tool actions and closes drawer', () => {
    const onOpenLogs = vi.fn()
    const onClose = vi.fn()
    const el = MobileNavDrawer({
      ...defaultProps,
      isOpen: true,
      onOpenLogs,
      onClose,
    }) as any

    const buttons = findNodes(el, (n) => n.type === 'button')
    const logsBtn = buttons.find((b) => collectText(b).includes(t('sidebar.liveLogs')))
    expect(logsBtn).toBeDefined()
    logsBtn.props.onClick()
    expect(onOpenLogs).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })
})
