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
      return 'bg-sys-redBg text-sys-red border border-sys-red/20'
    }
    if (msg.includes('CACHE-RAM') || msg.includes('RAM')) {
      return 'bg-sys-greenBg text-sys-green border border-sys-green/20'
    }
    if (msg.includes('CACHE-DISK') || msg.includes('DISK')) {
      return 'bg-sys-blueBg text-sys-blue border border-sys-blue/20'
    }
    if (msg.includes('PREFETCH')) {
      return 'bg-indigo-950/80 text-indigo-400 border border-indigo-800/40'
    }
    if (msg.includes('BYPASS') || msg.includes('REST') || msg.includes('/rest/')) {
      return 'bg-sys-amberBg text-sys-amber border border-sys-amber/20'
    }
    return 'bg-surface-active text-label-secondary border border-hairline'
  }

  return (
    <Drawer
      isOpen={isOpen}
      onClose={onClose}
      title={
        <>
          <Terminal className="w-4 h-4 text-sys-blue" />
          <span>实时请求与内核日志终端</span>
          <span className="text-xs font-mono text-label-tertiary">
            ({filteredLogs.length} 条)
          </span>
        </>
      }
      subtitle="透明转发流量流向、缓存命中状态与避让日志 (按 L 开关)"
      headerRight={
        <div className="flex items-center gap-2">
          {/* Level Filter */}
          <div className="flex items-center gap-1 bg-surface-subtle p-0.5 rounded-lg border border-hairline text-xs">
            {['ALL', 'API', 'CACHE', 'PREFETCH', 'ERROR'].map((lvl) => (
              <button
                key={lvl}
                onClick={() => setFilterLevel(lvl)}
                className={`px-2 py-0.5 rounded-md font-medium text-[11px] transition-colors ${
                  filterLevel === lvl
                    ? 'bg-sys-blue text-white shadow-sm'
                    : 'text-label-tertiary hover:text-label-primary'
                }`}
              >
                {lvl}
              </button>
            ))}
          </div>

          {/* Search Input */}
          <div className="relative hidden sm:block">
            <Search className="w-3.5 h-3.5 absolute left-2.5 top-2 text-label-tertiary" />
            <input
              type="text"
              placeholder="搜索关键字 / URL..."
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              className="bg-surface-subtle border border-hairline rounded-lg pl-8 pr-2.5 py-1 text-xs text-label-primary placeholder-label-tertiary focus:outline-none focus:border-sys-blue/50 w-40"
            />
          </div>

          {/* Auto Scroll Toggle */}
          <button
            onClick={() => setAutoScroll(!autoScroll)}
            className={`p-1 rounded-lg border text-xs transition-colors ${
              autoScroll
                ? 'bg-sys-blueBg border-sys-blue/30 text-sys-blue'
                : 'bg-surface-subtle border-hairline text-label-tertiary'
            }`}
            title="自动滚底"
          >
            <ArrowDown className="w-3.5 h-3.5" />
          </button>

          {/* Clear Logs */}
          <button
            onClick={onClearLogs}
            className="p-1 rounded-lg bg-surface-subtle border border-hairline text-label-tertiary hover:text-sys-red transition-colors"
            title="清空终端"
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
          placeholder="搜索关键字 / URL..."
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          className="w-full bg-surface-subtle border border-hairline rounded-lg px-3 py-1.5 text-xs text-label-primary placeholder-label-tertiary focus:outline-none"
        />
      </div>

      {/* Terminal Viewport */}
      <div
        ref={terminalRef}
        className="flex-1 bg-[#08090C] border border-hairline rounded-xl p-3.5 font-mono text-xs overflow-y-auto space-y-1 shadow-inner"
      >
        {filteredLogs.length === 0 ? (
          <div className="text-label-tertiary text-center py-24 flex flex-col items-center justify-center gap-2">
            <Terminal className="w-8 h-8 opacity-20" />
            <span>暂无符合过滤条件的控制台日志输出</span>
          </div>
        ) : (
          filteredLogs.map((item, idx) => (
            <div
              key={idx}
              className="flex items-start gap-2.5 hover:bg-white/[0.03] px-2 py-0.5 rounded transition-colors text-[12px] leading-relaxed"
            >
              <span className="text-label-tertiary shrink-0 select-none">
                [{item.time}]
              </span>
              <span
                className={`text-[10px] px-1.5 py-0.5 rounded font-semibold shrink-0 uppercase tracking-tight ${getLevelBadgeClass(
                  item.level,
                  item.msg
                )}`}
              >
                {item.level || 'INFO'}
              </span>
              <span className="text-label-primary break-all">
                {item.msg}
              </span>
            </div>
          ))
        )}
      </div>
    </Drawer>
  )
}
