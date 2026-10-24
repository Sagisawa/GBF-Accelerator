import { describe, it, expect, vi } from 'vitest'
import { RoutingGuideModal } from './RoutingGuideModal'

function collectText(node: any): string {
  if (node == null || typeof node === 'boolean') return ''
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(collectText).join('')
  if (typeof node === 'object' && node.props) {
    return collectText(node.props.children)
  }
  return ''
}

describe('RoutingGuideModal', () => {
  it('renders modal with Scheme 1 having no redundant PAC URL or copy button', () => {
    const el = RoutingGuideModal({
      isOpen: true,
      onClose: vi.fn(),
      listenPort: 8124,
    }) as any

    expect(el).toBeDefined()
    expect(el?.props.isOpen).toBe(true)

    const modalContent = el?.props.children
    const sections = modalContent.props.children
    const scheme1Box = sections[0]
    const scheme1Text = collectText(scheme1Box)

    // Scheme 1 should contain title and desc, but NOT contain PAC URL or copy button
    expect(scheme1Text).toContain('方案 1')
    expect(scheme1Text).toContain('自动配置系统 PAC 代理')
    expect(scheme1Text).not.toContain('http://127.0.0.1:8124/proxy.pac')
    expect(scheme1Text).not.toContain('PAC 脚本地址')
    expect(scheme1Text).not.toContain('复制')

    // Scheme 2 should still contain PAC URL for extension configuration
    const scheme2Box = sections[1]
    const scheme2Text = collectText(scheme2Box)
    expect(scheme2Text).toContain('方案 2')
    expect(scheme2Text).toContain('http://127.0.0.1:8124/proxy.pac')
  })

  it('defaults to port 8124 when listenPort is omitted', () => {
    const el = RoutingGuideModal({
      isOpen: true,
      onClose: vi.fn(),
    }) as any

    const modalContent = el?.props.children
    const sections = modalContent.props.children
    const scheme2Box = sections[1]
    const scheme2Text = collectText(scheme2Box)

    expect(scheme2Text).toContain('http://127.0.0.1:8124/proxy.pac')
  })

  it('respects custom listen port in Scheme 2 and Scheme 3', () => {
    const el = RoutingGuideModal({
      isOpen: true,
      onClose: vi.fn(),
      listenPort: 9000,
    }) as any

    const modalContent = el?.props.children
    const sections = modalContent.props.children
    const scheme2Box = sections[1]
    const scheme2Text = collectText(scheme2Box)
    const scheme3Box = sections[2]
    const scheme3Text = collectText(scheme3Box)

    expect(scheme2Text).toContain('http://127.0.0.1:9000/proxy.pac')
    expect(scheme3Text).toContain('127.0.0.1:9000')
  })

  it('passes isOpen=false to Modal when closed', () => {
    const el = RoutingGuideModal({
      isOpen: false,
      onClose: vi.fn(),
    }) as any

    expect(el?.props.isOpen).toBe(false)
  })

  it('ensures Scheme 1 exists and pacScriptUrl is deleted across all locales', async () => {
    const { zh } = await import('../../i18n/locales/zh')
    const { en } = await import('../../i18n/locales/en')
    const { ja } = await import('../../i18n/locales/ja')

    for (const [, dict] of [['zh', zh], ['en', en], ['ja', ja]] as const) {
      expect((dict as any).modals.routingGuide.scheme1Title).toBeTruthy()
      expect((dict as any).modals.routingGuide.scheme1Desc).toBeTruthy()
      expect((dict as any).modals.routingGuide.pacScriptUrl).toBeUndefined()
    }
  })
})
