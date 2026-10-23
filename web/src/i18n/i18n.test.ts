import { describe, it, expect } from 'vitest'
import { zh } from './locales/zh'
import { en } from './locales/en'
import { ja } from './locales/ja'
import { translate } from './context'

describe('i18n dictionaries and translation', () => {
  it('has identical key structure across zh, en, and ja', () => {
    function getKeys(obj: any, prefix = ''): string[] {
      let keys: string[] = []
      for (const k of Object.keys(obj)) {
        const full = prefix ? `${prefix}.${k}` : k
        if (typeof obj[k] === 'object' && obj[k] !== null && !Array.isArray(obj[k])) {
          keys = keys.concat(getKeys(obj[k], full))
        } else {
          keys.push(full)
        }
      }
      return keys.sort()
    }

    const zhKeys = getKeys(zh)
    const enKeys = getKeys(en)
    const jaKeys = getKeys(ja)

    expect(enKeys).toEqual(zhKeys)
    expect(jaKeys).toEqual(zhKeys)
  })

  it('translates strings correctly for all supported languages', () => {
    expect(translate('zh', 'common.ok')).toBe('确定')
    expect(translate('en', 'common.ok')).toBe('OK')
    expect(translate('ja', 'common.ok')).toBe('確定')

    expect(translate('zh', 'header.quitApp')).toBe('彻底退出')
    expect(translate('en', 'header.quitApp')).toBe('Exit')
    expect(translate('ja', 'header.quitApp')).toBe('終了')
  })

  it('interpolates variables properly', () => {
    expect(translate('zh', 'header.updateFound', { version: '2.5.0' })).toBe('发现新版 v2.5.0')
    expect(translate('en', 'header.updateFound', { version: '2.5.0' })).toBe('Update v2.5.0 Available')
    expect(translate('ja', 'header.updateFound', { version: '2.5.0' })).toBe('新バージョン v2.5.0 が利用可能')

    expect(translate('zh', 'header.runningWithPort', { port: 8124 })).toBe('运行中 (监听端口 8124)')
    expect(translate('en', 'header.runningWithPort', { port: 8124 })).toBe('Running (Port 8124)')
    expect(translate('ja', 'header.runningWithPort', { port: 8124 })).toBe('稼働中 (ポート 8124)')
  })

  it('falls back safely for unknown keys', () => {
    expect(translate('en', 'nonexistent.key')).toBe('nonexistent.key')
    expect(translate('ja', 'nonexistent.key')).toBe('nonexistent.key')
  })
})
