import React, { useState, useRef, useEffect } from 'react'
import { LogItem } from '../../types'
import { Drawer } from '../common/Drawer'
import { Terminal, Search, Trash2, ArrowDown } from 'lucide-react'

export interface LogTerminalDrawerProps {
  isOpen: boolean
  onClose: () => void
  logs: LogItem[]
  onClearLogs: () => void
}

export const LogTerminalDrawer: React.FC<LogTerminalDrawerProps> = ({
  isOpen,
  onClose,
  logs,
  onClearLogs,
}) => {
  const [filterLevel, setFilterLevel] = useState<string>('ALL')
  const [keyword, setKeyword] = useState<string>('')
  const [autoScroll, setAutoScroll] = useState<boolean>(true)
  const terminalRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (autoScroll && terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight
    }
  }, [logs, autoScroll, isOpen])

  const filteredLogs = logs.filter((item) => {
    if (filterLevel !== 'ALL') {
      if (filterLevel === 'ERROR' && !item.level.includes('ERR') && !item.msg.includes('Error')) return false
      if (filterLevel === 'API' && !item.msg.includes('/rest/') && !item.msg.includes('API')) return false
      if (filterLevel === 'CACHE' && !item.msg.includes('CACHE') && !item.msg.includes('HIT') && !item.msg.includes('MISS')) return false
      if (filterLevel === 'PREFETCH' && !item.msg.includes('PREFETCH')) return false
    }
    if (keyword) {
      return item.msg.toLowerCase().includes(keyword.toLowerCase())
    }
    return true
  })

  const getLevelBadgeClass = (level: string, msg: string) => {
    if (level.includes('ERR') || msg.includes('Error') || msg.includes('504')) {
      return 'bg-apple-red/20 text-apple-red'
    }
    if (msg.includes('CACHE-RAM') || msg.includes('RAM')) {
      return 'bg-apple-green/20 text-apple-green'
    }
    if (msg.includes('CACHE-DISK') || msg.includes('DISK')) {
      return 'bg-apple-blue/20 text-apple-blue'
    }
    if (msg.includes('PREFETCH')) {
      return 'bg-purple-500/20 text-purple-400'
    }
    if (msg.includes('BYPASS') || msg.includes('REST') || msg.includes('/rest/')) {
      return 'bg-apple-amber/20 text-apple-amber'
    }
    return 'bg-white/[0.08] text-label-secondary'
  }

  return (
    <Drawer
      isOpen={isOpen}
      onClose={onClose}
      title={
        <>
          <Terminal className="w-4 h-4 text-apple-blue" />
          <span>实时请求与内核日志</span>
          <span className="text-xs font-mono text-label-secondary">
            ({filteredLogs.length} 条)
          </span>
        </>
      }
      subtitle="透明转发流量流向、缓存命中状态与避让遥测 (按 L 键开关)"
      headerRight={
        <div className="flex items-center gap-2">
          {/* Apple Segmented Filter */}
          <div className="flex items-center p-0.5 rounded-xl bg-black/40 border border-white/[0.06] text-xs">
            {['ALL', 'API', 'CACHE', 'PREFETCH', 'ERROR'].map((lvl) => (
              <button
                key={lvl}
                onClick={() => setFilterLevel(lvl)}
                className={`px-2.5 py-1 rounded-lg font-medium text-[11px] transition-all ${
                  filterLevel === lvl
                    ? 'bg-white/[0.16] text-white font-semibold shadow-sm'
                    : 'text-label-secondary hover:text-white'
                }`}
              >
                {lvl}
              </button>
            ))}
          </div>

          {/* Search Input */}
          <div className="relative hidden sm:block">
            <Search className="w-3.5 h-3.5 absolute left-2.5 top-2 text-label-secondary pointer-events-none" />
            <input
              type="text"
              placeholder="过滤 URL / 关键字..."
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              className="bg-black/30 border border-white/[0.08] rounded-xl pl-8 pr-3 py-1 text-xs text-white placeholder-label-tertiary focus:outline-none focus:border-apple-red/50 w-44 transition-all"
            />
          </div>

          {/* Auto Scroll Toggle */}
          <button
            onClick={() => setAutoScroll(!autoScroll)}
            className={`p-1.5 rounded-full border text-xs transition-colors ${
              autoScroll
                ? 'bg-apple-blue/15 border-apple-blue/30 text-apple-blue'
                : 'bg-white/[0.06] border-white/[0.08] text-label-secondary hover:text-white'
            }`}
            title={autoScroll ? '已开启自动滚底' : '已暂停自动滚底'}
          >
            <ArrowDown className="w-3.5 h-3.5" />
          </button>

          {/* Clear Logs */}
          <button
            onClick={onClearLogs}
            className="p-1.5 rounded-full bg-white/[0.06] border border-white/[0.08] text-label-secondary hover:text-apple-red hover:bg-apple-red/10 transition-colors"
            title="清空终端日志"
          >
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        </div>
      }
    >
      {/* Mobile search bar if on small screen */}
      <div className="sm:hidden mb-3">
        <input
          type="text"
          placeholder="过滤 URL / 关键字..."
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          className="w-full bg-black/30 border border-white/[0.08] rounded-xl px-3 py-1.5 text-xs text-white placeholder-label-tertiary focus:outline-none"
        />
      </div>

      {/* Terminal Viewport */}
      <div
        ref={terminalRef}
        className="flex-1 bg-black/80 border border-white/[0.08] rounded-2xl p-4 font-mono text-xs overflow-y-auto space-y-1.5 shadow-inner"
      >
        {filteredLogs.length === 0 ? (
          <div className="text-label-tertiary text-center py-28 flex flex-col items-center justify-center gap-2 select-none">
            <Terminal className="w-8 h-8 opacity-20" />
            <span>暂无符合过滤条件的内核日志输出</span>
          </div>
        ) : (
          filteredLogs.map((item, idx) => (
            <div
              key={idx}
              className="flex items-start gap-2.5 hover:bg-white/[0.04] px-2 py-0.5 rounded-lg transition-colors text-[12px] leading-relaxed"
            >
              <span className="text-label-tertiary shrink-0 select-none font-mono text-[11px]">
                [{item.time}]
              </span>
              <span
                className={`text-[10px] px-2 py-0.5 rounded-full font-semibold shrink-0 uppercase tracking-tight ${getLevelBadgeClass(
                  item.level,
                  item.msg
                )}`}
              >
                {item.level || 'INFO'}
              </span>
              <span className="text-white break-all font-mono">
                {item.msg}
              </span>
            </div>
          ))
        )}
      </div>
    </Drawer>
  )
}
