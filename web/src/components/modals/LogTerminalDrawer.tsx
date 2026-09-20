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
      return 'bg-red-900/40 text-red-400 border border-red-800/50'
    }
    if (msg.includes('CACHE-RAM') || msg.includes('RAM')) {
      return 'bg-emerald-900/40 text-emerald-400 border border-emerald-800/50'
    }
    if (msg.includes('CACHE-DISK') || msg.includes('DISK')) {
      return 'bg-blue-900/40 text-blue-400 border border-blue-800/50'
    }
    if (msg.includes('PREFETCH')) {
      return 'bg-purple-900/40 text-purple-400 border border-purple-800/50'
    }
    if (msg.includes('BYPASS') || msg.includes('REST') || msg.includes('/rest/')) {
      return 'bg-amber-900/40 text-amber-400 border border-amber-800/50'
    }
    return 'bg-slate-800 text-slate-400 border border-slate-700'
  }

  return (
    <Drawer
      isOpen={isOpen}
      onClose={onClose}
      title={
        <>
          <Terminal className="w-4 h-4 text-blue-400" />
          <span>实时请求与内核日志</span>
          <span className="text-xs font-mono text-slate-400">
            ({filteredLogs.length} 条)
          </span>
        </>
      }
      subtitle="透明转发流量流向、缓存命中状态与避让遥测 (按 L 键开关)"
      headerRight={
        <div className="flex items-center gap-2">
          {/* Segmented Filter */}
          <div className="flex items-center p-0.5 rounded-lg bg-black/50 border border-slate-700 text-xs">
            {['ALL', 'API', 'CACHE', 'PREFETCH', 'ERROR'].map((lvl) => (
              <button
                key={lvl}
                onClick={() => setFilterLevel(lvl)}
                className={`px-2.5 py-1 rounded text-[11px] font-medium transition-all ${
                  filterLevel === lvl
                    ? 'bg-slate-700 text-white font-semibold shadow-xs'
                    : 'text-slate-400 hover:text-white'
                }`}
              >
                {lvl}
              </button>
            ))}
          </div>

          {/* Search Input */}
          <div className="relative hidden sm:block">
            <Search className="w-3.5 h-3.5 absolute left-2.5 top-2 text-slate-400 pointer-events-none" />
            <input
              type="text"
              placeholder="过滤 URL / 关键字..."
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              className="bg-black/50 border border-slate-700 rounded-lg pl-8 pr-3 py-1 text-xs text-white placeholder-slate-500 focus:outline-none focus:border-blue-500 w-44 transition-all"
            />
          </div>

          {/* Auto Scroll Toggle */}
          <button
            onClick={() => setAutoScroll(!autoScroll)}
            className={`p-1.5 rounded-lg border text-xs transition-colors ${
              autoScroll
                ? 'bg-blue-900/40 border-blue-700 text-blue-400'
                : 'bg-slate-800 border-slate-700 text-slate-400 hover:text-white'
            }`}
            title={autoScroll ? '已开启自动滚底' : '已暂停自动滚底'}
          >
            <ArrowDown className="w-3.5 h-3.5" />
          </button>

          {/* Clear Logs */}
          <button
            onClick={onClearLogs}
            className="p-1.5 rounded-lg bg-slate-800 border border-slate-700 text-slate-400 hover:text-red-400 hover:bg-red-950/30 transition-colors"
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
          className="w-full bg-black/50 border border-slate-700 rounded-lg px-3 py-1.5 text-xs text-white placeholder-slate-500 focus:outline-none"
        />
      </div>

      {/* Terminal Viewport */}
      <div
        ref={terminalRef}
        className="flex-1 bg-[#121212] border border-slate-800 rounded-lg p-3.5 font-mono text-xs overflow-y-auto space-y-1 shadow-inner"
      >
        {filteredLogs.length === 0 ? (
          <div className="text-slate-500 text-center py-24 flex flex-col items-center justify-center gap-2 select-none">
            <Terminal className="w-8 h-8 opacity-20" />
            <span>暂无符合过滤条件的内核日志输出</span>
          </div>
        ) : (
          filteredLogs.map((item, idx) => (
            <div
              key={idx}
              className="flex items-start gap-2 hover:bg-white/[0.04] px-1.5 py-0.5 rounded transition-colors text-[11px] leading-relaxed"
            >
              <span className="text-slate-500 shrink-0 select-none font-mono text-[10px]">
                [{item.time}]
              </span>
              <span
                className={`text-[9px] px-1.5 py-0.2 rounded font-semibold shrink-0 uppercase tracking-tight ${getLevelBadgeClass(
                  item.level,
                  item.msg
                )}`}
              >
                {item.level || 'INFO'}
              </span>
              <span className="text-slate-200 break-all font-mono">
                {item.msg}
              </span>
            </div>
          ))
        )}
      </div>
    </Drawer>
  )
}

