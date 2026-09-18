import React, { useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Stethoscope, CheckCircle2 } from 'lucide-react'
import { auditCache } from '../../api'

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
  const [result, setResult] = useState<any | null>(null)

  const handleStartAudit = async () => {
    setRunning(true)
    setResult(null)
    try {
      const res = await auditCache()
      setResult(res)
      onToast('缓存体检任务已在后台启动', 'info')
      onRefresh()
    } catch (e: any) {
      onToast(`体检启动失败: ${e.message}`, 'error')
    } finally {
      setRunning(false)
    }
  }

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

        {result && (
          <div className="p-3 bg-emerald-50 border border-emerald-200 rounded-lg text-emerald-800 space-y-1">
            <div className="font-bold flex items-center gap-1.5 text-emerald-900">
              <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              <span>体检任务已触发</span>
            </div>
            <p className="text-[11px] text-emerald-700">
              后台正在全量扫描本地缓存库，扫描与修复结果将自动广播至实时终端日志中。
            </p>
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

          <Button
            variant="primary"
            size="sm"
            loading={running}
            onClick={handleStartAudit}
          >
            立即开始体检
          </Button>
        </div>
      </div>
    </Modal>
  )
}
