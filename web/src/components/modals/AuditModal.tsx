import React, { useState, useEffect, useRef } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Stethoscope, CheckCircle2, RefreshCw, XCircle } from 'lucide-react'
import { auditCache, fetchCacheTaskStatus, cancelCacheTask } from '../../api'
import { useI18n } from '../../i18n'

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
  const { t } = useI18n()
  const [running, setRunning] = useState(false)
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const pollTimerRef = useRef<any>(null)
  const auditTaskIdRef = useRef<string | null>(null)

  const fetchStatus = async () => {
    try {
      const data = await fetchCacheTaskStatus()
      setTaskStatus(data)
      setRunning(Boolean(data?.is_auditing))
      if (data?.is_auditing && data?.active_task_id) {
        auditTaskIdRef.current = String(data.active_task_id)
      } else if (!data?.is_auditing) {
        auditTaskIdRef.current = null
      }
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
      const res = await auditCache()
      if (res?.task_id) {
        auditTaskIdRef.current = String(res.task_id)
      }
      onToast(t('modals.audit.auditStarted'), 'info')
      fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(t('modals.audit.startFailed', { error: e.message }), 'error')
      setRunning(false)
    }
  }

  const handleCancelAudit = async () => {
    try {
      const targetTaskId =
        (taskStatus?.is_auditing && taskStatus?.active_task_id
          ? String(taskStatus.active_task_id)
          : '') ||
        auditTaskIdRef.current ||
        ''
      if (!targetTaskId) {
        await fetchStatus()
        return
      }
      await cancelCacheTask(targetTaskId)
      onToast(t('modals.audit.auditStopped'), 'info')
      fetchStatus()
    } catch (e: any) {
      onToast(t('modals.audit.stopFailed', { error: e.message }), 'error')
    }
  }

  const isAuditing = Boolean(taskStatus?.is_auditing)
  const isSlimming = Boolean(taskStatus?.is_slimming)
  const isOtherTaskActive =
    !isAuditing &&
    Boolean(
      taskStatus?.active_task_id ||
        isSlimming ||
        taskStatus?.is_scanning ||
        taskStatus?.is_clearing_category ||
        taskStatus?.is_boosting
    )
  const progress = taskStatus?.audit_progress

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Stethoscope className="w-4 h-4 text-emerald-600" />
          <span>{t('modals.audit.title')}</span>
        </div>
      }
      subtitle={t('modals.audit.subtitle')}
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2 leading-relaxed">
          <div className="font-bold text-slate-900">{t('modals.audit.firstScanTitle')}</div>
          <ul className="list-disc list-inside space-y-1 text-slate-600">
            <li>{t('modals.audit.firstScanItem1')}</li>
            <li>{t('modals.audit.firstScanItem2')}</li>
            <li>{t('modals.audit.firstScanItem3')}</li>
            <li>{t('modals.audit.firstScanItem4')}</li>
          </ul>
        </div>

        {/* Realtime Audit Progress */}
        {isAuditing && (
          <div className="p-3 bg-blue-50 border border-blue-200 rounded-lg text-blue-900 space-y-2">
            <div className="font-bold flex items-center justify-between">
              <span className="flex items-center gap-1.5">
                <RefreshCw className="w-3.5 h-3.5 animate-spin text-blue-600" />
                <span>{t('modals.audit.statusRunning')}</span>
              </span>
              <Button
                variant="desktop"
                size="xs"
                onClick={handleCancelAudit}
                icon={<XCircle className="w-3 h-3 text-red-600" />}
              >
                {t('modals.audit.stopAudit')}
              </Button>
            </div>
            <div className="grid grid-cols-3 gap-2 text-[11px] font-mono text-blue-800 pt-1">
              <div>{t('modals.audit.scannedLabel', { count: progress?.scanned ?? 0 })}</div>
              <div>{t('modals.audit.healthyLabel', { count: progress?.healthy ?? 0 })}</div>
              <div>{t('modals.audit.corruptedLabel', { count: progress?.corrupted ?? 0 })}</div>
            </div>
          </div>
        )}

        {!isAuditing && taskStatus?.last_audit_result && (
          <div className="p-3 bg-emerald-50 border border-emerald-200 rounded-lg text-emerald-800 space-y-1">
            <div className="font-bold flex items-center gap-1.5 text-emerald-900">
              <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              <span>{t('modals.audit.statusCompleted')}</span>
            </div>
            <div className="text-[11px] text-emerald-700 font-mono">
              {t('modals.audit.scannedLabel', { count: taskStatus.last_audit_result.scanned ?? 0 })} ｜ 
              {t('modals.audit.healthyLabel', { count: taskStatus.last_audit_result.healthy ?? 0 })} ｜ 
              {t('modals.audit.repairedLabel', { count: taskStatus.last_audit_result.corrupted ?? 0 })}
            </div>
          </div>
        )}

        <div className="flex items-center justify-between pt-1">
          <Button
            variant="desktop"
            size="sm"
            onClick={onClose}
          >
            {t('common.closeEsc')}
          </Button>

          {isAuditing ? (
            <Button
              variant="desktop"
              size="sm"
              onClick={handleCancelAudit}
              icon={<XCircle className="w-3.5 h-3.5 text-red-600" />}
            >
              {t('modals.audit.stopAudit')}
            </Button>
          ) : (
            <Button
              variant="primary"
              size="sm"
              loading={running}
              disabled={running || isOtherTaskActive}
              onClick={handleStartAudit}
            >
              {isSlimming
                ? t('modals.clearCache.slimmingProgress')
                : isOtherTaskActive
                  ? t('modals.boost.otherTaskActive')
                  : t('modals.audit.startAudit')}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  )
}
