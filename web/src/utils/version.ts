/**
 * GBF-Accelerator SemVer comparison utilities.
 * Mirrors engine/updater/updater.go logic for version parsing and pre-release ordering.
 */

export function parseVersion(v: string): number[] {
  let cleaned = v.trim().replace(/^v/i, '')
  const dashIdx = cleaned.search(/[-+]/)
  if (dashIdx >= 0) {
    cleaned = cleaned.substring(0, dashIdx)
  }
  const parts = cleaned.split('.')
  const res: number[] = []
  for (const p of parts) {
    const m = p.match(/^(\d+)/)
    res.push(m ? parseInt(m[1], 10) : 0)
  }
  while (res.length < 3) {
    res.push(0)
  }
  return res
}

export function preReleaseTag(v: string): string {
  let cleaned = v.trim().replace(/^v/i, '')
  const dashIdx = cleaned.indexOf('-')
  if (dashIdx < 0) return ''
  let pre = cleaned.substring(dashIdx + 1)
  const plusIdx = pre.indexOf('+')
  if (plusIdx >= 0) {
    pre = pre.substring(0, plusIdx)
  }
  return pre.toLowerCase()
}

export function comparePreRelease(a: string, b: string): number {
  if (a === b) return 0
  if (a === '') return 1 // stable release outranks pre-release
  if (b === '') return -1 // pre-release < stable release
  const as = a.split('.')
  const bs = b.split('.')
  const minLen = Math.min(as.length, bs.length)
  for (let i = 0; i < minLen; i++) {
    const aIsNum = /^\d+$/.test(as[i])
    const bIsNum = /^\d+$/.test(bs[i])
    if (aIsNum && bIsNum) {
      const an = parseInt(as[i], 10)
      const bn = parseInt(bs[i], 10)
      if (an !== bn) return an < bn ? -1 : 1
    } else if (aIsNum) {
      return -1 // numeric identifiers have lower precedence than non-numeric
    } else if (bIsNum) {
      return 1
    } else {
      if (as[i] !== bs[i]) return as[i] < bs[i] ? -1 : 1
    }
  }
  if (as.length < bs.length) return -1
  if (as.length > bs.length) return 1
  return 0
}

export function isNewerVersion(remote: string, current: string): boolean {
  const r = parseVersion(remote)
  const c = parseVersion(current)
  const maxLen = Math.max(r.length, c.length)
  while (r.length < maxLen) r.push(0)
  while (c.length < maxLen) c.push(0)
  for (let i = 0; i < maxLen; i++) {
    if (r[i] > c[i]) return true
    if (r[i] < c[i]) return false
  }
  return comparePreRelease(preReleaseTag(remote), preReleaseTag(current)) > 0
}
