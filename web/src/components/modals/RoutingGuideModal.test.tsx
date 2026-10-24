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

  it('respects custom listen port in Scheme 2', () => {
    const el = RoutingGuideModal({
      isOpen: true,
      onClose: vi.fn(),
      listenPort: 9000,
    }) as any

    const modalContent = el?.props.children
    const sections = modalContent.props.children
    const scheme2Box = sections[1]
    const scheme2Text = collectText(scheme2Box)

    expect(scheme2Text).toContain('http://127.0.0.1:9000/proxy.pac')
  })
})
