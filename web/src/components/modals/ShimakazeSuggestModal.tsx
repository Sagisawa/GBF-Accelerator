import React, { useEffect, useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { ShieldCheck, CheckCircle2, Sparkles } from 'lucide-react'

export interface ShimakazeSuggestModalProps {
  isOpen: boolean
  onClose: () => void
  onEnable: () => Promise<void>
  targetName?: string
}

export const ShimakazeSuggestModal: React.FC<ShimakazeSuggestModalProps> = ({
  isOpen,
  onClose,
  onEnable,
  targetName,
}) => {
  const [loading, setLoading] = useState(false)

  const isAcgp = Boolean(targetName?.includes('8123') || targetName?.toLowerCase().includes('acgpower'))
  const clientName = isAcgp ? 'ACGPower' : '岛风GO'

  const handleConfirm = async () => {
    setLoading(true)
    try {
      await onEnable()
      onClose()
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!isOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && !loading) {
        e.preventDefault()
        handleConfirm()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, loading])

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-amber-600" />
          <span>{clientName} 兼容适配建议</span>
        </div>
      }
      subtitle={`检测到当前上游代理为 ${targetName || (isAcgp ? 'ACGPower (8123)' : '岛风 GO (8099)')}`}
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5">
        <div className="p-3 bg-amber-50/70 border border-amber-200/80 rounded-lg space-y-2">
          <div className="text-xs font-semibold text-amber-900 flex items-center gap-1.5">
            <Sparkles className="w-3.5 h-3.5 text-amber-600 shrink-0" />
            <span>是否立即启用【{clientName} 兼容优化模式】？</span>
          </div>
          <div className="text-[11px] text-amber-800/90 leading-relaxed space-y-1">
            <div className="flex items-start gap-1.5">
              <CheckCircle2 className="w-3 h-3 text-amber-600 shrink-0 mt-0.5" />
              <span>放宽上游代理连接与读写超时，减少大型素材包断流</span>
            </div>
            <div className="flex items-start gap-1.5">
              <CheckCircle2 className="w-3 h-3 text-amber-600 shrink-0 mt-0.5" />
              <span>针对幂等只读请求启用快速重试与自愈</span>
            </div>
            <div className="flex items-start gap-1.5">
              <CheckCircle2 className="w-3 h-3 text-amber-600 shrink-0 mt-0.5" />
              <span>适配 {clientName} 本地自签证书（放行证书）与链式代理转发</span>
            </div>
          </div>
        </div>

        <div className="text-[11px] text-slate-500">
          注：此选项开启后也可随时在主界面的复选框中关闭。
        </div>

        <div className="pt-2 flex justify-end gap-2 border-t border-slate-100">
          <Button
            variant="desktop"
            size="sm"
            onClick={onClose}
            disabled={loading}
          >
            暂不启用
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={handleConfirm}
            loading={loading}
          >
            立即启用
          </Button>
        </div>
      </div>
    </Modal>
  )
}
