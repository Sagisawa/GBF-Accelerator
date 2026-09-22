import React, { useCallback, useEffect, useState } from 'react'
import { CheckCircle2, ExternalLink, PlugZap, RefreshCw, XCircle } from 'lucide-react'
import { Modal } from '../common/Modal'
import { fetchTarouIntegrationStatus, setTarouIntegrationEnabled } from '../../api'
import { TarouIntegrationStatus } from '../../types'

interface TarouIntegrationModalProps {
  isOpen: boolean
  onClose: () => void
  onToast?: (message: string, type?: 'success' | 'info' | 'error') => void
}

export const TarouIntegrationModal: React.FC<TarouIntegrationModalProps> = ({
  isOpen,
  onClose,
  onToast,
}) => {
  const [status, setStatus] = useState<TarouIntegrationStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [refreshing, setRefreshing] = useState(false)

  const refresh = useCallback(async () => {
    setRefreshing(true)
    try {
      setStatus(await fetchTarouIntegrationStatus())
    } catch (e: any) {
      onToast?.(e?.message || '读取 Tarou 集成状态失败', 'error')
    } finally {
      setRefreshing(false)
    }
  }, [onToast])

  useEffect(() => {
    if (isOpen) refresh()
  }, [isOpen, refresh])

  const toggle = async () => {
    if (loading || !status) return
    setLoading(true)
    try {
      const next = await setTarouIntegrationEnabled(!status.enabled)
      setStatus(next)
      onToast?.(next.enabled ? 'Tarou 集成已启用' : 'Tarou 集成已关闭', 'info')
    } catch (e: any) {
      onToast?.(e?.message || '修改 Tarou 集成设置失败', 'error')
    } finally {
      setLoading(false)
    }
  }

  const connected = Boolean(status?.connected)
  const enabled = Boolean(status?.enabled)
  const badgeClass = !status
    ? 'bg-slate-100 text-slate-500 border-slate-200'
    : connected
      ? 'bg-emerald-50 text-emerald-700 border-emerald-200'
      : enabled
        ? 'bg-amber-50 text-amber-700 border-amber-200'
        : 'bg-slate-100 text-slate-500 border-slate-200'

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <PlugZap className="w-4 h-4 text-sky-600" />
          <span>Tarou 集成</span>
        </div>
      }
      subtitle="可选浏览器扩展集成（不影响加速数据面）"
      maxWidth="max-w-lg"
    >
      <div className="space-y-4">
        <div className="flex items-start justify-between gap-3 rounded-xl border border-slate-200 bg-slate-50/70 p-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-base">🧩</span>
              <div className="font-bold text-slate-900">Chrome-Extension-Tarou</div>
            </div>
            <div className="text-xs text-slate-500 mt-1">
              可选集成模块 · 原项目：Waaatanuki
            </div>
          </div>
          <div className={badgeClass + ' shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-xs font-semibold'}>
            {connected ? <CheckCircle2 className="w-3.5 h-3.5" /> : <XCircle className="w-3.5 h-3.5" />}
            {connected ? '已连接' : enabled ? '等待连接' : '未启用'}
          </div>
        </div>

        <div className="rounded-xl border border-sky-200/80 bg-sky-50/70 p-4 text-xs leading-relaxed text-sky-950">
          <div className="font-semibold mb-1">当前仅提供集成接口，不捆绑 Tarou 源码。</div>
          <div>
            未来经作者授权后的 Tarou 适配版本可通过本地 Bridge 与 Accelerator 通信；已经安装 Tarou 的用户无需迁移现有扩展。
          </div>
          <div className="mt-1">
            加速器的数据代理（8124）与控制面（8125）不会依赖 Tarou 才能运行。
          </div>
        </div>

        <div className="space-y-2 text-xs text-slate-600">
          <div className="flex items-center justify-between gap-3">
            <span>协议版本</span>
            <span className="font-mono font-semibold text-slate-800">v{status?.protocol_version ?? status?.supported_protocol_version ?? 1}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span>Tarou 扩展版本</span>
            <span className="font-mono text-slate-800">{status?.extension_version || '--'}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span>最近连接</span>
            <span className="font-mono text-slate-800 text-right">
              {status?.last_seen_at ? new Date(status.last_seen_at).toLocaleString() : '--'}
            </span>
          </div>
        </div>

        <div className="pt-2 border-t border-slate-100 flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={refresh}
              disabled={refreshing}
              className="px-3 py-1.5 rounded-lg text-xs font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 border border-slate-200 transition-all disabled:opacity-50 inline-flex items-center gap-1.5"
            >
              <RefreshCw className={refreshing ? 'w-3.5 h-3.5 animate-spin' : 'w-3.5 h-3.5'} />
              刷新状态
            </button>
            {status?.source_url && (
              <a
                href={status.source_url}
                target="_blank"
                rel="noreferrer"
                className="px-3 py-1.5 rounded-lg text-xs font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 border border-slate-200 transition-all inline-flex items-center gap-1.5"
              >
                <ExternalLink className="w-3.5 h-3.5" />
                原项目
              </a>
            )}
          </div>
          <button
            type="button"
            onClick={toggle}
            disabled={loading || !status}
            className={enabled
              ? 'px-4 py-1.5 rounded-lg text-xs font-bold text-white bg-slate-700 hover:bg-slate-800 transition-all'
              : 'px-4 py-1.5 rounded-lg text-xs font-bold text-white bg-sky-600 hover:bg-sky-700 transition-all'}
          >
            {loading ? '处理中...' : enabled ? '关闭集成' : '启用集成'}
          </button>
        </div>
      </div>
    </Modal>
  )
}
