import React from 'react'

export function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export function parseSearchTokens(query: string): string[] {
  if (!query) return []
  return query
    .trim()
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
}

export function matchesAllTokens(text: string, tokens: string[]): boolean {
  if (!tokens || tokens.length === 0) return true
  const lower = text.toLowerCase()
  return tokens.every((t) => lower.includes(t.toLowerCase()))
}

export function highlightText(
  text: string | undefined | null,
  tokens: string[],
  isActiveRow: boolean
): React.ReactNode {
  if (!text) return text ?? ''
  if (!tokens || tokens.length === 0) return text

  const validTokens = Array.from(
    new Set(
      tokens
        .map((t) => t.trim())
        .filter(Boolean)
    )
  )
    .map(escapeRegExp)
    .sort((a, b) => b.length - a.length)

  if (validTokens.length === 0) return text

  const regex = new RegExp(`(${validTokens.join('|')})`, 'gi')
  const parts = text.split(regex).filter((p) => p.length > 0)
  if (parts.length <= 1 && !regex.test(text)) return text

  return parts.map((part, i) => {
    const isMatch = validTokens.some((vt) => new RegExp(`^${vt}$`, 'i').test(part))
    if (isMatch) {
      return (
        <mark
          key={i}
          className={
            isActiveRow
              ? 'bg-amber-400 text-slate-950 font-bold px-0.5 rounded-2xs shadow-xs'
              : 'bg-amber-500/35 text-amber-200 px-0.5 rounded-2xs'
          }
        >
          {part}
        </mark>
      )
    }
    return part
  })
}
