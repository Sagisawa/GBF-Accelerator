import { describe, it, expect, vi } from 'vitest'
import { ToggleSwitch, SettingToggleRow } from './ToggleSwitch'

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

describe('ToggleSwitch Component', () => {
  it('renders switch role and handles click toggling', () => {
    const onChange = vi.fn()
    const el = ToggleSwitch({
      checked: true,
      onChange,
      ariaLabel: 'Test Switch',
    }) as any

    expect(el).toBeDefined()
    expect(el.props.role).toBe('switch')
    expect(el.props['aria-checked']).toBe(true)
    expect(el.props.className).toContain('bg-emerald-500')

    el.props.onClick()
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('renders off state with slate background', () => {
    const onChange = vi.fn()
    const el = ToggleSwitch({
      checked: false,
      onChange,
    }) as any

    expect(el.props['aria-checked']).toBe(false)
    expect(el.props.className).toContain('bg-slate-300')
  })

  it('respects disabled state', () => {
    const onChange = vi.fn()
    const el = ToggleSwitch({
      checked: true,
      disabled: true,
      onChange,
    }) as any

    expect(el.props.disabled).toBe(true)
    expect(el.props.className).toContain('opacity-50')
  })
})

describe('SettingToggleRow Component', () => {
  it('renders label, description, and switches state on row click', () => {
    const onChange = vi.fn()
    const el = SettingToggleRow({
      label: 'Feature Toggle',
      description: 'Enables high performance mode',
      checked: false,
      onChange,
    }) as any

    expect(el).toBeDefined()
    const text = collectText(el)
    expect(text).toContain('Feature Toggle')
    expect(text).toContain('Enables high performance mode')

    // Find the clickable label container
    const labelContainer = findNodes(el, (n) => typeof n.props?.onClick === 'function')[0]
    expect(labelContainer).toBeDefined()
    labelContainer.props.onClick()
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('does not trigger onChange when row is disabled and clicked', () => {
    const onChange = vi.fn()
    const el = SettingToggleRow({
      label: 'Disabled Feature',
      checked: false,
      disabled: true,
      onChange,
    }) as any

    const labelContainer = findNodes(el, (n) => typeof n.props?.onClick === 'function')[0]
    expect(labelContainer).toBeDefined()
    labelContainer.props.onClick()
    expect(onChange).not.toHaveBeenCalled()
  })
})
