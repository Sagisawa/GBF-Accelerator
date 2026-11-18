import { describe, it, expect, vi } from 'vitest'
import { ThemeToggle } from './ThemeToggle'

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

describe('ThemeToggle Component', () => {
  it('renders expanded mode with label and triggers onToggle callback', () => {
    const onToggle = vi.fn()
    const el = ThemeToggle({
      theme: 'light',
      onToggle,
      compact: false,
    }) as any

    expect(el).toBeDefined()
    expect(el.type).toBe('button')

    const text = collectText(el)
    expect(text).toContain('深色模式')

    el.props.onClick()
    expect(onToggle).toHaveBeenCalledTimes(1)
  })

  it('renders dark mode label when theme is dark', () => {
    const el = ThemeToggle({
      theme: 'dark',
      onToggle: vi.fn(),
      compact: false,
    }) as any

    const text = collectText(el)
    expect(text).toContain('浅色模式')
  })

  it('renders compact mode with circular button and title', () => {
    const onToggle = vi.fn()
    const el = ThemeToggle({
      theme: 'light',
      onToggle,
      compact: true,
    }) as any

    const buttons = findNodes(el, (n) => n.type === 'button')
    expect(buttons).toHaveLength(1)

    const btn = buttons[0]
    expect(btn.props.className).toContain('w-10 h-10')
    expect(btn.props.title).toContain('深色模式')

    btn.props.onClick()
    expect(onToggle).toHaveBeenCalledTimes(1)
  })
})
