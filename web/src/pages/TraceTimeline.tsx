import React, { useState, useRef, useEffect } from 'react'
import { LogItem } from '../types'
import { Terminal, Search, Trash2, ArrowDown } from 'lucide-react'

interface TraceTimelineProps {
  logs: LogItem[]
  onClearLogs: () => void
}

export const TraceTimeline: React.FC<TraceTimelineProps> = ({ logs, onClearLogs }) => {
  const [filterLevel, setFilterLevel] = useState<string>('ALL')
  const [keyword, setKeyword] = useState<string>('')
  const [autoScroll, setAutoScroll] = useState<boolean>(true)
  const terminalRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (autoScroll && terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight
    }
  }, [logs, autoScroll])

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
      return 'bg-rose-950/80 text-rose-400 border border-rose-800'
    }
    if (msg.includes('CACHE-RAM') || msg.includes('RAM')) {
      return 'bg-emerald-950/80 text-emerald-400 border border-emerald-800'
    }
    if (msg.includes('CACHE-DISK') || msg.includes('DISK')) {
      return 'bg-sky-950/80 text-sky-400 border border-sky-800'
    }
    if (msg.includes('PREFETCH')) {
      return 'bg-indigo-950/80 text-indigo-400 border border-indigo-800'
    }
    if (msg.includes('BYPASS') || msg.includes('REST') || msg.includes('/rest/')) {
      return 'bg-amber-950/80 text-amber-400 border border-amber-800'
    }
    return 'bg-slate-800 text-slate-300'
  }

  return (
    <div className="space-y-4">
      {/* Control Bar */}
      <div className="flex flex-wrap items-center justify-between gap-3 bg-[#131c2e] border border-slate-800 p-4 rounded-xl">
        <div className="flex items-center gap-2">
          <Terminal className="w-5 h-5 text-sky-400" />
          <span className="text-sm font-semibold text-white">实时请求监控与控制台流</span>
          <span className="text-xs text-slate-400 font-mono">({filteredLogs.length} 条记录)</span>
        </div>

        <div className="flex items-center gap-3">
          {/* Level Filter */}
          <div className="flex items-center gap-1 bg-slate-900/80 p-1 rounded-lg border border-slate-800 text-xs">
            {['ALL', 'API', 'CACHE', 'PREFETCH', 'ERROR'].map((lvl) => (
              <button
                key={lvl}
                onClick={() => setFilterLevel(lvl)}
                className={`px-2.5 py-1 rounded-md font-medium transition ${
                  filterLevel === lvl ? 'bg-sky-600 text-white' : 'text-slate-400 hover:text-slate-200'
                }`}
              >
                {lvl}
              </button>
            ))}
          </div>

          {/* Search Input */}
          <div className="relative">
            <Search className="w-3.5 h-3.5 absolute left-2.5 top-2.5 text-slate-400" />
            <input
              type="text"
              placeholder="搜索 URL、错误或关键字..."
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              className="bg-slate-900 border border-slate-800 rounded-lg pl-8 pr-3 py-1.5 text-xs text-white placeholder-slate-500 focus:outline-none focus:border-sky-500 w-48"
            />
          </div>

          {/* Auto Scroll Toggle */}
          <button
            onClick={() => setAutoScroll(!autoScroll)}
            className={`p-1.5 rounded-lg border text-xs transition ${
              autoScroll ? 'bg-sky-950/60 border-sky-800 text-sky-400' : 'bg-slate-900 border-slate-800 text-slate-400'
            }`}
            title="自动滚动到底部"
          >
            <ArrowDown className="w-4 h-4" />
          </button>

          {/* Clear Logs */}
          <button
            onClick={onClearLogs}
            className="p-1.5 rounded-lg bg-slate-900 border border-slate-800 text-slate-400 hover:text-rose-400 transition"
            title="清空当前日志"
          >
            <Trash2 className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Terminal Display */}
      <div 
        ref={terminalRef}
        className="bg-[#090e17] border border-slate-800/90 rounded-xl p-4 font-mono text-xs h-[520px] overflow-y-auto space-y-1.5 shadow-inner"
      >
        {filteredLogs.length === 0 ? (
          <div className="text-slate-500 text-center py-20 flex flex-col items-center justify-center gap-2">
            <Terminal className="w-8 h-8 opacity-30" />
            <span>暂无符合过滤条件的日志输出</span>
          </div>
        ) : (
          filteredLogs.map((item, idx) => (
            <div key={idx} className="flex items-start gap-3 hover:bg-slate-900/60 px-2 py-1 rounded transition">
              <span className="text-slate-500 shrink-0 select-none">[{item.time}]</span>
              <span className={`text-[10px] px-1.5 py-0.5 rounded font-semibold shrink-0 uppercase ${getLevelBadgeClass(item.level, item.msg)}`}>
                {item.level || 'INFO'}
              </span>
              <span className="text-slate-200 break-all leading-relaxed">
                {item.msg}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
