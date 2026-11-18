import { describe, it, expect, vi } from 'vitest'
import { LanguageSwitcher } from './LanguageSwitcher'

function findNodes(node: any, predicate: (n: any) => boolean): any[] {
  if (!node) return []
  if (Array.isArray(node)) {
    return node.flatMap((item) => findNodes(item, predicate))
  }
  if (typeof node !== 'object') return []
  const results: any[] = []
  if (predicate(node)) results.push(node)
  if (node.props?.children) {
    results.push(...findNodes(node.props.children, predicate))
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

describe('LanguageSwitcher Component', () => {
  it('renders all 3 language options with radiogroup semantics', () => {
    const onSelect = vi.fn()
    const el = LanguageSwitcher({
      language: 'zh',
      onSelect,
    }) as any

    expect(el).toBeDefined()
    expect(el.props.role).toBe('radiogroup')

    const buttons = findNodes(el, (n) => n.type === 'button')
    expect(buttons).toHaveLength(3)

    const zhBtn = buttons[0]
    const enBtn = buttons[1]
    const jaBtn = buttons[2]

    expect(collectText(zhBtn)).toBe('简中')
    expect(zhBtn.props['aria-checked']).toBe(true)

    expect(collectText(enBtn)).toBe('EN')
    expect(enBtn.props['aria-checked']).toBe(false)

    expect(collectText(jaBtn)).toBe('日本語')
    expect(jaBtn.props['aria-checked']).toBe(false)

    // Trigger language switch to Japanese
    jaBtn.props.onClick()
    expect(onSelect).toHaveBeenCalledWith('ja')
  })

  it('renders active pill indicator corresponding to current language', () => {
    const el = LanguageSwitcher({
      language: 'ja',
      onSelect: vi.fn(),
    }) as any

    // Find sliding indicator div with transform
    const indicator = findNodes(el, (n) => n.props?.style?.transform?.includes('translateX'))[0]
    expect(indicator).toBeDefined()
    expect(indicator.props.style.transform).toBe('translateX(200%)')
  })

  it('renders compact single button switcher when compact is true and cycles language on click', () => {
    const onSelect = vi.fn()
    const el = LanguageSwitcher({
      language: 'zh',
      onSelect,
      compact: true,
    }) as any

    const btn = findNodes(el, (n) => n.type === 'button')[0]
    expect(btn).toBeDefined()
    expect(collectText(btn)).toBe('中')

    // Click cycles zh -> en
    btn.props.onClick()
    expect(onSelect).toHaveBeenCalledWith('en')
  })
})
