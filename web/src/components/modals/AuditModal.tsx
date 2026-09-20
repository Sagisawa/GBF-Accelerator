import React, { useState, useEffect, useRef } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Stethoscope, CheckCircle2, RefreshCw, XCircle } from 'lucide-react'
import { auditCache, fetchCacheTaskStatus, cancelCacheTask } from '../../api'

export interface AuditModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const AuditModal: React.FC<AuditModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
}) => {
  const [running, setRunning] = useState(false)
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const pollTimerRef = useRef<any>(null)

  const fetchStatus = async () => {
    try {
      const data = await fetchCacheTaskStatus()
      setTaskStatus(data)
      setRunning(Boolean(data?.is_auditing))
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

  const handleStartAudit = async () => {
    setRunning(true)
    try {
      await auditCache()
      onToast('缓存体检任务已在后台启动', 'info')
      fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(`体检启动失败: ${e.message}`, 'error')
      setRunning(false)
    }
  }

  const handleCancelAudit = async () => {
    try {
      await cancelCacheTask()
      onToast('已请求终止体检任务', 'info')
      fetchStatus()
    } catch (e: any) {
      onToast(`终止任务失败: ${e.message}`, 'error')
    }
  }

  const isAuditing = Boolean(taskStatus?.is_auditing)
  const isSlimming = Boolean(taskStatus?.is_slimming)
  const progress = taskStatus?.audit_progress

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Stethoscope className="w-4 h-4 text-emerald-600" />
          <span>本地静态缓存健康体检</span>
        </div>
      }
      subtitle="通过 Magic Bytes 二进制文件头深度扫描并清理 0 字节与损坏废件"
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2 leading-relaxed">
          <div className="font-bold text-slate-900">【一键体检缓存】扫描内容：</div>
          <ul className="list-disc list-inside space-y-1 text-slate-600">
            <li>自动扫描本地全部 PNG / JPG / WebP / MP3 / JS 资源</li>
            <li>基于文件头 Magic Bytes 识别并隔离 0 字节损坏文件</li>
            <li>自动拦截 CDN 502/503 异常 HTML 废件</li>
            <li>清理后游戏会自动向官方拉取健康素材，解决黑屏白屏卡死</li>
          </ul>
        </div>

        {/* Realtime Audit Progress */}
        {isAuditing && (
          <div className="p-3 bg-blue-50 border border-blue-200 rounded-lg text-blue-900 space-y-2">
            <div className="font-bold flex items-center justify-between">
              <span className="flex items-center gap-1.5">
                <RefreshCw className="w-3.5 h-3.5 animate-spin text-blue-600" />
                <span>正在全量扫描本地缓存中...</span>
              </span>
              <Button
                variant="desktop"
                size="xs"
                onClick={handleCancelAudit}
                icon={<XCircle className="w-3 h-3 text-red-600" />}
              >
                终止扫描
              </Button>
            </div>
            <div className="grid grid-cols-3 gap-2 text-[11px] font-mono text-blue-800 pt-1">
              <div>已扫描: <strong>{progress?.scanned ?? 0}</strong></div>
              <div>健康正常: <strong>{progress?.healthy ?? 0}</strong></div>
              <div>损坏隔离: <strong className="text-red-600">{progress?.corrupted ?? 0}</strong></div>
            </div>
          </div>
        )}

        {!isAuditing && taskStatus?.last_audit_result && (
          <div className="p-3 bg-emerald-50 border border-emerald-200 rounded-lg text-emerald-800 space-y-1">
            <div className="font-bold flex items-center gap-1.5 text-emerald-900">
              <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              <span>最近一次体检完成</span>
            </div>
            <div className="text-[11px] text-emerald-700 font-mono">
              扫描总数: {taskStatus.last_audit_result.scanned ?? 0} ｜ 
              健康: {taskStatus.last_audit_result.healthy ?? 0} ｜ 
              损坏修复: {taskStatus.last_audit_result.corrupted ?? 0}
            </div>
          </div>
        )}

        <div className="flex items-center justify-between pt-1">
          <Button
            variant="desktop"
            size="sm"
            onClick={onClose}
          >
            关闭 (Esc)
          </Button>

          {isAuditing ? (
            <Button
              variant="desktop"
              size="sm"
              onClick={handleCancelAudit}
              icon={<XCircle className="w-3.5 h-3.5 text-red-600" />}
            >
              终止体检
            </Button>
          ) : (
            <Button
              variant="primary"
              size="sm"
              loading={running}
              disabled={isSlimming}
              onClick={handleStartAudit}
            >
              {isSlimming ? '瘦身任务执行中...' : '立即开始体检'}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  )
}
