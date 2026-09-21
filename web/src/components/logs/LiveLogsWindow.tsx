import React, { useState, useRef, useEffect, useMemo, useCallback } from 'react'
import { LogItem, TelemetrySummary } from '../../types'
import {
  X,
  Play,
  Pause,
  Terminal,
} from 'lucide-react'

export interface LiveLogsWindowProps {
  isOpen: boolean
  onClose: () => void
  logs: LogItem[]
  onClearLogs: () => void
  telemetry?: TelemetrySummary
  hitRatioPercent?: number
  isStandalone?: boolean
}

// --- Privacy Desensitization Engine (Strictly 5 asterisks '*****') ---
const RE_PRIVACY_QUERY = /([?&](?:uid|_|[tT]|timestamp|token|session|auth|password|signature)=)[^&\s)]+/gi
const RE_PRIVACY_RAID = /((?:\/supporter_raid\/|(?:normal_)?multiraid\/content\/index\/|raid(?:\/content\/index\/)?|(?:\/|\b)raid))\d{8,14}(\b|\?|$|\/)/g
const RE_PRIVACY_GUILD = /((?:\/|\b)guild)\d{5,10}(\b|\?|$|\/)/g
const RE_PRIVACY_USER = /(\/(?:profile|user)\/(?:content\/index|status)\/)\d+(\b|\?|$|\/)/g
const RE_PRIVACY_IP = /\b(192\.168\.\d+\.|10\.\d+\.\d+\.|172\.(?:1[6-9]|2\d|3[01])\.\d+\.)\d+\b/g

export function maskLogPrivacy(line: string): string {
  if (!line) return line
  line = line.replace(RE_PRIVACY_QUERY, '$1*****')
  line = line.replace(RE_PRIVACY_RAID, '$1*****$2')
  line = line.replace(RE_PRIVACY_GUILD, '$1*****$2')
  line = line.replace(RE_PRIVACY_USER, '$1*****$2')
  line = line.replace(RE_PRIVACY_IP, '$1*****')
  return line
}

// --- Columnar Alignment Parser (Aligned with Python main _align_log_line) ---
export interface ParsedLogLine {
  raw: string
  time: string
  levelTag: string
  method?: string
  statusCode?: string
  latencyMs?: number
  latencyStr?: string
  stateTag?: string
  hostTag?: string
  urlPath?: string
  urlQuery?: string
  urlExtra?: string
  isError: boolean
  isSystem: boolean
  systemMsg?: string
}

