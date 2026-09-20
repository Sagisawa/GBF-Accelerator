import React, { useState, useEffect, useRef } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Sparkles, CheckCircle2, RefreshCw, XCircle } from 'lucide-react'
import { slimCache, fetchCacheTaskStatus, cancelCacheTask } from '../../api'

export interface SlimModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const SlimModal: React.FC<SlimModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
}) => {
  const [keepCount, setKeepCount] = useState<number>(8)
  const [running, setRunning] = useState(false)
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const pollTimerRef = useRef<any>(null)

  const fetchStatus = async () => {
    try {
      const data = await fetchCacheTaskStatus()
      setTaskStatus(data)
      setRunning(Boolean(data?.is_slimming))
    } catch {}
  }

  useEffect(() => {
    if (isOpen) {
      fetchStatus()
      pollTimerRef.current = setInterval(fetchStatus, 1000)
    } else {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current)
    }
    return () => {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current)
    }
  }, [isOpen])

  const handleStartSlim = async () => {
    setRunning(true)
    try {
      await slimCache(keepCount)
      onToast('缓存瘦身任务已在后台执行', 'info')
      fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(`瘦身启动失败: ${e.message}`, 'error')
      setRunning(false)
    }
  }

  const handleCancelSlim = async () => {
    try {
      await cancelCacheTask()
      onToast('已请求终止瘦身任务', 'info')
      fetchStatus()
    } catch (e: any) {
      onToast(`终止任务失败: ${e.message}`, 'error')
    }
  }

  const isSlimming = Boolean(taskStatus?.is_slimming)
  const isAuditing = Boolean(taskStatus?.is_auditing)
  const slimProgress = taskStatus?.slim_progress

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Sparkles className="w-4 h-4 text-blue-600" />
          <span>缓存安全瘦身与历史版本清理</span>
        </div>
      }
      subtitle="仅清理废弃历史版本代码碎片，立绘、语音与全部公共素材 100% 完好保留"
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2 leading-relaxed">
          <div className="font-bold text-slate-900">安全瘦身说明：</div>
          <p className="text-slate-600">
            碧蓝幻想每次游戏更新都会生成新的版本哈希目录。长久运行后，磁盘中会残留大量已被游戏废弃的历史版本代码文件。
          </p>
          <div className="text-[11px] text-slate-600 space-y-1">
            <div>• <strong>保留最新版本数：</strong>默认保留最近 {keepCount} 个健康更新版本</div>
            <div>• <strong>完全不受影响：</strong>所有角色立绘、武器召唤、背景及音频等高价值大素材全部完好无损</div>
          </div>
        </div>

        <div className="flex items-center justify-between p-3 bg-slate-50 border border-slate-200 rounded-lg">
          <span className="font-semibold text-slate-800">保留最新版本数量：</span>
          <div className="flex items-center gap-2">
            {[4, 8, 12].map((num) => (
              <button
                key={num}
                type="button"
                disabled={isSlimming}
                onClick={() => setKeepCount(num)}
                className={`px-2.5 py-1 rounded text-xs font-mono border transition-colors ${
                  keepCount === num
                    ? 'bg-blue-600 text-white border-blue-600 font-bold'
                    : 'bg-white text-slate-700 border-slate-300 hover:bg-slate-100'
                } ${isSlimming ? 'opacity-50 cursor-not-allowed' : ''}`}
              >
                {num} 个
              </button>
            ))}
          </div>
        </div>

        {/* Realtime Slim Progress */}
        {isSlimming && (
          <div className="p-3 bg-blue-50 border border-blue-200 rounded-lg text-blue-900 space-y-2">
            <div className="font-bold flex items-center justify-between">
              <span className="flex items-center gap-1.5">
                <RefreshCw className="w-3.5 h-3.5 animate-spin text-blue-600" />
                <span>正在扫描并清理历史废弃版本...</span>
              </span>
              <Button
                variant="desktop"
                size="xs"
                onClick={handleCancelSlim}
                icon={<XCircle className="w-3 h-3 text-red-600" />}
              >
                终止瘦身
              </Button>
            </div>
            <div className="grid grid-cols-2 gap-2 text-[11px] font-mono text-blue-800 pt-1">
              <div>目录扫描: <strong>{slimProgress?.current_idx ?? 0} / {slimProgress?.total_dirs ?? 0}</strong></div>
              <div>已清理废件: <strong>{slimProgress?.deleted_files ?? 0} 个</strong></div>
              <div className="col-span-2">
                已释放空间: <strong>{(((slimProgress?.freed_bytes ?? 0) / 1024 / 1024)).toFixed(2)} MB</strong>
              </div>
            </div>
          </div>
        )}

        {!isSlimming && taskStatus?.last_slim_result && (
          <div className="p-3 bg-emerald-50 border border-emerald-200 rounded-lg text-emerald-800 space-y-1">
            <div className="font-bold flex items-center gap-1.5 text-emerald-900">
              <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              <span>最近一次瘦身完成</span>
            </div>
            <div className="text-[11px] text-emerald-700 font-mono">
              清理废件: {taskStatus.last_slim_result.deleted_files ?? 0} 个 ｜ 
              释放磁盘空间: {(((taskStatus.last_slim_result.freed_bytes ?? 0) / 1024 / 1024)).toFixed(2)} MB
            </div>
          </div>
        )}

        <div className="flex items-center justify-between pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>

          {isSlimming ? (
            <Button
              variant="desktop"
              size="sm"
              onClick={handleCancelSlim}
              icon={<XCircle className="w-3.5 h-3.5 text-red-600" />}
            >
              终止瘦身
            </Button>
          ) : (
            <Button
              variant="primary"
              size="sm"
              loading={running}
              disabled={isAuditing}
              onClick={handleStartSlim}
            >
              {isAuditing ? '体检任务执行中...' : '开始安全瘦身'}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  )
}
