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

    expect(translate('zh', 'modals.androidPatch.browserNames.skyleap')).toBe('DeNA SkyLeap')
    expect(translate('en', 'modals.androidPatch.browserNames.skyleap')).toBe('DeNA SkyLeap')
    expect(translate('ja', 'modals.androidPatch.browserNames.skyleap')).toBe('DeNA SkyLeap')

    expect(translate('zh', 'modals.androidPatch.tagOfficial')).toBe('官方推荐')
    expect(translate('en', 'modals.androidPatch.tagOfficial')).toBe('Recommended')
    expect(translate('ja', 'modals.androidPatch.tagOfficial')).toBe('公式推奨')

    expect(translate('zh', 'modals.androidPatch.tagStandaloneUnsupported')).toBe('独立内核暂不支持')
    expect(translate('en', 'modals.androidPatch.tagStandaloneUnsupported')).toBe('Standalone Engine Unsupported')
    expect(translate('ja', 'modals.androidPatch.tagStandaloneUnsupported')).toBe('独自エンジン非対応')
  })

  it('interpolates variables properly', () => {
    expect(translate('zh', 'header.updateFound', { version: '2.5.0' })).toBe('发现新版 v2.5.0')
    expect(translate('en', 'header.updateFound', { version: '2.5.0' })).toBe('Update v2.5.0 Available')
    expect(translate('ja', 'header.updateFound', { version: '2.5.0' })).toBe('新バージョン v2.5.0 が利用可能')

    expect(translate('zh', 'header.runningWithPort', { port: 8124 })).toBe('运行中 (监听端口 8124)')
    expect(translate('en', 'header.runningWithPort', { port: 8124 })).toBe('Running (Port 8124)')
    expect(translate('ja', 'header.runningWithPort', { port: 8124 })).toBe('稼働中 (ポート 8124)')
  })

  it('verifies refined localization terminology and step punctuation', () => {
    expect(translate('en', 'common.error')).toBe('Error')
    expect(translate('ja', 'common.error')).toBe('エラー')

    expect(translate('en', 'cards.optim.browserCache')).toBe('Aggressive Browser Caching (immutable)')
    expect(translate('ja', 'cards.optim.browserCache')).toBe('ブラウザキャッシュ制御 (immutable)')

    expect(translate('en', 'modals.androidPatch.backupBtn')).toBe('Backup Directory')
    expect(translate('en', 'modals.androidPatch.installAllEnvSize')).toBe('Install Toolchain (~120 MB)')
    expect(translate('ja', 'modals.androidPatch.desc')).toContain('Goネイティブの')

    const enStep1 = translate('en', 'modals.routingGuide.methodAStep1')
    const jaStep1 = translate('ja', 'modals.routingGuide.methodAStep1')
    expect(enStep1.endsWith('.')).toBe(true)
    expect(jaStep1.endsWith('。')).toBe(true)
    expect(enStep1).not.toContain(';')
    expect(jaStep1).not.toContain(';')
    expect(jaStep1).not.toContain('；')

    // Verify asset terminology across Japanese locale
    expect(translate('ja', 'cards.cache.title')).toBe('ローカルキャッシュ & アセット保存')
    expect(translate('ja', 'cards.optim.prefetch')).toBe('静的アセットのスムーズプリフェッチ')
    expect(translate('ja', 'cards.optim.browserCacheEnabled')).toBe('ブラウザキャッシュ制御を有効化しました')
    expect(translate('ja', 'liveLogs.keepaliveReuse')).toBe('Keep-Alive 再利用: ')
    expect(translate('ja', 'modals.clearCache.categoryDescriptions.core_ui')).toContain('基本UI、アイコン')
    expect(translate('ja', 'modals.boost.subtitle')).toBe('高頻度アセットをRAM常駐プールに展開し、0msの超高速読込を実現')
    expect(translate('ja', 'modals.audit.valid')).toBe('正常アセット')
    expect(translate('ja', 'modals.audit.starting')).toBe('開始中...')
    expect(translate('ja', 'modals.clearCache.categories.other')).toBe('その他基本アセット')
    expect(translate('ja', 'modals.androidPatch.uninstallConfirm')).toContain('完全に解放しますか？')
    expect(translate('ja', 'modals.androidPatch.backupBtn')).toBe('元パッケージ保存先')
    expect(translate('ja', 'modals.androidPatch.step3')).toBe('3. LSPatch 適用')

    function collectStrings(obj: any): string[] {
      let result: string[] = []
      for (const val of Object.values(obj)) {
        if (typeof val === 'string') {
          result.push(val)
        } else if (typeof val === 'object' && val !== null) {
          result = result.concat(collectStrings(val))
        }
      }
      return result
    }

    const jaStrings = collectStrings(ja)
    for (const str of jaStrings) {
      expect(str).not.toContain('素材')
      expect(str).not.toContain('主画面')
      expect(str).not.toContain('強キャッシュ')
      expect(str).not.toContain('持続接続')
      expect(str).not.toContain('常用UI')
      expect(str).not.toContain('元版')
      expect(str).not.toContain('フォルダー')
      expect(str).not.toContain('パッチ注入')
    }
  })

  it('falls back safely for unknown keys', () => {
    expect(translate('en', 'nonexistent.key')).toBe('nonexistent.key')
    expect(translate('ja', 'nonexistent.key')).toBe('nonexistent.key')
  })
})