function parseLogLine(
  rawLine: string,
  timeStr: string,
  lvlStr: string,
  align: boolean,
  compactDomain: boolean,
  maskPrivacy: boolean
): ParsedLogLine {
  let line = rawLine.trim()
  if (maskPrivacy) {
    line = maskLogPrivacy(line)
  }

  // Extract raw level tag if line starts with [TAG]
  let tag = lvlStr || 'INFO'
  let msg = line
  const tagMatch = line.match(/^\[([^\]]+)\]\s*(.*)$/)
  if (tagMatch) {
    tag = tagMatch[1]
    msg = tagMatch[2]
  }

  const isError =
    tag.includes('ERR') ||
    tag.includes('BLOCK') ||
    msg.includes('500 ') ||
    msg.includes('502 ') ||
    msg.includes('503 ') ||
    msg.includes('504 ') ||
    msg.includes('TIMEOUT')

  // If not aligned, return simple formatted line
  if (!align) {
    return {
      raw: line,
      time: timeStr,
      levelTag: tag,
      isError,
      isSystem: true,
      systemMsg: msg,
    }
  }

  // 1. BYPASS-API or BYPASS-PLAIN-API
  if (tag.includes('API')) {
    // e.g.: 200 GET game.granbluefantasy.jp/rest/guild/main/guild_info?_=*****&t=*****&uid=***** (92ms, HTTP/1.1, reused)
    const m = msg.match(
      /^(\d+)\s+([A-Z]+)\s+(.+?)(?:\s+\((\d+)ms(?:,\s*[^,\)]+)*(?:,\s*(reused|new))?\))?$/
    )
    if (m) {
      const code = m[1]
      const method = m[2]
      let fullUrl = m[3]
      const ms = m[4] ? parseInt(m[4], 10) : undefined
      const reuse = m[5] || ''

      let hostTag: string | undefined
      let path = fullUrl
      if (compactDomain) {
        if (fullUrl.includes('game.granbluefantasy.jp')) {
          hostTag = '[gbf  ]'
          path = fullUrl.split('game.granbluefantasy.jp')[1] || '/'
        } else if (fullUrl.includes('akamaized.net')) {
          hostTag = '[asset]'
          path = '/' + (fullUrl.split('/')[1] || fullUrl)
        } else if (fullUrl.includes('mbga.jp')) {
          hostTag = '[mbga ]'
          path = fullUrl.split('mbga.jp')[1] || '/'
        } else if (fullUrl.includes('ws.')) {
          hostTag = '[ws   ]'
          path = fullUrl
        } else {
          hostTag = '[other]'
        }
      }

      const qIdx = path.indexOf('?')
      const urlPath = qIdx !== -1 ? path.substring(0, qIdx) : path
      const urlQuery = qIdx !== -1 ? path.substring(qIdx) : ''

      return {
        raw: line,
        time: timeStr,
        levelTag: tag,
        method,
        statusCode: code,
        latencyMs: ms,
        latencyStr: ms !== undefined ? `${ms.toString().padStart(4, ' ')}ms` : '   -ms',
        stateTag: reuse ? `[${reuse.padEnd(6, ' ')}]` : '[      ]',
        hostTag,
        urlPath,
        urlQuery,
        isError,
        isSystem: false,
      }
    }
  }

  // 2. FETCH-ASSET
  if (tag.includes('FETCH')) {
    // New format: 200 184ms -> host/path (N B)
    // Legacy format: 200 OK -> host/path (N B) or 200 OK & STREAMED (132ms) -> ...
    const m = msg.match(/200\s+(\d+)ms\s+->\s*([^\s(]+)(.*)/)
    if (m) {
      const ms = parseInt(m[1], 10)
      const fullUrl = m[2]
      const extra = m[3] || ''
      let path = fullUrl
      let hostTag: string | undefined
      if (compactDomain && fullUrl.includes('akamaized.net')) {
        hostTag = '[asset]'
        path = '/' + (fullUrl.split('/')[1] || fullUrl)
      }
      return {
        raw: line,
        time: timeStr,
        levelTag: tag,
        method: 'GET ',
        statusCode: '200',
        latencyMs: ms,
        latencyStr: `${ms.toString().padStart(4, ' ')}ms`,
        stateTag: '[fetch ]',
        hostTag,
        urlPath: path,
        urlExtra: extra,
        isError,
        isSystem: false,
      }
    }
    // Legacy: 200 OK & STREAMED or 200 OK
    const mLegacy = msg.match(/(?:200 OK & STREAMED \((\d+)ms\)|200 OK)\s*->\s*([^\s(]+)(.*)/)
    if (mLegacy) {
      const ms = mLegacy[1] ? parseInt(mLegacy[1], 10) : undefined
      const fullUrl = mLegacy[2]
      const extra = mLegacy[3] || ''
      let path = fullUrl
      let hostTag: string | undefined
      if (compactDomain && fullUrl.includes('akamaized.net')) {
        hostTag = '[asset]'
        path = '/' + (fullUrl.split('/')[1] || fullUrl)
      }
      return {
        raw: line,
        time: timeStr,
        levelTag: tag,
        method: 'GET ',
        statusCode: '200',
        latencyMs: ms,
        latencyStr: ms !== undefined ? `${ms.toString().padStart(4, ' ')}ms` : '   -ms',
        stateTag: '[stream]',
        hostTag,
        urlPath: path,
        urlExtra: extra,
        isError,
        isSystem: false,
      }
    }
  }

  // 3. CACHE (CACHE-DISK / CACHE-RAM)
  if (tag.includes('CACHE')) {
    const isDisk = tag.includes('DISK')
    const tagLabel = isDisk ? '[disk  ]' : '[ram   ]'
    if (msg.includes('Not Modified')) {
      // New format: 304 2ms Not Modified -> path
      // Legacy format: 304 Not Modified -> path
      const mNew = msg.match(/304\s+(\d+)ms\s+Not Modified\s+->\s+(.+)/)
      const mLeg = !mNew ? msg.match(/304\s+Not Modified\s+->\s+(.+)/) : null
      const ms = mNew ? parseInt(mNew[1], 10) : 0
      const path = (mNew ? mNew[2] : mLeg ? mLeg[1] : msg).trim()
      const subTag = isDisk ? '[hit304]' : '[ram304]'
      return {
        raw: line,
        time: timeStr,
        levelTag: tag,
        method: 'GET ',
        statusCode: '304',
        latencyMs: ms,
        latencyStr: `${ms.toString().padStart(4, ' ')}ms`,
        stateTag: subTag,
        hostTag: compactDomain ? '[asset]' : undefined,
        urlPath: path,
        isError,
        isSystem: false,
      }
    } else if (msg.includes('HIT')) {
      // New format: HIT 2ms -> path (bytes B)
      // Legacy format: HIT -> path (bytes B)
      const pMatch = msg.match(/HIT\s+(\d+)ms\s+->\s+([^\s(]+)(.*)/)
      const pLeg = !pMatch ? msg.match(/HIT\s+->\s+([^\s(]+)(.*)/) : null
      if (pMatch) {
        const ms = parseInt(pMatch[1], 10)
        const path = pMatch[2]
        const extra = pMatch[3] || ''
        return {
          raw: line,
          time: timeStr,
          levelTag: tag,
          method: 'GET ',
          statusCode: '200',
          latencyMs: ms,
          latencyStr: `${ms.toString().padStart(4, ' ')}ms`,
          stateTag: tagLabel,
          hostTag: compactDomain ? '[asset]' : undefined,
          urlPath: path,
          urlExtra: extra,
          isError,
          isSystem: false,
        }
      } else if (pLeg) {
        const path = pLeg[1]
        const extra = pLeg[2] || ''
        return {
          raw: line,
          time: timeStr,
          levelTag: tag,
          method: 'GET ',
          statusCode: '200',
          latencyMs: 0,
          latencyStr: '   0ms',
          stateTag: tagLabel,
          hostTag: compactDomain ? '[asset]' : undefined,
          urlPath: path,
          urlExtra: extra,
          isError,
          isSystem: false,
        }
      }
    }
  }

  // 4. CONNECT
  if (tag.includes('CONNECT')) {
    // e.g.: [127.0.0.1] game.granbluefantasy.jp:443 -> MITM
    const m = msg.match(/(?:\[([^\]]+)\]\s+)?([^\s]+)\s+->\s+(\w+)/)
    if (m) {
      const target = m[2]
      const action = m[3]
      const proto = action.toUpperCase() === 'MITM' ? 'TLS ' : 'TCP '
      const actionTag = `[${action.toLowerCase().padEnd(6, ' ')}]`
      let hostTag: string | undefined
      if (compactDomain) {
        if (target.includes('akamaized.net')) hostTag = '[asset]'
        else if (target.includes('ws.')) hostTag = '[ws   ]'
        else if (target.includes('granbluefantasy.jp')) hostTag = '[gbf  ]'
        else hostTag = '[other]'
      }
      return {
        raw: line,
        time: timeStr,
        levelTag: tag,
        method: proto,
        statusCode: '---',
        latencyStr: '   -ms',
        stateTag: actionTag,
        hostTag,
        urlPath: target,
        isError,
        isSystem: false,
      }
    }
  }

  // 5. BYPASS-TCP
  if (tag.includes('BYPASS-TCP')) {
    const target = msg.replace('Tunneling ', '').replace(' via upstream', '').trim()
    return {
      raw: line,
      time: timeStr,
      levelTag: tag,
      method: 'TCP ',
      statusCode: '---',
      latencyStr: '   -ms',
      stateTag: '[tunnel]',
      hostTag: compactDomain ? '[ws   ]' : undefined,
      urlPath: target,
      isError,
      isSystem: false,
    }
  }

  // 6. PREFETCH
  if (tag.includes('PREFETCH')) {
    // New format: P1 wait=0ms fetch=236ms -> host/path (N B)
    const mNew = msg.match(/P(\d+)\s+wait=(\d+)ms\s+fetch=(\d+)ms\s+->\s+([^\s(]+)(.*)/)
    if (mNew) {
      const prio = mNew[1]
      const waitMs = parseInt(mNew[2], 10)
      const fetchMs = parseInt(mNew[3], 10)
      const fullUrl = mNew[4]
      const extra = mNew[5] || ''
      const waitInfo = waitMs > 0 ? ` wait=${waitMs}ms` : ''
      return {
        raw: line,
        time: timeStr,
        levelTag: tag,
        method: 'GET ',
        statusCode: '200',
        latencyMs: fetchMs,
        latencyStr: `${fetchMs.toString().padStart(4, ' ')}ms`,
        stateTag: `[pf-p${prio.padEnd(2, ' ')}]`,
        hostTag: compactDomain ? '[asset]' : undefined,
        urlPath: fullUrl,
        urlExtra: extra + waitInfo,
        isError,
        isSystem: false,
      }
    }
    // Legacy format: Warmed (P1) -> host/path (N B)
    const mLeg = msg.match(/Warmed \(P(\d+)\)\s+->\s+([^\s(]+)(.*)/)
    if (mLeg) {
      const prio = mLeg[1]
      const fullUrl = mLeg[2]
      const extra = mLeg[3] || ''
      return {
        raw: line,
        time: timeStr,
        levelTag: tag,
        method: 'GET ',
        statusCode: '200',
        latencyStr: '   -ms',
        stateTag: `[pf-p${prio.padEnd(2, ' ')}]`,
        hostTag: compactDomain ? '[asset]' : undefined,
        urlPath: fullUrl,
        urlExtra: extra,
        isError,
        isSystem: false,
      }
    }
  }

  // Fallback for system lines (READY, WARN, RAM-WARM, etc.)
  return {
    raw: line,
    time: timeStr,
    levelTag: tag,
    isError,
    isSystem: true,
    systemMsg: msg,
  }
}

export const LiveLogsWindow: React.FC<LiveLogsWindowProps> = ({
  isOpen,
  onClose,
  logs,
  onClearLogs,
  telemetry,
  hitRatioPercent = 100.0,
  isStandalone = false,
}) => {
  // --- UI Controls State ---
  const [filterText, setFilterText] = useState<string>('')
  const [filterCat, setFilterCat] = useState<string>('全部 (All)')
  const [hideConnect, setHideConnect] = useState<boolean>(false)
  const [compactDomain, setCompactDomain] = useState<boolean>(false)
  const [muteAssets, setMuteAssets] = useState<boolean>(false)
  const [hideMocks, setHideMocks] = useState<boolean>(false)
  const [maskPrivacy, setMaskPrivacy] = useState<boolean>(true)
  const [alignFormat, setAlignFormat] = useState<boolean>(true)
  const [autoScroll, setAutoScroll] = useState<boolean>(true)
  const [isPaused, setIsPaused] = useState<boolean>(false)
  const isMaximized = false
  const [isMinimized, setIsMinimized] = useState<boolean>(false)
  const [fontSize, setFontSize] = useState<number>(12)

  // Right-click context menu state
  const [contextMenu, setContextMenu] = useState<{
    x: number
    y: number
    item: ParsedLogLine | null
  } | null>(null)

  // Internal paused buffer
  const [frozenLogs, setFrozenLogs] = useState<LogItem[]>([])
  const terminalRef = useRef<HTMLDivElement>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)

  // Pause / Resume handling
  const togglePause = useCallback(() => {
    setIsPaused((prev) => {
      const next = !prev
      if (next) {
        setFrozenLogs([...logs])
      }
      return next
    })
  }, [logs])

  const activeLogs = isPaused ? frozenLogs : logs

  // Auto-scroll on new records
  useEffect(() => {
    if (autoScroll && terminalRef.current && !isPaused) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight
    }
  }, [activeLogs, autoScroll, isPaused, isOpen])

  // Global Keyboard shortcuts when window is visible
  useEffect(() => {
    if (!isOpen || isMinimized) return

    const handleKeyDown = (e: KeyboardEvent) => {
      // Space: Toggle pause (unless typing in search input)
      if (e.code === 'Space' && document.activeElement !== searchInputRef.current) {
        e.preventDefault()
        togglePause()
        return
      }

      // Ctrl+F / Cmd+F: Focus Search
      if ((e.ctrlKey || e.metaKey) && (e.key === 'f' || e.key === 'F')) {
        e.preventDefault()
        searchInputRef.current?.focus()
        searchInputRef.current?.select()
        return
      }

      // Escape: clear filter or close context menu
      if (e.key === 'Escape') {
        if (contextMenu) {
          setContextMenu(null)
          return
        }
        if (filterText) {
          setFilterText('')
          return
        }
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, isMinimized, togglePause, filterText, contextMenu])

  // Mouse wheel zoom font size (Ctrl + Wheel)
  const handleWheel = (e: React.WheelEvent) => {
    if (e.ctrlKey || e.metaKey) {
      e.preventDefault()
      if (e.deltaY < 0) {
        setFontSize((prev) => Math.min(18, prev + 1))
      } else {
        setFontSize((prev) => Math.max(9, prev - 1))
      }
    }
  }

  // Filter matching
  const matchesFilter = useCallback(
    (item: LogItem): boolean => {
      const rawMsg = item.msg || ''
      const lvl = item.level || 'INFO'
      const lineLower = (rawMsg + ' ' + lvl).toLowerCase()

      // 1. Checkboxes
      if (hideConnect && (rawMsg.includes('CONNECT') || rawMsg.includes('BYPASS-TCP') || rawMsg.includes('TLS '))) {
        return false
      }
      if (muteAssets && (rawMsg.includes('FETCH') || rawMsg.includes('PREFETCH') || rawMsg.includes('CACHE'))) {
        return false
      }
      if (hideMocks && (rawMsg.includes('MOCK') || rawMsg.includes('/ob/r') || rawMsg.includes('mbga.jp'))) {
        return false
      }

      // 2. Search Text
      const q = filterText.trim().toLowerCase()
      if (q && !lineLower.includes(q)) {
        return false
      }

      // 3. Category Filter
      if (filterCat === '仅战斗 API (BYPASS-API)' && !rawMsg.includes('BYPASS-API')) {
        return false
      }
      if (filterCat === '仅缓存命中 (CACHE)' && !rawMsg.includes('CACHE')) {
        return false
      }
      if (filterCat === '仅素材拉取 (FETCH/PREFETCH)' && !rawMsg.includes('FETCH') && !rawMsg.includes('PREFETCH')) {
        return false
      }
      if (
        filterCat === '仅重连与告警 (RETRY/ERROR)' &&
        !rawMsg.includes('RETRY') &&
        !rawMsg.includes('ERR') &&
        !lvl.includes('ERR') &&
        !lvl.includes('WARN')
      ) {
        return false
      }
      if (filterCat === '仅慢请求 (>300ms)') {
        const msMatch = rawMsg.match(/(\d+)\s*ms/)
        if (!msMatch || parseInt(msMatch[1], 10) <= 300) {
          return false
        }
      }

      return true
    },
    [filterText, filterCat, hideConnect, muteAssets, hideMocks]
  )

  // Parsed and filtered records
  const renderedRecords = useMemo(() => {
    const list: ParsedLogLine[] = []
    for (const item of activeLogs) {
      if (!matchesFilter(item)) continue
      list.push(parseLogLine(item.msg, item.time, item.level, alignFormat, compactDomain, maskPrivacy))
    }
    return list
  }, [activeLogs, matchesFilter, alignFormat, compactDomain, maskPrivacy])

  // Copy operations
  const copyAllVisible = () => {
    const text = renderedRecords.map((r) => r.raw).join('\n')
    navigator.clipboard.writeText(text)
  }

  // Export logs to local file
  const exportLogs = () => {
    const text = renderedRecords.map((r) => `[${r.time}] ${r.raw}`).join('\n')
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
    a.href = url
    a.download = `GBF_Proxy_LiveLogs_${ts}.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  // Right-click handling
  const handleContextMenu = (e: React.MouseEvent, item: ParsedLogLine) => {
    e.preventDefault()
    setContextMenu({
      x: Math.min(window.innerWidth - 240, e.clientX),
      y: Math.min(window.innerHeight - 260, e.clientY),
      item,
    })
  }

  // Close context menu on global click
  useEffect(() => {
    const handleGlobalClick = () => setContextMenu(null)
    window.addEventListener('click', handleGlobalClick)
    return () => window.removeEventListener('click', handleGlobalClick)
  }, [])

  if (!isOpen) return null

  // If minimized: show a sleek floating taskbar badge in bottom right corner
  if (isMinimized) {
    return (
      <div className="fixed bottom-4 right-4 z-50 flex items-center gap-3 bg-[#1e2430] border border-slate-700 text-white px-4 py-2.5 rounded-xl shadow-2xl backdrop-blur-md animate-fade-in font-mono text-xs">
        <span className="flex h-2 w-2 relative">
          <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
          <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
        </span>
        <span className="font-semibold text-slate-200">实时网络与转发日志 (已最小化)</span>
        <span className="text-slate-400">({renderedRecords.length} 行)</span>
        <button
          onClick={() => setIsMinimized(false)}
          className="ml-2 px-2.5 py-1 bg-blue-600 hover:bg-blue-500 text-white rounded text-xs transition-colors"
        >
          还原窗口
        </button>
        <button
          onClick={onClose}
          className="p-1 hover:bg-slate-700 text-slate-400 hover:text-white rounded transition-colors"
        >
          <X className="w-3.5 h-3.5" />
        </button>
      </div>
    )
  }

  // --- Render Individual Token Styling ---
  const renderLogContent = (item: ParsedLogLine) => {
    if (item.isSystem || !alignFormat) {
      return (
        <span className={item.isError ? 'text-red-400 font-bold' : 'text-slate-200'}>
          {item.systemMsg || item.raw}
        </span>
      )
    }

    // Method color
    let methodColor = 'text-[#569cd6]' // default GET blue
    if (item.method === 'POST') methodColor = 'text-[#bd93f9] font-bold'
    else if (item.method?.trim() === 'TLS' || item.method?.trim() === 'TCP') methodColor = 'text-[#8be9fd]'

    // Status code color
    let codeColor = 'text-[#89d185]' // 200 green
    if (item.statusCode === '304') codeColor = 'text-[#8be9fd]' // cyan
    else if (item.statusCode?.startsWith('5') || item.statusCode?.startsWith('4')) {
      codeColor = 'text-[#f14c4c] font-bold'
    } else if (item.statusCode === '---') {
      codeColor = 'text-slate-500'
    }

    // Latency threshold coloring: user requested >300ms red background highlight!
    let latencyClass = 'text-[#cccccc]'
    if (item.latencyMs !== undefined) {
      if (item.latencyMs < 100) {
        latencyClass = 'text-[#50fa7b]' // fast green (<100ms)
      } else if (item.latencyMs <= 300) {
        latencyClass = 'text-[#cccccc]' // normal grey (100~300ms)
      } else {
        // >300ms: bold red text with dark red background highlight!
        latencyClass = 'text-[#ff6b6b] bg-[#401818] font-bold rounded-xs'
      }
    }

    // Connection state tag color
    let stateClass = 'text-[#e5c07b]' // new yellow
    if (item.stateTag?.includes('reused')) {
      stateClass = 'text-[#50fa7b] font-bold'
    } else if (item.stateTag?.includes('304')) {
      stateClass = 'text-[#8be9fd]'
    } else if (item.stateTag?.includes('disk') || item.stateTag?.includes('ram')) {
      stateClass = 'text-[#89d185]'
    } else if (item.stateTag?.includes('stream')) {
      stateClass = 'text-[#569cd6]'
    } else if (item.stateTag?.includes('mitm') || item.stateTag?.includes('tunnel')) {
      stateClass = 'text-[#9cdcfe]'
    }

    return (
      <div className="flex items-center whitespace-pre font-mono">
        {/* Method (exact 5 chars: 4 chars + 1 space) */}
        {item.method && (
          <span className={methodColor}>
            {(item.method || '').trim().padEnd(4, ' ') + ' '}
          </span>
        )}

        {/* Status Code (exact 5 chars: 3 chars + 2 spaces) */}
        {item.statusCode && (
          <span className={codeColor}>
            {(item.statusCode || '').trim().padEnd(3, ' ') + '  '}
          </span>
        )}

        {/* Latency (exact 7 chars: 6 right-aligned chars + 1 space) */}
        {item.latencyStr && (
          <span>
            <span className={latencyClass}>
              {(item.latencyStr || '').trim().padStart(6, ' ')}
            </span>
            <span> </span>
          </span>
        )}

        {/* Connection/Reuse State (exact 9 chars: 8 chars + 1 space) */}
        {item.stateTag && (
          <span className={stateClass}>
            {(item.stateTag || '').padEnd(8, ' ') + ' '}
          </span>
        )}

        {/* Simplified Host Tag if enabled (exact 7 chars: 6 chars + 1 space) */}
        {item.hostTag && (
          <span className="text-[#79c0ff]">
            {item.hostTag.padEnd(6, ' ') + ' '}
          </span>
        )}

        {/* URL Path (white) + Query params (bright readable silver) - never truncated */}
        <span>
          <span className="text-[#f0f6fc]">{item.urlPath}</span>
          {item.urlQuery && <span className="text-[#c9d1d9]">{item.urlQuery}</span>}
          {item.urlExtra && <span className="text-[#8fa1b3] text-[11px] ml-1.5">{item.urlExtra}</span>}
        </span>
      </div>
    )
  }

  useEffect(() => {
    if (isStandalone) {
      document.title = '实时网络与转发日志 (Live Logs) - GBF 加速器'
    }
  }, [isStandalone])

  const handleClose = () => {
    if (isStandalone) {
      window.close()
    }
    onClose()
  }

  const windowInnerContent = (
    <div
      className={`bg-[#1e1e1e] flex flex-col overflow-hidden transition-all duration-150 ${
        isStandalone
          ? 'w-screen h-screen'
          : isMaximized
          ? 'w-full h-full rounded-none'
          : 'w-full max-w-[1280px] h-[90vh] max-h-[840px] min-h-[520px] rounded-xl border border-slate-700/80 shadow-2xl'
      }`}
    >
        {/* ========================================================================= */}
        {/* 1. Window Title Bar (Only in modal/drawer mode; standalone uses OS title) */}
        {/* ========================================================================= */}
        {!isStandalone && (
          <div className="h-9 bg-[#2d2d2d] border-b border-black/40 px-3 flex items-center justify-between shrink-0 select-none">
            {/* Left: Window Icon + Title */}
            <div className="flex items-center gap-2 text-xs font-medium text-slate-200">
              <span className="flex h-2 w-2 relative">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
              </span>
              <span className="font-semibold text-slate-100">
                实时网络与转发日志 (Live Logs) - GBF 加速器
              </span>
            </div>

            {/* Right: Window Controls (_, □, ✕) */}
            <div className="flex items-center">
              {/* Pop out to independent window */}
              <button
                onClick={() => {
                  onClose()
                  const w = 1220
                  const h = 740
                  const left = Math.max(0, Math.floor((window.screen.width - w) / 2))
                  const top = Math.max(0, Math.floor((window.screen.height - h) / 2))
                  const features = `width=${w},height=${h},left=${left},top=${top},menubar=no,toolbar=no,location=no,status=no,resizable=yes,scrollbars=yes`
                  window.open('/logs', 'GBFLiveLogsWindow', features)
                }}
                className="h-9 px-2 hover:bg-white/10 text-slate-300 hover:text-white flex items-center justify-center transition-colors text-xs gap-1"
                title="独立窗口打开"
              >
                <span>↗</span>
                <span className="text-[11px] hidden sm:inline">独立窗口</span>
              </button>

              {/* Close */}
              <button
                onClick={handleClose}
                className="h-9 px-3 hover:bg-red-600 text-slate-300 hover:text-white flex items-center justify-center transition-colors"
                title="关闭"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
          </div>
        )}

        {/* ==================================================== */}
        {/* 2. Top Toolbar (1:1 Strictly Aligned with main Fig 1) */}
        {/* ==================================================== */}
        <div className="bg-[#252526] border-b border-black/40 px-3 py-2 flex flex-wrap items-center justify-between gap-y-2 text-xs text-slate-300 shrink-0 font-sans">
          {/* Left Controls: Search, Category, and Checkboxes */}
          <div className="flex flex-wrap items-center gap-3">
            {/* Search Input */}
            <div className="flex items-center gap-1.5">
              <span className="text-slate-400">搜索:</span>
              <div className="relative">
                <input
                  ref={searchInputRef}
                  type="text"
                  value={filterText}
                  onChange={(e) => setFilterText(e.target.value)}
                  placeholder="Ctrl+F 搜索..."
                  className="bg-[#181818] border border-slate-700/80 rounded px-2 py-0.5 text-xs text-white placeholder-slate-500 focus:outline-none focus:border-blue-500 w-32 sm:w-40 font-mono transition-all"
                />
                {filterText && (
                  <button
                    onClick={() => setFilterText('')}
                    className="absolute right-1 top-1 text-slate-400 hover:text-white"
                  >
                    <X className="w-3 h-3" />
                  </button>
                )}
              </div>
            </div>

            {/* Category Dropdown */}
            <div className="flex items-center gap-1.5">
              <span className="text-slate-400">类型:</span>
              <select
                value={filterCat}
                onChange={(e) => setFilterCat(e.target.value)}
                className="bg-[#181818] border border-slate-700/80 rounded px-2 py-0.5 text-xs text-slate-200 focus:outline-none focus:border-blue-500 cursor-pointer"
              >
                <option value="全部 (All)">全部 (All)</option>
                <option value="仅战斗 API (BYPASS-API)">仅战斗 API (BYPASS-API)</option>
                <option value="仅缓存命中 (CACHE)">仅缓存命中 (CACHE)</option>
                <option value="仅素材拉取 (FETCH/PREFETCH)">仅素材拉取 (FETCH/PREFETCH)</option>
                <option value="仅慢请求 (>300ms)">仅慢请求 (&gt;300ms)</option>
                <option value="仅重连与告警 (RETRY/ERROR)">仅重连与告警 (RETRY/ERROR)</option>
              </select>
            </div>

            {/* 7 Checkboxes */}
            <div className="flex flex-wrap items-center gap-2.5 text-slate-300">
              <label className="flex items-center gap-1 cursor-pointer hover:text-white">
                <input
                  type="checkbox"
                  checked={hideConnect}
                  onChange={(e) => setHideConnect(e.target.checked)}
                  className="rounded bg-black/40 border-slate-700 text-blue-500 focus:ring-0 cursor-pointer"
                />
                <span>隐藏握手</span>
              </label>

              <label className="flex items-center gap-1 cursor-pointer hover:text-white">
                <input
                  type="checkbox"
                  checked={compactDomain}
                  onChange={(e) => setCompactDomain(e.target.checked)}
                  className="rounded bg-black/40 border-slate-700 text-blue-500 focus:ring-0 cursor-pointer"
                />
                <span>简化域名</span>
              </label>

              <label className="flex items-center gap-1 cursor-pointer hover:text-white">
                <input
                  type="checkbox"
                  checked={muteAssets}
                  onChange={(e) => setMuteAssets(e.target.checked)}
                  className="rounded bg-black/40 border-slate-700 text-blue-500 focus:ring-0 cursor-pointer"
                />
                <span>隐藏静态</span>
              </label>

              <label className="flex items-center gap-1 cursor-pointer hover:text-white">
                <input
                  type="checkbox"
                  checked={hideMocks}
                  onChange={(e) => setHideMocks(e.target.checked)}
                  className="rounded bg-black/40 border-slate-700 text-blue-500 focus:ring-0 cursor-pointer"
                />
                <span>隐藏打点</span>
              </label>

              <label className="flex items-center gap-1 cursor-pointer hover:text-white">
                <input
                  type="checkbox"
                  checked={maskPrivacy}
                  onChange={(e) => setMaskPrivacy(e.target.checked)}
                  className="rounded bg-black/40 border-slate-700 text-blue-500 focus:ring-0 cursor-pointer"
                />
                <span>脱敏隐私</span>
              </label>

              <label className="flex items-center gap-1 cursor-pointer hover:text-white">
                <input
                  type="checkbox"
                  checked={alignFormat}
                  onChange={(e) => setAlignFormat(e.target.checked)}
                  className="rounded bg-black/40 border-slate-700 text-blue-500 focus:ring-0 cursor-pointer"
                />
                <span>对齐排版</span>
              </label>

              <label className="flex items-center gap-1 cursor-pointer hover:text-white">
                <input
                  type="checkbox"
                  checked={autoScroll}
                  onChange={(e) => setAutoScroll(e.target.checked)}
                  className="rounded bg-black/40 border-slate-700 text-blue-500 focus:ring-0 cursor-pointer"
                />
                <span>自动滚屏</span>
              </label>
            </div>
          </div>

          {/* Right Action Buttons */}
          <div className="flex items-center gap-1.5 ml-auto">
            {/* Pause / Resume */}
            <button
              onClick={togglePause}
              className={`px-2.5 py-1 rounded text-xs font-medium border flex items-center gap-1 transition-colors ${
                isPaused
                  ? 'bg-amber-900/40 border-amber-700 text-amber-300 hover:bg-amber-900/60'
                  : 'bg-[#333333] hover:bg-[#3e3e3e] border-slate-700 text-slate-200'
              }`}
              title="空格键亦可切换"
            >
              {isPaused ? <Play className="w-3 h-3" /> : <Pause className="w-3 h-3" />}
              <span>{isPaused ? '继续' : '暂停'}</span>
            </button>

            {/* Clear Display */}
            <button
              onClick={onClearLogs}
              className="px-2.5 py-1 rounded text-xs font-medium bg-[#333333] hover:bg-[#3e3e3e] border border-slate-700 text-slate-200 hover:text-white transition-colors"
            >
              清除显示
            </button>

            {/* Copy All */}
            <button
              onClick={copyAllVisible}
              className="px-2.5 py-1 rounded text-xs font-medium bg-[#333333] hover:bg-[#3e3e3e] border border-slate-700 text-slate-200 hover:text-white transition-colors"
            >
              复制全部
            </button>

            {/* Export Logs */}
            <button
              onClick={exportLogs}
              className="px-2.5 py-1 rounded text-xs font-medium bg-[#333333] hover:bg-[#3e3e3e] border border-slate-700 text-slate-200 hover:text-white transition-colors"
            >
              导出日志
            </button>
          </div>
        </div>

        {/* ======================================================= */}
        {/* 3. Central Monospace Log Viewport (Dark Terminal Theme) */}
        {/* ======================================================= */}
        <div
          ref={terminalRef}
          onWheel={handleWheel}
          className="flex-1 bg-[#181818] overflow-x-auto overflow-y-auto p-3 font-mono leading-relaxed select-text space-y-0.5 text-xs text-[#cccccc]"
          style={{ fontSize: `${fontSize}px` }}
        >
          {renderedRecords.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center text-slate-600 gap-2 select-none">
              <Terminal className="w-10 h-10 opacity-20" />
              <span>暂无符合过滤条件的网络或转发日志</span>
            </div>
          ) : (
            renderedRecords.map((item, idx) => (
              <div
                key={idx}
                onContextMenu={(e) => handleContextMenu(e, item)}
                className={`flex items-center px-1.5 py-0.5 rounded transition-colors hover:bg-white/[0.06] whitespace-pre font-mono min-w-max ${
                  item.isError ? 'bg-[#381a1a]/60 text-red-200' : ''
                }`}
              >
                {/* Timestamp (exact 11 chars: [HH:MM:SS] + 1 space) */}
                <span className="text-[#6e7681] shrink-0 select-none whitespace-pre">
                  [{item.time}]{' '}
                </span>

                {/* Level Tag (exact 13 chars: [TAG       ] + 1 space) */}
                <span
                  className={`shrink-0 font-bold whitespace-pre ${
                    item.levelTag.includes('API')
                      ? 'text-[#4ec9b0]'
                      : item.levelTag.includes('CACHE')
                      ? 'text-[#89d185]'
                      : item.levelTag.includes('FETCH')
                      ? 'text-[#569cd6]'
                      : item.levelTag.includes('PREFETCH')
                      ? 'text-[#c586c0]'
                      : item.levelTag.includes('RETRY') || item.levelTag.includes('WARN')
                      ? 'text-[#e5c07b]'
                      : item.levelTag.includes('ERR') || item.levelTag.includes('BLOCK')
                      ? 'text-[#f14c4c]'
                      : 'text-[#9cdcfe]'
                  }`}
                >
                  {`[${item.levelTag.padEnd(10, ' ')}] `}
                </span>

                {/* Structured Columns */}
                {renderLogContent(item)}
              </div>
            ))
          )}
        </div>

        {/* ====================================================== */}
        {/* 4. Bottom Status Bar (Real-time Telemetry Metrics Bar) */}
        {/* ====================================================== */}
        <div className="h-7 bg-[#1e2430] border-t border-black/40 px-3 flex items-center justify-between text-[11px] font-mono text-slate-400 shrink-0 select-none">
          {/* Left: Metrics & Counter */}
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1.5 text-slate-300">
              <span className="text-amber-400">⚡</span>
              <span>
                API P50:{' '}
                <strong className="text-emerald-400 font-semibold">
                  {telemetry?.percentiles?.p50_ms ?? 0}ms
                </strong>{' '}
                (P95: {telemetry?.percentiles?.p95_ms ?? 0}ms)
              </span>
            </span>

            <span className="text-slate-600">|</span>

            <span>
              长连接复用:{' '}
              <strong className="text-sky-400 font-semibold">
                {telemetry?.reuse_rate_percent ?? 0}%
              </strong>
            </span>

            <span className="text-slate-600">|</span>

            <span>
              缓存命中:{' '}
              <strong className="text-emerald-400 font-semibold">
                {hitRatioPercent.toFixed(1)}%
              </strong>
            </span>

            <span className="text-slate-600">|</span>

            <span>
              当前显示: {renderedRecords.length} 行 / 历史: {activeLogs.length} 条
            </span>

            <span className="text-slate-600">|</span>

            <span className="flex items-center gap-1.5">
              <span
                className={`h-1.5 w-1.5 rounded-full ${
                  isPaused ? 'bg-amber-400' : 'bg-emerald-400 animate-pulse'
                }`}
              />
              <span className={isPaused ? 'text-amber-400' : 'text-emerald-400'}>
                {isPaused ? '已暂停刷新' : '实时监听中'}
              </span>
            </span>
          </div>

          {/* Right: Shortcut hints (1:1 aligned with Image 1) */}
          <div className="text-slate-500 hidden md:flex items-center gap-2">
            <span>Ctrl+F 搜索</span>
            <span>|</span>
            <span>空格 暂停</span>
            <span>|</span>
            <span>Ctrl+滚轮 缩放</span>
          </div>
        </div>
      </div>
  )

  const renderContextMenu = () => {
    if (!contextMenu) return null
    return (
      <div
        style={{ top: `${contextMenu.y}px`, left: `${contextMenu.x}px` }}
        className="fixed z-50 bg-[#252526] border border-slate-700/80 rounded-lg shadow-2xl py-1 text-xs text-slate-200 font-sans w-56 select-none animate-fade-in"
        onClick={(e) => e.stopPropagation()}
      >
        <button
          onClick={() => {
            if (contextMenu.item) navigator.clipboard.writeText(contextMenu.item.raw)
            setContextMenu(null)
          }}
          className="w-full px-3 py-1.5 text-left hover:bg-blue-600 hover:text-white flex items-center gap-2 transition-colors"
        >
          <span>📋</span> 复制当前显示行
        </button>

        <button
          onClick={() => {
            if (contextMenu.item) navigator.clipboard.writeText(contextMenu.item.raw)
            setContextMenu(null)
          }}
          className="w-full px-3 py-1.5 text-left hover:bg-blue-600 hover:text-white flex items-center gap-2 transition-colors"
        >
          <span>📜</span> 复制原始完整记录 (含参数)
        </button>

        <button
          onClick={() => {
            if (contextMenu.item?.urlPath) {
              const full = contextMenu.item.urlPath + (contextMenu.item.urlQuery || '')
              navigator.clipboard.writeText(full)
            }
            setContextMenu(null)
          }}
          className="w-full px-3 py-1.5 text-left hover:bg-blue-600 hover:text-white flex items-center gap-2 transition-colors"
        >
          <span>🔗</span> 复制完整 URL
        </button>

        <button
          onClick={() => {
            if (contextMenu.item?.urlPath) {
              setFilterText(contextMenu.item.urlPath)
            }
            setContextMenu(null)
          }}
          className="w-full px-3 py-1.5 text-left hover:bg-blue-600 hover:text-white flex items-center gap-2 transition-colors"
        >
          <span>🔍</span> 仅筛选此路径
        </button>

        <div className="h-px bg-slate-700 my-1" />

        <button
          onClick={() => {
            exportLogs()
            setContextMenu(null)
          }}
          className="w-full px-3 py-1.5 text-left hover:bg-blue-600 hover:text-white flex items-center gap-2 transition-colors"
        >
          <span>💾</span> 导出全量排查诊断日志...
        </button>

        <button
          onClick={() => {
            onClearLogs()
            setContextMenu(null)
          }}
          className="w-full px-3 py-1.5 text-left hover:bg-red-600 hover:text-white flex items-center gap-2 text-red-400 transition-colors"
        >
          <span>🧹</span> 清除当前显示
        </button>
      </div>
    )
  }

  if (isStandalone) {
    return (
      <div className="w-screen h-screen flex flex-col bg-[#1e1e1e] overflow-hidden select-none">
        {windowInnerContent}
        {renderContextMenu()}
      </div>
    )
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/65 backdrop-blur-xs p-2 sm:p-4 select-none animate-fade-in">
      {windowInnerContent}
      {renderContextMenu()}
    </div>
  )
}
