import { describe, it, expect } from 'vitest'
import {
  escapeRegExp,
  parseSearchTokens,
  matchesAllTokens,
  highlightText,
} from './highlight'

describe('highlight utilities', () => {
  describe('escapeRegExp', () => {
    it('escapes special regex characters correctly', () => {
      expect(escapeRegExp('test?.*+')).toBe('test\\?\\.\\*\\+')
      expect(escapeRegExp('(id=123)')).toBe('\\(id=123\\)')
    })
  })

  describe('parseSearchTokens', () => {
    it('handles empty or whitespace-only queries', () => {
      expect(parseSearchTokens('')).toEqual([])
      expect(parseSearchTokens('   ')).toEqual([])
    })

    it('splits multiple words by spaces and lowercases them', () => {
      expect(parseSearchTokens('duel 200')).toEqual(['duel', '200'])
      expect(parseSearchTokens('  QUEST   MULTI   GET  ')).toEqual(['quest', 'multi', 'get'])
    })
  })

  describe('matchesAllTokens', () => {
    it('returns true when tokens list is empty', () => {
      expect(matchesAllTokens('any text', [])).toBe(true)
    })

    it('returns true only when ALL tokens match the text (case-insensitive)', () => {
      const line = '200 GET game.granbluefantasy.jp/quest/content/duel_supporter?id=123'
      expect(matchesAllTokens(line, ['duel', '200'])).toBe(true)
      expect(matchesAllTokens(line, ['DUEL', 'QUEST', '200'])).toBe(true)
      expect(matchesAllTokens(line, ['duel', '404'])).toBe(false)
      expect(matchesAllTokens(line, ['gacha'])).toBe(false)
    })
  })

  describe('highlightText', () => {
    it('returns original string when text is empty or tokens is empty', () => {
      expect(highlightText('', ['duel'], false)).toBe('')
      expect(highlightText('hello world', [], false)).toBe('hello world')
    })

    it('returns split array with <mark> elements for matched tokens', () => {
      const result = highlightText('quest_duel_200', ['duel', '200'], false) as any[]
      expect(Array.isArray(result)).toBe(true)
      expect(result).toHaveLength(4)
      expect(result[0]).toBe('quest_')
      expect(result[1].type).toBe('mark')
      expect(result[1].props.children).toBe('duel')
      expect(result[1].props.className).toContain('text-amber-200')
      expect(result[2]).toBe('_')
      expect(result[3].type).toBe('mark')
      expect(result[3].props.children).toBe('200')
    })

    it('uses active highlight styling when isActiveRow is true', () => {
      const result = highlightText('quest_duel', ['duel'], true) as any[]
      expect(result[1].type).toBe('mark')
      expect(result[1].props.className).toContain('bg-amber-400')
      expect(result[1].props.className).toContain('text-slate-950')
    })

    it('handles special characters in tokens without throwing', () => {
      const result = highlightText('result?id=(123)', ['?id=', '(123)'], false) as any[]
      expect(Array.isArray(result)).toBe(true)
    })
  })
})
