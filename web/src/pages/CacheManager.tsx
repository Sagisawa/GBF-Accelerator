import React, { useState } from 'react'
import { CacheStats } from '../types'
import { clearCache, auditCache, slimCache } from '../api'
import { Database, HardDrive, Trash2, Wrench, RefreshCw, CheckCircle2, ShieldAlert } from 'lucide-react'

interface CacheManagerProps {
  stats: CacheStats | null
  onRefresh: () => void
}

export const CacheManager: React.FC<CacheManagerProps> = ({ stats, onRefresh }) => {
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ text: string; type: 'success' | 'info' | 'error' } | null>(null)

  const handleClearRam = async () => {
    if (!confirm('确认清空 RAM 内存热缓存？(磁盘缓存不会受影响)')) return
    setBusy(true)
    try {
      await clearCache(true)
      setMsg({ text: 'RAM 内存热缓存已成功清空！', type: 'success' })
      onRefresh()
    } catch (e: any) {
      setMsg({ text: `操作失败: ${e.message}`, type: 'error' })
    } finally {
      setBusy(false)
    }
  }

  const handleClearAll = async () => {
    if (!confirm('⚠️ 警告：确认清空所有缓存（包括磁盘文件）？首次加载素材将重新从网络下载。')) return
    setBusy(true)
    try {
      const res = await clearCache(false)
      setMsg({ text: `全部缓存已清空，释放文件 ${res.disk_files_deleted || 0} 个`, type: 'success' })
      onRefresh()
    } catch (e: any) {
      setMsg({ text: `操作失败: ${e.message}`, type: 'error' })
    } finally {
      setBusy(false)
    }
  }

  const handleAudit = async () => {
    setBusy(true)
    try {
      await auditCache()
      setMsg({ text: '缓存自检与自愈任务已在后台启动！检查损坏文件与 0 字节文件...', type: 'info' })
      setTimeout(onRefresh, 1500)
    } catch (e: any) {
      setMsg({ text: `启动失败: ${e.message}`, type: 'error' })
    } finally {
      setBusy(false)
    }
  }

  const handleSlim = async () => {
    if (!confirm('确认安全精简历史过期版本缓存？仅保留最新活跃版本，不会影响游戏正常素材。')) return
    setBusy(true)
    try {
      await slimCache(8)
      setMsg({ text: '历史旧版本缓存精简任务已在后台执行！', type: 'info' })
      setTimeout(onRefresh, 1500)
    } catch (e: any) {
      setMsg({ text: `启动失败: ${e.message}`, type: 'error' })
    } finally {
      setBusy(false)
    }
  }

  if (!stats) {
    return <div className="text-slate-400 p-8 text-center">正在读取缓存统计...</div>
  }

  return (
    <div className="space-y-6">
      {msg && (
        <div className={`p-4 rounded-xl border text-sm flex items-center gap-2 ${
          msg.type === 'success' 
            ? 'bg-emerald-950/60 border-emerald-800 text-emerald-300' 
            : msg.type === 'info'
            ? 'bg-sky-950/60 border-sky-800 text-sky-300'
            : 'bg-rose-950/60 border-rose-800 text-rose-300'
        }`}>
          <CheckCircle2 className="w-4 h-4 shrink-0" />
          <span>{msg.text}</span>
        </div>
      )}

      {/* 1. Storage Overview Grid */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
        {/* RAM Cache */}
        <div className="bg-[#131c2e] border border-slate-800 rounded-2xl p-6">
          <div className="flex items-center justify-between text-slate-400 mb-2">
            <span className="text-xs font-semibold uppercase tracking-wider">RAM 内存热存</span>
            <Database className="w-5 h-5 text-emerald-400" />
          </div>
          <div className="text-3xl font-bold font-mono text-white">
            {stats.ram_mb} <span className="text-sm font-normal text-slate-400">MB</span>
          </div>
          <p className="text-xs text-slate-400 mt-2">
            当前驻留热项: <strong className="text-slate-200 font-mono">{stats.ram_items}</strong> 项 / 上限 {stats.ram_max_mb} MB
          </p>
          <div className="w-full bg-slate-800 rounded-full h-1.5 mt-4 overflow-hidden">
            <div 
              className="bg-emerald-400 h-1.5 rounded-full transition-all duration-500"
              style={{ width: `${Math.min(100, (stats.ram_mb / (stats.ram_max_mb || 256)) * 100)}%` }}
            />
          </div>
        </div>

        {/* Disk Base Directory */}
        <div className="bg-[#131c2e] border border-slate-800 rounded-2xl p-6 md:col-span-2">
          <div className="flex items-center justify-between text-slate-400 mb-2">
            <span className="text-xs font-semibold uppercase tracking-wider">本地磁盘缓存基准目录</span>
            <HardDrive className="w-5 h-5 text-sky-400" />
          </div>
          <div className="text-sm font-mono text-slate-200 bg-slate-900/80 p-3 rounded-lg border border-slate-800 truncate">
            {stats.cache_base || '未设置'}
          </div>
          <div className="flex items-center justify-between text-xs text-slate-400 mt-3">
            <span>总计命中: <strong className="text-emerald-400 font-mono">{stats.hits_total}</strong> 次</span>
            <span>命中率: <strong className="text-sky-400 font-mono">{stats.hit_ratio_percent}%</strong></span>
            <span>未命中: <strong className="text-slate-400 font-mono">{stats.misses}</strong> 次</span>
          </div>
        </div>
      </div>

      {/* 2. Management & Maintenance Actions */}
      <div className="bg-[#131c2e] border border-slate-800 rounded-2xl p-6 space-y-4">
        <h3 className="text-base font-semibold text-white tracking-wide flex items-center gap-2">
          <Wrench className="w-4 h-4 text-sky-400" /> 缓存维护与健康管理
        </h3>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 pt-2">
          {/* Audit */}
          <button
            onClick={handleAudit}
            disabled={busy}
            className="flex flex-col items-start p-4 bg-slate-900/60 hover:bg-slate-800/80 border border-slate-800 hover:border-slate-700 rounded-xl transition text-left group"
          >
            <div className="flex items-center gap-2 text-sky-400 font-medium text-sm mb-1">
              <RefreshCw className="w-4 h-4 group-hover:rotate-180 transition-transform duration-500" />
              <span>缓存体检与自愈</span>
            </div>
            <p className="text-xs text-slate-400">
              扫描 0 字节与损坏文件，自动校验媒体魔数并清理异常缓存。
            </p>
          </button>

          {/* Slim */}
          <button
            onClick={handleSlim}
            disabled={busy}
            className="flex flex-col items-start p-4 bg-slate-900/60 hover:bg-slate-800/80 border border-slate-800 hover:border-slate-700 rounded-xl transition text-left group"
          >
            <div className="flex items-center gap-2 text-indigo-400 font-medium text-sm mb-1">
              <HardDrive className="w-4 h-4" />
              <span>历史版本安全瘦身</span>
            </div>
            <p className="text-xs text-slate-400">
              安全清理过旧的历史素材版本目录，仅保留最近 8 个活跃版本。
            </p>
          </button>

          {/* Clear RAM */}
          <button
            onClick={handleClearRam}
            disabled={busy}
            className="flex flex-col items-start p-4 bg-slate-900/60 hover:bg-slate-800/80 border border-slate-800 hover:border-slate-700 rounded-xl transition text-left group"
          >
            <div className="flex items-center gap-2 text-amber-400 font-medium text-sm mb-1">
              <Trash2 className="w-4 h-4" />
              <span>清空内存热缓存</span>
            </div>
            <p className="text-xs text-slate-400">
              立即释放占用的物理 RAM 内存，不影响已持久化的磁盘缓存。
            </p>
          </button>

          {/* Clear All */}
          <button
            onClick={handleClearAll}
            disabled={busy}
            className="flex flex-col items-start p-4 bg-rose-950/20 hover:bg-rose-950/40 border border-rose-900/40 hover:border-rose-800 rounded-xl transition text-left group"
          >
            <div className="flex items-center gap-2 text-rose-400 font-medium text-sm mb-1">
              <ShieldAlert className="w-4 h-4" />
              <span>清空全部缓存</span>
            </div>
            <p className="text-xs text-slate-400">
              彻底抹除所有本地缓存（慎用，后续重复加载需重新下载）。
            </p>
          </button>
        </div>
      </div>
    </div>
  )
}
